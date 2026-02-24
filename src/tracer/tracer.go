package tracer

import (
	"os/exec"
)

type Operation int

const (
	OpRead Operation = iota
	OpWrite
)

func (o Operation) String() string {
	switch o {
	case OpRead:
		return "read"
	case OpWrite:
		return "write"
	default:
		return "unknown"
	}
}

// FileAccess represents a file access traced during command execution.
// Simplified from the eBPF version - we don't need PID/TID/Timestamp
// for target-level tracing with fsatrace.
type FileAccess struct {
	Path      string
	Operation Operation
}

// Tracer wraps command execution with file access tracing.
// This wrapper-style interface works with fsatrace (library interposition).
type Tracer interface {
	// WrapCommand prepends the tracer to the command.
	// Returns an exec.Cmd ready to run with tracing enabled.
	WrapCommand(shell []string, cmd string) (*exec.Cmd, error)

	// ParseOutput reads and parses the tracer output file.
	// Returns the file accesses captured during execution.
	ParseOutput() ([]FileAccess, error)

	// Cleanup removes temporary files created by the tracer.
	Cleanup() error
}

type Config struct {
	CaptureReads  bool
	CaptureWrites bool
}

func DefaultConfig() Config {
	return Config{
		CaptureReads:  true,
		CaptureWrites: true,
	}
}

func New(cfg Config) (Tracer, error) {
	return newPlatformTracer(cfg)
}
