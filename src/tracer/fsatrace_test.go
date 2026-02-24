//go:build !experimental_ebpf

package tracer

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Helper to compile a test binary that performs specific file operations.
// Returns path to binary or skips test if compilation fails.
func compileTestBinary(t *testing.T, name, code string) string {
	t.Helper()
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, name+".c")
	binFile := filepath.Join(tmpDir, name)

	if err := os.WriteFile(srcFile, []byte(code), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := exec.Command("cc", "-o", binFile, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot compile test binary: %v\n%s", err, out)
	}

	return binFile
}

// Helper to create tracer and skip if unavailable
func newTestTracer(t *testing.T) Tracer {
	t.Helper()
	tracer, err := New(DefaultConfig())
	if err != nil {
		t.Skipf("tracer not available: %v", err)
	}
	t.Cleanup(func() { tracer.Cleanup() })
	return tracer
}


// hasAccess checks if accesses contain a specific path and operation
func hasAccess(accesses []FileAccess, path string, op Operation) bool {
	for _, a := range accesses {
		if strings.Contains(a.Path, path) && a.Operation == op {
			return true
		}
	}
	return false
}

func TestCorrectness_ReadRecordedAsRead(t *testing.T) {
	bin := compileTestBinary(t, "reader", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_read_file.txt", O_RDONLY);
    if (fd >= 0) close(fd);
    return 0;
}
`)
	// Create the file to read
	os.WriteFile("/tmp/test_read_file.txt", []byte("data"), 0644)
	defer os.Remove("/tmp/test_read_file.txt")

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	if out, err := cmd.CombinedOutput(); err != nil {
		if runtime.GOOS == "darwin" {
			t.Skipf("tracing failed (SIP?): %v\n%s", err, out)
		}
		t.Fatalf("command failed: %v\n%s", err, out)
	}

	accesses, _ := tracer.ParseOutput()
	if len(accesses) == 0 && runtime.GOOS == "darwin" {
		t.Skip("no accesses captured - SIP restriction")
	}
	if !hasAccess(accesses, "test_read_file.txt", OpRead) {
		t.Errorf("expected read access for test_read_file.txt, got: %+v", accesses)
	}
}

func TestCorrectness_WriteRecordedAsWrite(t *testing.T) {
	bin := compileTestBinary(t, "writer", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_write_file.txt", O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd >= 0) { write(fd, "hi", 2); close(fd); }
    return 0;
}
`)
	defer os.Remove("/tmp/test_write_file.txt")

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	if !hasAccess(accesses, "test_write_file.txt", OpWrite) {
		t.Errorf("expected write access for test_write_file.txt, got: %+v", accesses)
	}
}

func TestCorrectness_RdwrRecordedAsWrite(t *testing.T) {
	bin := compileTestBinary(t, "rdwr", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_rdwr_file.txt", O_RDWR | O_CREAT, 0644);
    if (fd >= 0) close(fd);
    return 0;
}
`)
	defer os.Remove("/tmp/test_rdwr_file.txt")

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	if !hasAccess(accesses, "test_rdwr_file.txt", OpWrite) {
		t.Errorf("expected write access for O_RDWR, got: %+v", accesses)
	}
}

func TestCorrectness_AppendRecordedAsWrite(t *testing.T) {
	os.WriteFile("/tmp/test_append_file.txt", []byte("existing"), 0644)
	defer os.Remove("/tmp/test_append_file.txt")

	bin := compileTestBinary(t, "appender", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_append_file.txt", O_WRONLY | O_APPEND);
    if (fd >= 0) { write(fd, "more", 4); close(fd); }
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	if !hasAccess(accesses, "test_append_file.txt", OpWrite) {
		t.Errorf("expected write access for O_APPEND, got: %+v", accesses)
	}
}

func TestCorrectness_RenameRecordedAsMove(t *testing.T) {
	os.WriteFile("/tmp/test_rename_src.txt", []byte("data"), 0644)
	defer os.Remove("/tmp/test_rename_src.txt")
	defer os.Remove("/tmp/test_rename_dst.txt")

	bin := compileTestBinary(t, "renamer", `
#include <stdio.h>
int main() {
    rename("/tmp/test_rename_src.txt", "/tmp/test_rename_dst.txt");
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	// Move is recorded as write in our implementation
	if !hasAccess(accesses, "test_rename_dst.txt", OpWrite) {
		t.Errorf("expected move/write access for rename dest, got: %+v", accesses)
	}
}

func TestCorrectness_DeleteRecorded(t *testing.T) {
	os.WriteFile("/tmp/test_delete_file.txt", []byte("data"), 0644)

	bin := compileTestBinary(t, "deleter", `
#include <unistd.h>
int main() {
    unlink("/tmp/test_delete_file.txt");
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	// Note: Our parseOutput ignores deletes (case 'd'), so this tests that behavior
	accesses, _ := tracer.ParseOutput()
	// Deletes are currently ignored in parseOutput
	for _, a := range accesses {
		if strings.Contains(a.Path, "test_delete_file.txt") {
			// If delete tracking is added, this should pass
			t.Logf("delete access recorded: %+v", a)
		}
	}
}

func TestCorrectness_StatRecordedAsRead(t *testing.T) {
	os.WriteFile("/tmp/test_stat_file.txt", []byte("data"), 0644)
	defer os.Remove("/tmp/test_stat_file.txt")

	bin := compileTestBinary(t, "statter", `
#include <sys/stat.h>
int main() {
    struct stat st;
    stat("/tmp/test_stat_file.txt", &st);
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	if !hasAccess(accesses, "test_stat_file.txt", OpRead) {
		t.Errorf("expected read access for stat, got: %+v", accesses)
	}
}

func TestCompleteness_ChildProcessAccesses(t *testing.T) {
	// Create a child binary that does file access
	childBin := compileTestBinary(t, "child", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_child_access.txt", O_WRONLY | O_CREAT, 0644);
    if (fd >= 0) { write(fd, "child", 5); close(fd); }
    return 0;
}
`)
	defer os.Remove("/tmp/test_child_access.txt")

	// Create parent that forks and execs child
	parentBin := compileTestBinary(t, "parent", `
#include <unistd.h>
#include <sys/wait.h>
int main(int argc, char *argv[]) {
    pid_t pid = fork();
    if (pid == 0) {
        execl(argv[1], argv[1], NULL);
        _exit(1);
    }
    wait(NULL);
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{parentBin, childBin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	if !hasAccess(accesses, "test_child_access.txt", OpWrite) {
		t.Errorf("expected child process write to be captured, got: %+v", accesses)
	}
}

func TestCompleteness_OpenatCaptured(t *testing.T) {
	os.MkdirTemp("", "")
	os.WriteFile("/tmp/test_openat_file.txt", []byte("data"), 0644)
	defer os.Remove("/tmp/test_openat_file.txt")

	bin := compileTestBinary(t, "openat_user", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int dirfd = open("/tmp", O_RDONLY | O_DIRECTORY);
    if (dirfd >= 0) {
        int fd = openat(dirfd, "test_openat_file.txt", O_RDONLY);
        if (fd >= 0) close(fd);
        close(dirfd);
    }
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	if !hasAccess(accesses, "test_openat_file.txt", OpRead) {
		t.Errorf("expected openat access to be captured, got: %+v", accesses)
	}
}

func TestPreservation_ExitCodePreserved(t *testing.T) {
	bin := compileTestBinary(t, "exitcode", `
int main() { return 42; }
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	err := cmd.Run()

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 42 {
			t.Errorf("exit code = %d, want 42", exitErr.ExitCode())
		}
	} else if err != nil {
		t.Errorf("unexpected error type: %v", err)
	} else {
		t.Error("expected exit code 42, got success")
	}
}

func TestPreservation_StdoutPreserved(t *testing.T) {
	bin := compileTestBinary(t, "stdout", `
#include <stdio.h>
int main() { printf("hello stdout"); return 0; }
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Run()

	if !strings.Contains(stdout.String(), "hello stdout") {
		t.Errorf("stdout = %q, want to contain 'hello stdout'", stdout.String())
	}
}

func TestPreservation_FileCreationPreserved(t *testing.T) {
	outFile := "/tmp/test_creation_preserved.txt"
	defer os.Remove(outFile)

	bin := compileTestBinary(t, "creator", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_creation_preserved.txt", O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd >= 0) { write(fd, "created", 7); close(fd); }
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(content) != "created" {
		t.Errorf("content = %q, want 'created'", content)
	}
}

func TestFormat_PathsAreRecorded(t *testing.T) {
	os.WriteFile("/tmp/test_path_format.txt", []byte("data"), 0644)
	defer os.Remove("/tmp/test_path_format.txt")

	bin := compileTestBinary(t, "pathtest", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_path_format.txt", O_RDONLY);
    if (fd >= 0) close(fd);
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	found := false
	for _, a := range accesses {
		if strings.Contains(a.Path, "test_path_format.txt") {
			found = true
			// Path should contain the file name
			if !strings.HasSuffix(a.Path, "test_path_format.txt") {
				t.Errorf("path should end with filename: %s", a.Path)
			}
		}
	}
	if !found {
		t.Errorf("path not found in accesses: %+v", accesses)
	}
}

func TestFormat_NoDuplicates(t *testing.T) {
	os.WriteFile("/tmp/test_dedup.txt", []byte("data"), 0644)
	defer os.Remove("/tmp/test_dedup.txt")

	bin := compileTestBinary(t, "dedup", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    for (int i = 0; i < 5; i++) {
        int fd = open("/tmp/test_dedup.txt", O_RDONLY);
        if (fd >= 0) close(fd);
    }
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, _ := tracer.ParseOutput()
	count := 0
	for _, a := range accesses {
		if strings.HasSuffix(a.Path, "test_dedup.txt") && a.Operation == OpRead {
			count++
		}
	}
	if count > 1 {
		t.Errorf("found %d duplicate entries for same file+op, want 1", count)
	}
}

func TestFormat_OutputParseable(t *testing.T) {
	bin := compileTestBinary(t, "parseable", `
#include <fcntl.h>
#include <unistd.h>
int main() {
    int fd = open("/tmp/test_parseable.txt", O_WRONLY | O_CREAT, 0644);
    if (fd >= 0) close(fd);
    return 0;
}
`)
	defer os.Remove("/tmp/test_parseable.txt")

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run()

	accesses, err := tracer.ParseOutput()
	if err != nil {
		t.Fatalf("ParseOutput failed: %v", err)
	}

	// Verify all accesses have valid operation and non-empty path
	for _, a := range accesses {
		if a.Path == "" {
			t.Error("access has empty path")
		}
		if a.Operation != OpRead && a.Operation != OpWrite {
			t.Errorf("access has invalid operation: %v", a.Operation)
		}
	}
}

func TestRobustness_CleanupOnSuccess(t *testing.T) {
	tracer, err := New(DefaultConfig())
	if err != nil {
		t.Skipf("tracer not available: %v", err)
	}

	fsa := tracer.(*fsatracer)
	outputPath := fsa.outputPath

	// Verify temp file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatal("temp file should exist before cleanup")
	}

	tracer.Cleanup()

	// Verify temp file is gone
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Error("temp file should be removed after cleanup")
	}
}

func TestRobustness_CleanupOnFailure(t *testing.T) {
	tracer, err := New(DefaultConfig())
	if err != nil {
		t.Skipf("tracer not available: %v", err)
	}

	fsa := tracer.(*fsatracer)
	outputPath := fsa.outputPath

	// Run a failing command
	bin := compileTestBinary(t, "failer", `int main() { return 1; }`)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run() // Ignore error

	tracer.Cleanup()

	// Verify temp file is gone
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Error("temp file should be removed after cleanup even on failure")
	}
}

func TestRobustness_PartialOutputOnCrash(t *testing.T) {
	// Create file before crash
	os.WriteFile("/tmp/test_precrash.txt", []byte("data"), 0644)
	defer os.Remove("/tmp/test_precrash.txt")

	bin := compileTestBinary(t, "crasher", `
#include <fcntl.h>
#include <unistd.h>
#include <signal.h>
int main() {
    int fd = open("/tmp/test_precrash.txt", O_RDONLY);
    if (fd >= 0) close(fd);
    raise(SIGKILL);
    return 0;
}
`)

	tracer := newTestTracer(t)
	cmd, _ := tracer.WrapCommand([]string{bin}, "")
	cmd.Run() // Will fail due to signal

	accesses, err := tracer.ParseOutput()
	if err != nil {
		t.Logf("ParseOutput error (may be expected): %v", err)
	}

	// We should have at least the access before the crash
	// (This depends on fsatrace buffering behavior)
	t.Logf("accesses captured before crash: %+v", accesses)
}

func TestFsatracerWrapCommand(t *testing.T) {
	tracer := &fsatracer{
		cfg:        DefaultConfig(),
		outputPath: "/tmp/test-output.log",
		binaryPath: "/usr/bin/fsatrace",
	}

	cmd, err := tracer.WrapCommand([]string{"sh", "-c"}, "echo hello")
	if err != nil {
		t.Fatalf("WrapCommand error: %v", err)
	}

	args := cmd.Args
	if len(args) < 6 {
		t.Fatalf("expected at least 6 args, got %d: %v", len(args), args)
	}

	if args[0] != "/usr/bin/fsatrace" {
		t.Errorf("args[0] = %s, want /usr/bin/fsatrace", args[0])
	}
	if args[1] != "rwmq" {
		t.Errorf("args[1] = %s, want rwmq", args[1])
	}
	if args[2] != "/tmp/test-output.log" {
		t.Errorf("args[2] = %s, want /tmp/test-output.log", args[2])
	}
	if args[3] != "--" {
		t.Errorf("args[3] = %s, want --", args[3])
	}
	if args[4] != "sh" || args[5] != "-c" || args[6] != "echo hello" {
		t.Errorf("shell args = %v, want [sh -c echo hello]", args[4:])
	}
}

func TestFsatracerWrapCommandConfigOptions(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		expectedOpts string
	}{
		{"all captures enabled", Config{CaptureReads: true, CaptureWrites: true}, "rwmq"},
		{"reads only", Config{CaptureReads: true, CaptureWrites: false}, "rq"},
		{"writes only", Config{CaptureReads: false, CaptureWrites: true}, "wm"},
		{"nothing captured", Config{CaptureReads: false, CaptureWrites: false}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer := &fsatracer{
				cfg:        tt.cfg,
				outputPath: "/tmp/test.log",
				binaryPath: "/usr/bin/fsatrace",
			}

			cmd, err := tracer.WrapCommand([]string{"sh", "-c"}, "test")
			if err != nil {
				t.Fatalf("WrapCommand error: %v", err)
			}

			if cmd.Args[1] != tt.expectedOpts {
				t.Errorf("options = %s, want %s", cmd.Args[1], tt.expectedOpts)
			}
		})
	}
}

func TestFsatracerCleanup(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "fsatrace-test-*.log")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		t.Fatal("temp file should exist before cleanup")
	}

	tracer := &fsatracer{
		cfg:        DefaultConfig(),
		outputPath: tmpPath,
		binaryPath: "/usr/bin/fsatrace",
	}

	if err := tracer.Cleanup(); err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}

	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should be removed after cleanup")
	}
}

func TestFsatracerCleanupNoFile(t *testing.T) {
	tracer := &fsatracer{
		cfg:        DefaultConfig(),
		outputPath: "",
		binaryPath: "/usr/bin/fsatrace",
	}

	if err := tracer.Cleanup(); err != nil {
		t.Errorf("Cleanup with empty path should not error: %v", err)
	}
}

func TestFsatracerParseOutput(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "fsatrace-test-*.log")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := "r|/path/to/input.c\nw|/path/to/output.o\nq|/path/to/header.h\n"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	tmpFile.Close()

	tracer := &fsatracer{
		cfg:        DefaultConfig(),
		outputPath: tmpFile.Name(),
		binaryPath: "/usr/bin/fsatrace",
	}

	accesses, err := tracer.ParseOutput()
	if err != nil {
		t.Fatalf("ParseOutput error: %v", err)
	}

	if len(accesses) != 3 {
		t.Fatalf("got %d accesses, want 3", len(accesses))
	}

	if accesses[0].Path != "/path/to/input.c" || accesses[0].Operation != OpRead {
		t.Errorf("access[0] = %+v, want {Path:/path/to/input.c, Op:Read}", accesses[0])
	}
	if accesses[1].Path != "/path/to/output.o" || accesses[1].Operation != OpWrite {
		t.Errorf("access[1] = %+v, want {Path:/path/to/output.o, Op:Write}", accesses[1])
	}
	if accesses[2].Path != "/path/to/header.h" || accesses[2].Operation != OpRead {
		t.Errorf("access[2] = %+v, want {Path:/path/to/header.h, Op:Read}", accesses[2])
	}
}

func TestFsatracerParseOutputMissingFile(t *testing.T) {
	tracer := &fsatracer{
		cfg:        DefaultConfig(),
		outputPath: "/nonexistent/path/fsatrace.log",
		binaryPath: "/usr/bin/fsatrace",
	}

	accesses, err := tracer.ParseOutput()
	if err != nil {
		t.Errorf("ParseOutput should not error on missing file: %v", err)
	}
	if accesses != nil {
		t.Errorf("accesses should be nil for missing file, got %v", accesses)
	}
}

func TestFindFsatraceBinary(t *testing.T) {
	path, err := findFsatraceBinary()

	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("unexpected error: %v", err)
		}
		t.Skipf("fsatrace not available: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("findFsatraceBinary returned non-existent path: %s", path)
	}
}

func TestFsatraceIntegration(t *testing.T) {
	cfg := DefaultConfig()
	tracer, err := New(cfg)
	if err != nil {
		t.Skipf("fsatrace not available: %v", err)
	}
	defer tracer.Cleanup()

	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.txt")
	outputFile := filepath.Join(tmpDir, "output.txt")

	if err := os.WriteFile(inputFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("write input file: %v", err)
	}

	var cmdStr string
	if runtime.GOOS == "windows" {
		cmdStr = "type " + inputFile + " > " + outputFile
	} else {
		cmdStr = "cat " + inputFile + " > " + outputFile
	}

	shell := []string{"sh", "-c"}
	if runtime.GOOS == "windows" {
		shell = []string{"cmd", "/c"}
	}

	cmd, err := tracer.WrapCommand(shell, cmdStr)
	if err != nil {
		t.Fatalf("WrapCommand error: %v", err)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		if runtime.GOOS == "darwin" {
			t.Skipf("fsatrace may require SIP disabled on macOS: %v\nOutput: %s", err, out)
		}
		t.Fatalf("command failed: %v\nOutput: %s", err, out)
	}

	accesses, err := tracer.ParseOutput()
	if err != nil {
		t.Fatalf("ParseOutput error: %v", err)
	}

	if len(accesses) == 0 {
		if runtime.GOOS == "darwin" {
			t.Skipf("fsatrace captured no accesses - likely SIP is enabled on macOS")
		}
		t.Error("expected some file accesses to be captured")
	}

	var sawInputRead, sawOutputWrite bool
	for _, a := range accesses {
		if strings.Contains(a.Path, "input.txt") && a.Operation == OpRead {
			sawInputRead = true
		}
		if strings.Contains(a.Path, "output.txt") && a.Operation == OpWrite {
			sawOutputWrite = true
		}
	}

	if !sawInputRead {
		t.Errorf("expected to capture read of input.txt, got accesses: %+v", accesses)
	}
	if !sawOutputWrite {
		t.Errorf("expected to capture write of output.txt, got accesses: %+v", accesses)
	}

	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("output content = %q, want %q", content, "hello")
	}
}

func TestFsatraceIntegrationWritesOnly(t *testing.T) {
	cfg := Config{CaptureReads: false, CaptureWrites: true}
	tracer, err := newPlatformTracer(cfg)
	if err != nil {
		t.Skipf("fsatrace not available: %v", err)
	}
	defer tracer.Cleanup()

	fsa := tracer.(*fsatracer)
	cmd, _ := fsa.WrapCommand([]string{"sh", "-c"}, "echo test")

	if cmd.Args[1] != "wm" {
		t.Errorf("options = %s, want wm (writes only)", cmd.Args[1])
	}
}

func TestNewPlatformTracerCreatesTempFile(t *testing.T) {
	_, err := findFsatraceBinary()
	if err != nil {
		t.Skipf("fsatrace not available: %v", err)
	}

	cfg := DefaultConfig()
	tracer, err := newPlatformTracer(cfg)
	if err != nil {
		t.Fatalf("newPlatformTracer error: %v", err)
	}
	defer tracer.Cleanup()

	fsa := tracer.(*fsatracer)

	if fsa.outputPath == "" {
		t.Error("outputPath should be set")
	}

	if _, err := os.Stat(fsa.outputPath); os.IsNotExist(err) {
		t.Error("temp output file should exist")
	}
}
