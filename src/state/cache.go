package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	CacheDir    = ".chase"
	TargetsDir  = ".chase/targets"
	CommandsDir = ".chase/commands"
	// Legacy single-file path for migration
	LegacyStateFile = ".chase/state.json"
)

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
type GraphState struct {
	CommandIDs []string            `json:"commandIds"`
	Producers  map[string]string   `json:"producers"`
	Consumers  map[string][]string `json:"consumers"`
}

// BuildState provides lazy-loaded access to build cache.
// Data is loaded from disk on first access and cached in memory.
type BuildState struct {
	baseDir string

	// In-memory caches (loaded lazily)
	targets  map[string]*TargetState
	commands map[string]*CommandState
	graphs   map[string]*GraphState

	// Track what's been modified (needs saving)
	dirtyTargets  map[string]bool
	dirtyCommands map[string]bool
	dirtyGraphs   map[string]bool

	mu sync.RWMutex
}

func NewBuildState() *BuildState {
	return &BuildState{
		baseDir:       CacheDir,
		targets:       make(map[string]*TargetState),
		commands:      make(map[string]*CommandState),
		graphs:        make(map[string]*GraphState),
		dirtyTargets:  make(map[string]bool),
		dirtyCommands: make(map[string]bool),
		dirtyGraphs:   make(map[string]bool),
	}
}

// Load initializes the cache, migrating from legacy format if needed.
// It does NOT load all data - that happens lazily on first access.
func Load(path string) (*BuildState, error) {
	if path == "" {
		path = CacheDir
	}

	bs := &BuildState{
		baseDir:       path,
		targets:       make(map[string]*TargetState),
		commands:      make(map[string]*CommandState),
		graphs:        make(map[string]*GraphState),
		dirtyTargets:  make(map[string]bool),
		dirtyCommands: make(map[string]bool),
		dirtyGraphs:   make(map[string]bool),
	}

	// Check for legacy single-file format and migrate
	legacyPath := filepath.Join(filepath.Dir(path), LegacyStateFile)
	if path == CacheDir {
		legacyPath = LegacyStateFile
	}

	if _, err := os.Stat(legacyPath); err == nil {
		if err := bs.migrateFromLegacy(legacyPath); err != nil {
			// Migration failed, but we can continue with empty state
			return bs, nil
		}
	}

	return bs, nil
}

// migrateFromLegacy converts old single-file format to new per-target format
func (bs *BuildState) migrateFromLegacy(legacyPath string) error {
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return err
	}

	var legacy struct {
		Targets  map[string]*TargetState  `json:"targets"`
		Commands map[string]*CommandState `json:"commands"`
		Graphs   map[string]*GraphState   `json:"graphs"`
	}

	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}

	// Create directories
	os.MkdirAll(filepath.Join(bs.baseDir, "targets"), 0755)
	os.MkdirAll(filepath.Join(bs.baseDir, "commands"), 0755)

	// Write each target to its own file
	for name, ts := range legacy.Targets {
		bs.targets[name] = ts
		bs.dirtyTargets[name] = true
	}

	// Write each command to its own file
	for id, cs := range legacy.Commands {
		bs.commands[id] = cs
		bs.dirtyCommands[id] = true
	}

	// Write graphs
	for id, gs := range legacy.Graphs {
		bs.graphs[id] = gs
		bs.dirtyGraphs[id] = true
	}

	// Save all migrated data
	if err := bs.Save(); err != nil {
		return err
	}

	// Remove legacy file after successful migration
	os.Remove(legacyPath)

	return nil
}

// Save writes all dirty entries to disk
func (bs *BuildState) Save() error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Ensure directories exist
	if err := os.MkdirAll(filepath.Join(bs.baseDir, "targets"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(bs.baseDir, "commands"), 0755); err != nil {
		return err
	}

	// Save dirty targets
	for name := range bs.dirtyTargets {
		if ts := bs.targets[name]; ts != nil {
			if err := bs.saveTarget(name, ts); err != nil {
				return err
			}
		}
	}
	bs.dirtyTargets = make(map[string]bool)

	// Save dirty commands
	for id := range bs.dirtyCommands {
		if cs := bs.commands[id]; cs != nil {
			if err := bs.saveCommand(id, cs); err != nil {
				return err
			}
		}
	}
	bs.dirtyCommands = make(map[string]bool)

	// Save dirty graphs (stored in targets dir for simplicity)
	for id := range bs.dirtyGraphs {
		if gs := bs.graphs[id]; gs != nil {
			if err := bs.saveGraph(id, gs); err != nil {
				return err
			}
		}
	}
	bs.dirtyGraphs = make(map[string]bool)

	return nil
}

func (bs *BuildState) saveTarget(name string, ts *TargetState) error {
	path := filepath.Join(bs.baseDir, "targets", sanitizeFilename(name)+".json")
	data, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (bs *BuildState) saveCommand(id string, cs *CommandState) error {
	path := filepath.Join(bs.baseDir, "commands", sanitizeFilename(id)+".json")
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (bs *BuildState) saveGraph(id string, gs *GraphState) error {
	path := filepath.Join(bs.baseDir, "targets", sanitizeFilename(id)+".graph.json")
	data, err := json.MarshalIndent(gs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// sanitizeFilename makes a string safe for use as a filename
func sanitizeFilename(s string) string {
	// Replace colons (used in command IDs like "build:0") with underscores
	return strings.ReplaceAll(s, ":", "_")
}

// loadTarget loads a target from disk (lazy loading)
func (bs *BuildState) loadTarget(name string) *TargetState {
	path := filepath.Join(bs.baseDir, "targets", sanitizeFilename(name)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var ts TargetState
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil
	}
	return &ts
}

// loadCommand loads a command from disk (lazy loading)
func (bs *BuildState) loadCommand(id string) *CommandState {
	path := filepath.Join(bs.baseDir, "commands", sanitizeFilename(id)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cs CommandState
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil
	}
	return &cs
}

// loadGraph loads a graph from disk (lazy loading)
func (bs *BuildState) loadGraph(id string) *GraphState {
	path := filepath.Join(bs.baseDir, "targets", sanitizeFilename(id)+".graph.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var gs GraphState
	if err := json.Unmarshal(data, &gs); err != nil {
		return nil
	}
	return &gs
}

// GetTarget returns target state, loading from disk if needed
func (bs *BuildState) GetTarget(name string) *TargetState {
	bs.mu.RLock()
	ts, loaded := bs.targets[name]
	bs.mu.RUnlock()

	if loaded {
		return ts
	}

	// Not in memory, try loading from disk
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Double-check after acquiring write lock
	if ts, ok := bs.targets[name]; ok {
		return ts
	}

	ts = bs.loadTarget(name)
	bs.targets[name] = ts // Cache it (even if nil)
	return ts
}

func (bs *BuildState) SetTarget(name string, ts *TargetState) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.targets[name] = ts
	bs.dirtyTargets[name] = true
}

func (bs *BuildState) NeedsBuild(target string) (bool, string) {
	ts := bs.GetTarget(target)
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

	bs.SetTarget(target, ts)
	return nil
}

// GetCommandState returns command state, loading from disk if needed
func (bs *BuildState) GetCommandState(cmdID string) *CommandState {
	bs.mu.RLock()
	cs, loaded := bs.commands[cmdID]
	bs.mu.RUnlock()

	if loaded {
		return cs
	}

	bs.mu.Lock()
	defer bs.mu.Unlock()

	if cs, ok := bs.commands[cmdID]; ok {
		return cs
	}

	cs = bs.loadCommand(cmdID)
	bs.commands[cmdID] = cs
	return cs
}

func (bs *BuildState) NeedsCommandBuild(cmdID string) (bool, string) {
	cs := bs.GetCommandState(cmdID)
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

func (bs *BuildState) RecordCommandState(cmdID string, cs *CommandState) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	cs.LastRun = time.Now()
	bs.commands[cmdID] = cs
	bs.dirtyCommands[cmdID] = true
}

func (bs *BuildState) RecordGraphState(taskID string, gs *GraphState) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.graphs[taskID] = gs
	bs.dirtyGraphs[taskID] = true
}

func (bs *BuildState) GetGraphState(taskID string) *GraphState {
	bs.mu.RLock()
	gs, loaded := bs.graphs[taskID]
	bs.mu.RUnlock()

	if loaded {
		return gs
	}

	bs.mu.Lock()
	defer bs.mu.Unlock()

	if gs, ok := bs.graphs[taskID]; ok {
		return gs
	}

	gs = bs.loadGraph(taskID)
	bs.graphs[taskID] = gs
	return gs
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
