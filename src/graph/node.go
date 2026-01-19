package graph

import "time"

type NodeID string

type NodeState int

const (
	Pending NodeState = iota
	Running
	Completed
	Failed
	Skipped
)

type Node struct {
	ID           NodeID
	Name         string
	Summary      string
	Commands     []string
	DeclaredDeps []NodeID
	TracedDeps   []NodeID
	InputFiles   []string
	OutputFiles  []string
	InputHashes  map[string]string
	State        NodeState
	Error        error
	StartTime    time.Time
	EndTime      time.Time
}

func NewNode(id NodeID, name string, cmds []string, deps []string) *Node {
	declaredDeps := make([]NodeID, len(deps))
	for i, d := range deps {
		declaredDeps[i] = NodeID(d)
	}

	return &Node{
		ID:           id,
		Name:         name,
		Commands:     cmds,
		DeclaredDeps: declaredDeps,
		InputHashes:  make(map[string]string),
		State:        Pending,
	}
}

func (n *Node) AllDeps() []NodeID {
	deps := make([]NodeID, 0, len(n.DeclaredDeps)+len(n.TracedDeps))
	deps = append(deps, n.DeclaredDeps...)
	deps = append(deps, n.TracedDeps...)
	return deps
}

func (s NodeState) String() string {
	switch s {
	case Pending:
		return "pending"
	case Running:
		return "running"
	case Completed:
		return "completed"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	default:
		return "unknown"
	}
}
