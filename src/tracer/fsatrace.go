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
	cfg        Config
	outputPath string
	binaryPath string
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
// 2. System PATH
func findFsatraceBinary() (string, error) {
	// Check fsatrace submodule directory relative to executable
	if execPath, err := os.Executable(); err == nil {
		submodulePath := filepath.Join(filepath.Dir(execPath), "fsatrace", "fsatrace")
		if runtime.GOOS == "windows" {
			submodulePath += ".exe"
		}
		if _, err := os.Stat(submodulePath); err == nil {
			return submodulePath, nil
		}
	}

	// Check relative to current working directory
	cwd, _ := os.Getwd()
	submodulePath := filepath.Join(cwd, "fsatrace", "fsatrace")
	if runtime.GOOS == "windows" {
		submodulePath += ".exe"
	}
	if _, err := os.Stat(submodulePath); err == nil {
		return submodulePath, nil
	}

	// Check PATH
	path, err := exec.LookPath("fsatrace")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("fsatrace binary not found: build it with 'make' in fsatrace/ or install to PATH")
}

// WrapCommand prepends fsatrace to the command.
// fsatrace <options> <output-file> -- <command>
func (t *fsatracer) WrapCommand(shell []string, cmdStr string) (*exec.Cmd, error) {
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

// Cleanup removes the temporary output file.
func (t *fsatracer) Cleanup() error {
	if t.outputPath != "" {
		return os.Remove(t.outputPath)
	}
	return nil
}
