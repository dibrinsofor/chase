package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewBuildState(t *testing.T) {
	bs := NewBuildState()
	if bs == nil {
		t.Fatal("NewBuildState returned nil")
	}
	if bs.Targets == nil {
		t.Error("Targets map should be initialized")
	}
}

func TestLoadNonExistent(t *testing.T) {
	bs, err := Load("/nonexistent/path/state.json")
	if err != nil {
		t.Errorf("Load should not error on missing file: %v", err)
	}
	if bs == nil {
		t.Fatal("Load should return empty BuildState")
	}
	if len(bs.Targets) != 0 {
		t.Error("Targets should be empty")
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, ".chase", "state.json")

	bs := NewBuildState()
	bs.path = path
	bs.RecordBuild("test", []string{}, []string{})

	if err := bs.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.GetTarget("test") == nil {
		t.Error("target 'test' should exist after load")
	}
}

func TestNeedsBuild(t *testing.T) {
	bs := NewBuildState()

	needs, reason := bs.NeedsBuild("nonexistent")
	if !needs {
		t.Error("should need build for nonexistent target")
	}
	if reason != "no previous build" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestNeedsBuildWithInputs(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.txt")

	if err := os.WriteFile(inputFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	bs := NewBuildState()
	bs.RecordBuild("test", []string{inputFile}, []string{})

	needs, _ := bs.NeedsBuild("test")
	if needs {
		t.Error("should not need build when inputs unchanged")
	}

	if err := os.WriteFile(inputFile, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}

	needs, reason := bs.NeedsBuild("test")
	if !needs {
		t.Error("should need build when input changed")
	}
	if reason != "input changed: "+inputFile {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestNeedsBuildMissingOutput(t *testing.T) {
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "output.txt")

	if err := os.WriteFile(outputFile, []byte("output"), 0644); err != nil {
		t.Fatal(err)
	}

	bs := NewBuildState()
	bs.RecordBuild("test", []string{}, []string{outputFile})

	os.Remove(outputFile)

	needs, reason := bs.NeedsBuild("test")
	if !needs {
		t.Error("should need build when output missing")
	}
	if reason != "output missing: "+outputFile {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestHashFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	hash1, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile failed: %v", err)
	}

	hash2, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile failed: %v", err)
	}

	if hash1 != hash2 {
		t.Error("same file should produce same hash")
	}

	if err := os.WriteFile(path, []byte("different"), 0644); err != nil {
		t.Fatal(err)
	}

	hash3, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile failed: %v", err)
	}

	if hash1 == hash3 {
		t.Error("different content should produce different hash")
	}
}

func TestHashFileNotFound(t *testing.T) {
	_, err := HashFile("/nonexistent/file.txt")
	if err == nil {
		t.Error("HashFile should error on missing file")
	}
}

func TestRecordBuild(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.txt")
	outputFile := filepath.Join(tmpDir, "output.txt")

	os.WriteFile(inputFile, []byte("input"), 0644)
	os.WriteFile(outputFile, []byte("output"), 0644)

	bs := NewBuildState()
	bs.RecordBuild("test", []string{inputFile}, []string{outputFile})

	ts := bs.GetTarget("test")
	if ts == nil {
		t.Fatal("target should exist")
	}

	if len(ts.TracedInputs) != 1 {
		t.Errorf("expected 1 traced input, got %d", len(ts.TracedInputs))
	}
	if len(ts.TracedOutputs) != 1 {
		t.Errorf("expected 1 traced output, got %d", len(ts.TracedOutputs))
	}
	if len(ts.InputHashes) != 1 {
		t.Errorf("expected 1 input hash, got %d", len(ts.InputHashes))
	}
	if ts.LastRun.IsZero() {
		t.Error("LastRun should be set")
	}
}
