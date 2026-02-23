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
	InputHashes   map[string]string `json:"inputHashes"`
	OutputHashes  map[string]string `json:"outputHashes"`
	TracedInputs  []string          `json:"tracedInputs"`
	TracedOutputs []string          `json:"tracedOutputs"`
	LastRun       time.Time         `json:"lastRun"`
}

type CommandState struct {
	Command      string            `json:"command"`
	InputHashes  map[string]string `json:"inputHashes"`
	OutputHashes map[string]string `json:"outputHashes"`
	Inputs       []string          `json:"inputs"`
	Outputs      []string          `json:"outputs"`
	LastRun      time.Time         `json:"lastRun"`
}

// GraphState is the serializable representation of a traced command graph.
// It stores the structure needed to reconstruct an ir.CommandGraph from cache.
type GraphState struct {
	CommandIDs []string            `json:"commandIds"` // ordered command IDs
	Producers  map[string]string   `json:"producers"`  // output file -> producing command ID
	Consumers  map[string][]string `json:"consumers"`  // input file -> consuming command IDs
}

type BuildState struct {
	Targets  map[string]*TargetState  `json:"targets"`
	Commands map[string]*CommandState `json:"commands"`
	Graphs   map[string]*GraphState   `json:"graphs,omitempty"`
	path     string
}

func NewBuildState() *BuildState {
	return &BuildState{
		Targets:  make(map[string]*TargetState),
		Commands: make(map[string]*CommandState),
		Graphs:   make(map[string]*GraphState),
		path:     StateFile,
	}
}

func Load(path string) (*BuildState, error) {
	if path == "" {
		path = StateFile
	}

	bs := &BuildState{
		Targets:  make(map[string]*TargetState),
		Commands: make(map[string]*CommandState),
		Graphs:   make(map[string]*GraphState),
		path:     path,
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

	if bs.Commands == nil {
		bs.Commands = make(map[string]*CommandState)
	}
	if bs.Graphs == nil {
		bs.Graphs = make(map[string]*GraphState)
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

// NeedsCommandBuild checks whether a specific traced subprocess needs rebuilding.
// It returns true if the command has never been traced, or if any of its inputs changed.
func (bs *BuildState) NeedsCommandBuild(cmdID string) (bool, string) {
	cs := bs.Commands[cmdID]
	if cs == nil {
		return true, "never traced"
	}

	for _, path := range cs.Inputs {
		oldHash, ok := cs.InputHashes[path]
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

	for _, path := range cs.Outputs {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return true, "output missing: " + path
		}
	}

	return false, ""
}

// RecordCommandState stores the build state for a single traced subprocess.
func (bs *BuildState) RecordCommandState(cmdID string, cs *CommandState) {
	bs.Commands[cmdID] = cs
}

// RecordGraphState stores the traced command graph for a task.
func (bs *BuildState) RecordGraphState(taskID string, gs *GraphState) {
	bs.Graphs[taskID] = gs
}

// GetGraphState retrieves the stored command graph for a task.
func (bs *BuildState) GetGraphState(taskID string) *GraphState {
	return bs.Graphs[taskID]
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
