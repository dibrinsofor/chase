package ir

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dibrinsofor/chase/src/state"
	"github.com/dibrinsofor/chase/src/tracer"
)

// CommandGraph maps file dependencies between traced subprocesses.
// It captures the implicit build graph discovered by tracing a compound command
// like "gcc -o main *.c".
type CommandGraph struct {
	commands  map[CommandID]*Command
	order     []CommandID            // insertion order for deterministic iteration
	byPID     map[int]*Command       // quick lookup during trace processing
	producers map[string]CommandID   // output file -> producing command
	consumers map[string][]CommandID // input file -> consuming commands
}

// NewCommandGraph creates an empty CommandGraph.
func NewCommandGraph() *CommandGraph {
	return &CommandGraph{
		commands:  make(map[CommandID]*Command),
		byPID:     make(map[int]*Command),
		producers: make(map[string]CommandID),
		consumers: make(map[string][]CommandID),
	}
}

// AddCommand inserts a command into the graph and indexes its file relationships.
func (g *CommandGraph) AddCommand(cmd *Command) {
	g.commands[cmd.ID] = cmd
	g.order = append(g.order, cmd.ID)
	g.byPID[cmd.PID] = cmd

	for _, out := range cmd.Outputs {
		g.producers[out] = cmd.ID
	}
	for _, in := range cmd.Inputs {
		g.consumers[in] = append(g.consumers[in], cmd.ID)
	}
}

// Get returns a command by ID.
func (g *CommandGraph) Get(id CommandID) *Command {
	return g.commands[id]
}

// Commands returns all commands in insertion order.
func (g *CommandGraph) Commands() []*Command {
	result := make([]*Command, 0, len(g.order))
	for _, id := range g.order {
		if cmd, ok := g.commands[id]; ok {
			result = append(result, cmd)
		}
	}
	return result
}

// Len returns the number of commands in the graph.
func (g *CommandGraph) Len() int {
	return len(g.commands)
}

// EdgeCount returns the number of producer->consumer edges in the graph.
func (g *CommandGraph) EdgeCount() int {
	count := 0
	for file, consumers := range g.consumers {
		if _, hasProducer := g.producers[file]; hasProducer {
			count += len(consumers)
		}
	}
	return count
}

// Dependents returns the commands that consume the outputs of the given command.
func (g *CommandGraph) Dependents(id CommandID) []*Command {
	cmd := g.commands[id]
	if cmd == nil {
		return nil
	}

	seen := make(map[CommandID]bool)
	var deps []*Command
	for _, out := range cmd.Outputs {
		for _, consumerID := range g.consumers[out] {
			if consumerID != id && !seen[consumerID] {
				seen[consumerID] = true
				if c := g.commands[consumerID]; c != nil {
					deps = append(deps, c)
				}
			}
		}
	}
	return deps
}

// AllFiles returns all input and output files across all commands.
func (g *CommandGraph) AllFiles() (inputs, outputs []string) {
	inSet := make(map[string]bool)
	outSet := make(map[string]bool)
	for _, cmd := range g.commands {
		for _, in := range cmd.Inputs {
			if !inSet[in] {
				inSet[in] = true
				inputs = append(inputs, in)
			}
		}
		for _, out := range cmd.Outputs {
			if !outSet[out] {
				outSet[out] = true
				outputs = append(outputs, out)
			}
		}
	}
	return
}

// GetStale returns the commands that need rebuilding because their inputs changed.
// It follows the dependency chain: if cc1's output changed, the downstream as and ld
// commands are also marked stale.
func (g *CommandGraph) GetStale(cache *state.BuildState) []*Command {
	if cache == nil {
		return g.Commands()
	}

	staleIDs := make(map[CommandID]bool)

	// First pass: find directly stale commands (inputs changed on disk)
	for id, cmd := range g.commands {
		if needs, _ := cache.NeedsCommandBuild(string(id)); needs {
			staleIDs[id] = true
		} else {
			// Also check if any output is missing
			for _, out := range cmd.Outputs {
				if _, err := os.Stat(out); os.IsNotExist(err) {
					staleIDs[id] = true
					break
				}
			}
		}
	}

	// Second pass: cascade - if a command is stale, everything that reads its outputs is stale too
	changed := true
	for changed {
		changed = false
		for id := range staleIDs {
			for _, dep := range g.Dependents(id) {
				if !staleIDs[dep.ID] {
					staleIDs[dep.ID] = true
					changed = true
				}
			}
		}
	}

	// Return in insertion order for deterministic rebuild sequence
	var result []*Command
	for _, id := range g.order {
		if staleIDs[id] {
			result = append(result, g.commands[id])
		}
	}
	return result
}

// GetAffected returns all command IDs that would need rebuild if the given file changed.
func (g *CommandGraph) GetAffected(changedFile string) []CommandID {
	stale := make(map[CommandID]bool)

	// Find commands that directly read this file
	for _, id := range g.consumers[changedFile] {
		stale[id] = true
	}

	// Cascade through dependents
	changed := true
	for changed {
		changed = false
		for id := range stale {
			for _, dep := range g.Dependents(id) {
				if !stale[dep.ID] {
					stale[dep.ID] = true
					changed = true
				}
			}
		}
	}

	var result []CommandID
	for _, id := range g.order {
		if stale[id] {
			result = append(result, id)
		}
	}
	return result
}

// ToGraphState converts a CommandGraph to a serializable GraphState for caching.
func (g *CommandGraph) ToGraphState() *state.GraphState {
	ids := make([]string, len(g.order))
	for i, id := range g.order {
		ids[i] = string(id)
	}

	producers := make(map[string]string, len(g.producers))
	for file, id := range g.producers {
		producers[file] = string(id)
	}

	consumers := make(map[string][]string, len(g.consumers))
	for file, ids := range g.consumers {
		sids := make([]string, len(ids))
		for i, id := range ids {
			sids[i] = string(id)
		}
		consumers[file] = sids
	}

	return &state.GraphState{
		CommandIDs: ids,
		Producers:  producers,
		Consumers:  consumers,
	}
}

// FromGraphState reconstructs a CommandGraph from cached state.
// It uses the CommandState entries from the build cache to populate each command.
func FromGraphState(gs *state.GraphState, commands map[string]*state.CommandState) *CommandGraph {
	g := NewCommandGraph()

	for _, idStr := range gs.CommandIDs {
		cs := commands[idStr]
		if cs == nil {
			continue
		}

		cmd := &Command{
			ID:           CommandID(idStr),
			Executable:   cs.Command,
			Args:         strings.Fields(cs.Command),
			Inputs:       cs.Inputs,
			Outputs:      cs.Outputs,
			InputHashes:  cs.InputHashes,
			OutputHashes: cs.OutputHashes,
		}
		g.commands[cmd.ID] = cmd
		g.order = append(g.order, cmd.ID)
	}

	for file, idStr := range gs.Producers {
		g.producers[file] = CommandID(idStr)
	}
	for file, idStrs := range gs.Consumers {
		ids := make([]CommandID, len(idStrs))
		for i, s := range idStrs {
			ids[i] = CommandID(s)
		}
		g.consumers[file] = ids
	}

	return g
}

// BuildFromTrace constructs a CommandGraph from traced subprocess and file access data.
// It groups file accesses by PID, creates a Command for each subprocess, and builds
// the producer->consumer edges.
func BuildFromTrace(procs []tracer.ProcessInfo, accesses []tracer.FileAccess) *CommandGraph {
	g := NewCommandGraph()

	// Group file accesses by PID
	accessByPID := make(map[int][]tracer.FileAccess)
	for _, a := range accesses {
		accessByPID[a.PID] = append(accessByPID[a.PID], a)
	}

	// Create a Command for each traced subprocess
	for _, proc := range procs {
		inputs, outputs := categorizeProcessAccesses(accessByPID[proc.PID])

		cmd := &Command{
			Executable: proc.Filename,
			Args:       proc.Argv,
			PID:        proc.PID,
			Inputs:     inputs,
			Outputs:    outputs,
		}
		cmd.ID = NewCommandID(cmd.Executable, cmd.Args, cmd.Outputs)
		cmd.UpdateHashes()

		g.AddCommand(cmd)
	}

	return g
}

// categorizeProcessAccesses splits file accesses for a single process into
// inputs (reads) and outputs (writes), filtering out noise like /dev, /proc, etc.
func categorizeProcessAccesses(accesses []tracer.FileAccess) (inputs, outputs []string) {
	inSeen := make(map[string]bool)
	outSeen := make(map[string]bool)

	for _, a := range accesses {
		path := a.Path
		if shouldFilterPath(path) {
			continue
		}

		// Resolve to absolute path if possible
		if !filepath.IsAbs(path) {
			if abs, err := filepath.Abs(path); err == nil {
				path = abs
			}
		}

		switch a.Operation {
		case tracer.OpRead, tracer.OpOpen:
			if a.Flags&0x3 == 0 && !inSeen[path] {
				inSeen[path] = true
				inputs = append(inputs, path)
			}
		case tracer.OpWrite:
			if !outSeen[path] {
				outSeen[path] = true
				outputs = append(outputs, path)
			}
		}
	}
	return
}

// shouldFilterPath returns true for paths that are not meaningful build artifacts
// (system libraries, /dev, /proc, etc.).
func shouldFilterPath(path string) bool {
	prefixes := []string{
		"/dev/",
		"/proc/",
		"/sys/",
		"/etc/ld.so",
		"/usr/lib/locale/",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	// Filter shared library opens (not build inputs)
	if strings.HasSuffix(path, ".so") || strings.Contains(path, ".so.") {
		return true
	}

	return false
}
