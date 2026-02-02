package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const StateFile = ".chase/state.json"

type TargetState struct {
	InputHashes  map[string]string `json:"inputHashes"`
	OutputHashes map[string]string `json:"outputHashes"`
	TracedInputs []string          `json:"tracedInputs"`
	TracedOutputs []string         `json:"tracedOutputs"`
	LastRun      time.Time         `json:"lastRun"`
}

type BuildState struct {
	Targets map[string]*TargetState `json:"targets"`
	path    string
}

func NewBuildState() *BuildState {
	return &BuildState{
		Targets: make(map[string]*TargetState),
		path:    StateFile,
	}
}

func Load(path string) (*BuildState, error) {
	if path == "" {
		path = StateFile
	}

	bs := &BuildState{
		Targets: make(map[string]*TargetState),
		path:    path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return bs, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, bs); err != nil {
		return nil, err
	}

	bs.path = path
	return bs, nil
}

func (bs *BuildState) Save() error {
	dir := filepath.Dir(bs.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(bs, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(bs.path, data, 0644)
}

func (bs *BuildState) GetTarget(name string) *TargetState {
	return bs.Targets[name]
}

func (bs *BuildState) SetTarget(name string, ts *TargetState) {
	bs.Targets[name] = ts
}

func (bs *BuildState) NeedsBuild(target string) (bool, string) {
	ts := bs.Targets[target]
	if ts == nil {
		return true, "no previous build"
	}

	if len(ts.TracedInputs) == 0 && len(ts.TracedOutputs) == 0 {
		return true, "no traced files"
	}

	for _, path := range ts.TracedInputs {
		oldHash, ok := ts.InputHashes[path]
		if !ok {
			return true, "new input: " + path
		}

		currentHash, err := HashFile(path)
		if err != nil {
			return true, "input missing: " + path
		}

		if currentHash != oldHash {
			return true, "input changed: " + path
		}
	}

	for _, path := range ts.TracedOutputs {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return true, "output missing: " + path
		}
	}

	return false, ""
}

func (bs *BuildState) RecordBuild(target string, inputs, outputs []string) error {
	ts := &TargetState{
		InputHashes:   make(map[string]string),
		OutputHashes:  make(map[string]string),
		TracedInputs:  inputs,
		TracedOutputs: outputs,
		LastRun:       time.Now(),
	}

	for _, path := range inputs {
		hash, err := HashFile(path)
		if err != nil {
			continue
		}
		ts.InputHashes[path] = hash
	}

	for _, path := range outputs {
		hash, err := HashFile(path)
		if err != nil {
			continue
		}
		ts.OutputHashes[path] = hash
	}

	bs.Targets[target] = ts
	return nil
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
