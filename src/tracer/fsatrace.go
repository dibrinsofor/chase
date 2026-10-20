//go:build !experimental_ebpf

package tracer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type fsatracer struct {
	cfg             Config
	outputPath      string
	binaryPath      string
	copiedShellPath string // temp copy of shell for macOS SIP workaround
}

func newPlatformTracer(cfg Config) (Tracer, error) {
	binaryPath, err := findFsatraceBinary()
	if err != nil {
		return nil, err
	}

	tmpFile, err := os.CreateTemp("", "fsatrace-*.log")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpFile.Close()

	return &fsatracer{
		cfg:        cfg,
		outputPath: tmpFile.Name(),
		binaryPath: binaryPath,
	}, nil
}

// findFsatraceBinary locates the fsatrace binary.
// It checks:
// 1. The fsatrace/ submodule directory (built binary)
// 2. Parent directories (for running from subdirectories)
// 3. System PATH
func findFsatraceBinary() (string, error) {
	binaryName := "fsatrace"
	if runtime.GOOS == "windows" {
		binaryName = "fsatrace.exe"
	}

	// Check fsatrace submodule directory relative to executable
	if execPath, err := os.Executable(); err == nil {
		submodulePath := filepath.Join(filepath.Dir(execPath), "fsatrace", binaryName)
		if _, err := os.Stat(submodulePath); err == nil {
			return submodulePath, nil
		}
	}

	// Check relative to current working directory and parent directories
	// This handles running tests from subdirectories like src/tracer/
	cwd, _ := os.Getwd()
	dir := cwd
	for i := 0; i < 5; i++ { // Check up to 5 levels up
		submodulePath := filepath.Join(dir, "fsatrace", binaryName)
		if _, err := os.Stat(submodulePath); err == nil {
			return submodulePath, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached root
		}
		dir = parent
	}

	// Check PATH
	path, err := exec.LookPath("fsatrace")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("fsatrace binary not found: build it with 'make' in fsatrace/ or install to PATH")
}

// isProtectedPath checks if a binary path is protected by macOS SIP.
func isProtectedPath(path string) bool {
	protected := []string{"/bin/", "/usr/bin/", "/usr/sbin/", "/sbin/"}
	for _, p := range protected {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// ensureUnprotectedShell copies the shell to a temp location on macOS if needed.
// SIP blocks DYLD_INSERT_LIBRARIES for binaries in protected paths.
// Copying the shell to /tmp bypasses this restriction.
func (t *fsatracer) ensureUnprotectedShell(shell []string) ([]string, error) {
	if runtime.GOOS != "darwin" || len(shell) == 0 {
		return shell, nil
	}

	shellPath := shell[0]
	if !isProtectedPath(shellPath) {
		return shell, nil
	}

	// Copy shell to temp directory
	tmpShell, err := os.CreateTemp("", "chase-shell-*")
	if err != nil {
		return shell, err
	}
	tmpShell.Close()

	data, err := os.ReadFile(shellPath)
	if err != nil {
		os.Remove(tmpShell.Name())
		return shell, err
	}
	if err := os.WriteFile(tmpShell.Name(), data, 0755); err != nil {
		os.Remove(tmpShell.Name())
		return shell, err
	}

	t.copiedShellPath = tmpShell.Name()

	newShell := make([]string, len(shell))
	copy(newShell, shell)
	newShell[0] = tmpShell.Name()
	return newShell, nil
}

// WrapCommand prepends fsatrace to the command.
// fsatrace <options> <output-file> -- <command>
func (t *fsatracer) WrapCommand(shell []string, cmdStr string) (*exec.Cmd, error) {
	// On macOS, copy protected shells to bypass SIP
	shell, _ = t.ensureUnprotectedShell(shell)
	// Build options string based on config
	opts := "rwmq" // read, write, move, stat
	if !t.cfg.CaptureReads {
		opts = strings.Replace(opts, "r", "", 1)
		opts = strings.Replace(opts, "q", "", 1)
	}
	if !t.cfg.CaptureWrites {
		opts = strings.Replace(opts, "w", "", 1)
		opts = strings.Replace(opts, "m", "", 1)
	}

	// fsatrace rwmq output.log -- sh -c "command"
	args := []string{opts, t.outputPath, "--"}
	args = append(args, shell...)
	args = append(args, cmdStr)

	cmd := exec.Command(t.binaryPath, args...)
	return cmd, nil
}

// ParseOutput reads and parses the fsatrace output file.
// Format: r|/path, w|/path, m|/dest|/src, d|/path, q|/path
func (t *fsatracer) ParseOutput() ([]FileAccess, error) {
	data, err := os.ReadFile(t.outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read fsatrace output: %w", err)
	}

	return parseOutput(data), nil
}

func parseOutput(data []byte) []FileAccess {
	var accesses []FileAccess
	seen := make(map[string]bool)

	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}

		parts := bytes.SplitN(line, []byte("|"), 3)
		if len(parts) < 2 {
			continue
		}

		var op Operation
		switch parts[0][0] {
		case 'w', 'm', 't':
			op = OpWrite
		case 'r', 'q':
			op = OpRead
		case 'd':
			// Ignore deletes
			continue
		default:
			continue
		}

		path := string(parts[1])

		// For move operations, parts[1] is destination (write)
		// parts[2] would be source (read) if we wanted to track it

		// Deduplicate
		key := fmt.Sprintf("%d:%s", op, path)
		if seen[key] {
			continue
		}
		seen[key] = true

		accesses = append(accesses, FileAccess{
			Path:      path,
			Operation: op,
		})
	}

	return accesses
}

// Cleanup removes the temporary output file and copied shell.
func (t *fsatracer) Cleanup() error {
	if t.copiedShellPath != "" {
		os.Remove(t.copiedShellPath)
	}
	if t.outputPath != "" {
		return os.Remove(t.outputPath)
	}
	return nil
}
