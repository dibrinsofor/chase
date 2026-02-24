package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildChase(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "chase")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build chase: %v\n%s", err, out)
	}
	return binary
}

func writeChasefile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "Chasefile"), []byte(content), 0644); err != nil {
		t.Fatalf("write Chasefile: %v", err)
	}
}

func TestCLIListDashes(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
build:
    summary: "build the project"
    cmds: "echo building"
test:
    summary: "run tests"
    cmds: "echo testing"
`)

	cmd := exec.Command(binary, "-l")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chase -l failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "build") {
		t.Errorf("expected 'build' in output, got: %s", output)
	}
	if !strings.Contains(output, "test") {
		t.Errorf("expected 'test' in output, got: %s", output)
	}
	if !strings.Contains(output, "build the project") {
		t.Errorf("expected summary in output, got: %s", output)
	}
}

func TestCLIRunSpecificDash(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
build:
    cmds: "echo built > `+marker+`"
other:
    cmds: "echo other"
`)

	cmd := exec.Command(binary, "-r", "build")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chase -r build failed: %v\n%s", err, out)
	}

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("expected build task to run and create marker file")
	}
}

func TestCLIRunAllDashes(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()
	marker1 := filepath.Join(dir, "task1.txt")
	marker2 := filepath.Join(dir, "task2.txt")

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
task1:
    cmds: "echo 1 > `+marker1+`"
task2:
    cmds: "echo 2 > `+marker2+`"
`)

	cmd := exec.Command(binary)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chase failed: %v\n%s", err, out)
	}

	if _, err := os.Stat(marker1); os.IsNotExist(err) {
		t.Error("expected task1 to run")
	}
	if _, err := os.Stat(marker2); os.IsNotExist(err) {
		t.Error("expected task2 to run")
	}
}

func TestCLIMissingChasefile(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()

	cmd := exec.Command(binary, "-l")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("expected error when Chasefile missing")
	}
	if !strings.Contains(string(out), "chasefile") {
		t.Errorf("expected error about chasefile, got: %s", out)
	}
}

func TestCLIInvalidChasefile(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()

	writeChasefile(t, dir, `this is not valid syntax {{{{`)

	cmd := exec.Command(binary, "-l")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("expected error for invalid Chasefile")
	}
	output := string(out)
	if !strings.Contains(output, "error") && !strings.Contains(output, "parse") {
		t.Errorf("expected parse error, got: %s", output)
	}
}

func TestCLIRunWithDependencies(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()
	logFile := filepath.Join(dir, "log.txt")

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
dep:
    cmds: "echo dep >> `+logFile+`"
main:
    uses: dep
    cmds: "echo main >> `+logFile+`"
`)

	cmd := exec.Command(binary, "-r", "main")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chase -r main failed: %v\n%s", err, out)
	}

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	log := string(content)
	if !strings.Contains(log, "dep") {
		t.Errorf("expected dependency to run, got: %s", log)
	}
	if !strings.Contains(log, "main") {
		t.Errorf("expected main to run, got: %s", log)
	}

	depIdx := strings.Index(log, "dep")
	mainIdx := strings.Index(log, "main")
	if depIdx > mainIdx {
		t.Errorf("dependency should run before main, got: %s", log)
	}
}

func TestCLIRunNonexistentDash(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
build:
    cmds: "echo ok"
`)

	cmd := exec.Command(binary, "-r", "nonexistent")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if !strings.Contains(string(out), "not found") {
		t.Errorf("expected 'not found' error, got: %s", out)
	}
}

func TestCLIParallelWorkers(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
task1:
    cmds: "echo 1"
task2:
    cmds: "echo 2"
task3:
    cmds: "echo 3"
task4:
    cmds: "echo 4"
`)

	cmd := exec.Command(binary, "-j", "4")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chase -j 4 failed: %v\n%s", err, out)
	}
}

func TestCLILintNoCache(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
build:
    cmds: "echo ok"
`)

	cmd := exec.Command(binary, "-lint")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	// Should not error, just report no traced data
	if err != nil {
		t.Fatalf("chase -lint failed: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "no traced data") && !strings.Contains(output, "No state cache") {
		t.Errorf("expected lint message about missing trace data, got: %s", output)
	}
}

func TestCLIEnvironmentVariables(t *testing.T) {
	binary := buildChase(t)
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.txt")

	writeChasefile(t, dir, `
set shell = ["sh", "-c"]
set myvar = "hello_chase"
build:
    cmds: "echo $myvar > `+outFile+`"
`)

	cmd := exec.Command(binary, "-r", "build")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chase failed: %v\n%s", err, out)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	if !strings.Contains(string(content), "hello_chase") {
		t.Errorf("expected env var substitution, got: %s", content)
	}
}
