package tracer

import (
	"context"
)

type Operation int

const (
	OpRead Operation = iota
	OpWrite
	OpOpen
	OpClose
)

func (o Operation) String() string {
	switch o {
	case OpRead:
		return "read"
	case OpWrite:
		return "write"
	case OpOpen:
		return "open"
	case OpClose:
		return "close"
	default:
		return "unknown"
	}
}

type FileAccess struct {
	Path      string
	Operation Operation
	PID       int
	TID       int
	Timestamp uint64
	Flags     uint32
}

// ProcessInfo captures subprocess spawn information from execve
type ProcessInfo struct {
	PID       int
	PPID      int
	Comm      string   // Process name (e.g., "cc1", "as", "ld")
	Filename  string   // Full path to executable
	Argv      []string // Command line arguments
	Timestamp uint64
}

// TraceResult holds the combined results from tracing
type TraceResult struct {
	FileAccesses []FileAccess
	Processes    []ProcessInfo
}

type Tracer interface {
	Start(ctx context.Context, pid int) error
	Stop() ([]FileAccess, []ProcessInfo, error)
	Events() <-chan FileAccess
}

type Config struct {
	FilterPID      int
	FollowChildren bool
	CaptureReads   bool
	CaptureWrites  bool
}

func DefaultConfig() Config {
	return Config{
		FilterPID:      0,
		FollowChildren: true,
		CaptureReads:   true,
		CaptureWrites:  true,
	}
}

func New(cfg Config) (Tracer, error) {
	return newPlatformTracer(cfg)
}
