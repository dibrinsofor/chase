//go:build !experimental_ebpf

package tracer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFsatracerWrapCommand(t *testing.T) {
	// Create a mock tracer with known paths
	tracer := &fsatracer{
		cfg:        DefaultConfig(),
		outputPath: "/tmp/test-output.log",
		binaryPath: "/usr/bin/fsatrace",
	}

	cmd, err := tracer.WrapCommand([]string{"sh", "-c"}, "echo hello")
	if err != nil {
		t.Fatalf("WrapCommand error: %v", err)
	}

	// Check the command structure
	args := cmd.Args
	if len(args) < 6 {
		t.Fatalf("expected at least 6 args, got %d: %v", len(args), args)
	}

	// args[0] = fsatrace binary path
	if args[0] != "/usr/bin/fsatrace" {
		t.Errorf("args[0] = %s, want /usr/bin/fsatrace", args[0])
	}

	// args[1] = options (rwmq)
	if args[1] != "rwmq" {
		t.Errorf("args[1] = %s, want rwmq", args[1])
	}

	// args[2] = output path
	if args[2] != "/tmp/test-output.log" {
		t.Errorf("args[2] = %s, want /tmp/test-output.log", args[2])
	}

	// args[3] = "--"
	if args[3] != "--" {
		t.Errorf("args[3] = %s, want --", args[3])
	}

	// args[4:] = shell command
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
		{
			name:         "all captures enabled",
			cfg:          Config{CaptureReads: true, CaptureWrites: true},
			expectedOpts: "rwmq",
		},
		{
			name:         "reads only",
			cfg:          Config{CaptureReads: true, CaptureWrites: false},
			expectedOpts: "rq",
		},
		{
			name:         "writes only",
			cfg:          Config{CaptureReads: false, CaptureWrites: true},
			expectedOpts: "wm",
		},
		{
			name:         "nothing captured",
			cfg:          Config{CaptureReads: false, CaptureWrites: false},
			expectedOpts: "",
		},
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
	// Create a temp file
	tmpFile, err := os.CreateTemp("", "fsatrace-test-*.log")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Verify file exists
	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		t.Fatal("temp file should exist before cleanup")
	}

	tracer := &fsatracer{
		cfg:        DefaultConfig(),
		outputPath: tmpPath,
		binaryPath: "/usr/bin/fsatrace",
	}

	// Cleanup
	if err := tracer.Cleanup(); err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}

	// Verify file is gone
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

	// Should not error with empty path
	if err := tracer.Cleanup(); err != nil {
		t.Errorf("Cleanup with empty path should not error: %v", err)
	}
}

func TestFsatracerParseOutput(t *testing.T) {
	// Create a temp file with test content
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

	// Check first access (read)
	if accesses[0].Path != "/path/to/input.c" || accesses[0].Operation != OpRead {
		t.Errorf("access[0] = %+v, want {Path:/path/to/input.c, Op:Read}", accesses[0])
	}

	// Check second access (write)
	if accesses[1].Path != "/path/to/output.o" || accesses[1].Operation != OpWrite {
		t.Errorf("access[1] = %+v, want {Path:/path/to/output.o, Op:Write}", accesses[1])
	}

	// Check third access (stat = read)
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
	// This test checks the search logic
	// It may find fsatrace or not depending on the environment

	path, err := findFsatraceBinary()

	if err != nil {
		// Expected if fsatrace isn't built/installed
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("unexpected error: %v", err)
		}
		t.Skipf("fsatrace not available: %v", err)
	}

	// If found, verify it exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("findFsatraceBinary returned non-existent path: %s", path)
	}
}

func TestFsatraceIntegration(t *testing.T) {
	// Skip if fsatrace binary not available
	cfg := DefaultConfig()
	tracer, err := New(cfg)
	if err != nil {
		t.Skipf("fsatrace not available: %v", err)
	}
	defer tracer.Cleanup()

	// Create a temp directory with test files
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.txt")
	outputFile := filepath.Join(tmpDir, "output.txt")

	if err := os.WriteFile(inputFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("write input file: %v", err)
	}

	// Build a command that reads and writes
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

	// Run the command
	out, err := cmd.CombinedOutput()
	if err != nil {
		// fsatrace might fail due to SIP on macOS or other issues
		if runtime.GOOS == "darwin" {
			t.Skipf("fsatrace may require SIP disabled on macOS: %v\nOutput: %s", err, out)
		}
		t.Fatalf("command failed: %v\nOutput: %s", err, out)
	}

	// Parse the output
	accesses, err := tracer.ParseOutput()
	if err != nil {
		t.Fatalf("ParseOutput error: %v", err)
	}

	// Verify we captured some accesses
	if len(accesses) == 0 {
		t.Error("expected some file accesses to be captured")
	}

	// Check that we saw the input file read
	var sawInputRead bool
	var sawOutputWrite bool
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

	// Verify output file was created
	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("output content = %q, want %q", content, "hello")
	}
}

func TestFsatraceIntegrationWritesOnly(t *testing.T) {
	// Test with CaptureReads disabled
	cfg := Config{CaptureReads: false, CaptureWrites: true}
	tracer, err := newPlatformTracer(cfg)
	if err != nil {
		t.Skipf("fsatrace not available: %v", err)
	}
	defer tracer.Cleanup()

	fsa := tracer.(*fsatracer)

	cmd, _ := fsa.WrapCommand([]string{"sh", "-c"}, "echo test")

	// Options should only have write-related flags
	if cmd.Args[1] != "wm" {
		t.Errorf("options = %s, want wm (writes only)", cmd.Args[1])
	}
}

func TestNewPlatformTracerCreatesTempFile(t *testing.T) {
	// Mock findFsatraceBinary by checking if one exists
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

	// Check that output path is set and file exists
	if fsa.outputPath == "" {
		t.Error("outputPath should be set")
	}

	if _, err := os.Stat(fsa.outputPath); os.IsNotExist(err) {
		t.Error("temp output file should exist")
	}
}
