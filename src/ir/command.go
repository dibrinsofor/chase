//go:build experimental_ebpf

package ir

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dibrinsofor/chase/src/state"
)

// CommandID uniquely identifies a traced subprocess command.
// It is a hash of the executable path, arguments, and outputs.
type CommandID string

// Command represents a single traced subprocess (e.g., cc1, as, ld).
type Command struct {
	ID           CommandID
	Executable   string            // Full path to executable (e.g., "/usr/lib/gcc/.../cc1")
	Args         []string          // Full command line arguments
	PID          int               // Process ID during trace
	Inputs       []string          // Files this process read
	Outputs      []string          // Files this process wrote
	InputHashes  map[string]string // SHA256 hashes of inputs at trace time
	OutputHashes map[string]string // SHA256 hashes of outputs at trace time
}

// NewCommandID creates a deterministic ID from executable, args, and outputs.
func NewCommandID(exe string, args, outputs []string) CommandID {
	h := sha256.New()
	h.Write([]byte(exe))
	h.Write([]byte{0})
	for _, a := range args {
		h.Write([]byte(a))
		h.Write([]byte{0})
	}
	// Include sorted outputs for stability
	sorted := make([]string, len(outputs))
	copy(sorted, outputs)
	sort.Strings(sorted)
	for _, o := range sorted {
		h.Write([]byte(o))
		h.Write([]byte{0})
	}
	return CommandID(fmt.Sprintf("%x", h.Sum(nil))[:16])
}

// UpdateHashes re-computes input and output file hashes from disk.
func (c *Command) UpdateHashes() {
	c.InputHashes = make(map[string]string, len(c.Inputs))
	for _, path := range c.Inputs {
		if h, err := state.HashFile(path); err == nil {
			c.InputHashes[path] = h
		}
	}
	c.OutputHashes = make(map[string]string, len(c.Outputs))
	for _, path := range c.Outputs {
		if h, err := state.HashFile(path); err == nil {
			c.OutputHashes[path] = h
		}
	}
}

// String returns a human-readable summary of the command.
func (c *Command) String() string {
	exe := c.Executable
	if idx := strings.LastIndex(exe, "/"); idx >= 0 {
		exe = exe[idx+1:]
	}
	return fmt.Sprintf("%s %s", exe, strings.Join(c.Args, " "))
}

// ToCommandState converts a Command to a serializable CommandState for caching.
func (c *Command) ToCommandState() *state.CommandState {
	return &state.CommandState{
		Command:      strings.Join(c.Args, " "),
		Inputs:       c.Inputs,
		Outputs:      c.Outputs,
		InputHashes:  c.InputHashes,
		OutputHashes: c.OutputHashes,
		LastRun:      time.Now(),
	}
}
