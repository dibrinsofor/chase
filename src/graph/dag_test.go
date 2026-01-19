package graph

import (
	"reflect"
	"testing"
)

func TestTopologicalSort(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []*Node
		wantOrder func([]NodeID) bool
		wantErr   bool
	}{
		{
			name: "single node",
			nodes: []*Node{
				NewNode("a", "a", []string{"echo a"}, nil),
			},
			wantOrder: func(order []NodeID) bool {
				return len(order) == 1 && order[0] == "a"
			},
		},
		{
			name: "linear chain",
			nodes: []*Node{
				NewNode("a", "a", []string{"echo a"}, nil),
				NewNode("b", "b", []string{"echo b"}, []string{"a"}),
				NewNode("c", "c", []string{"echo c"}, []string{"b"}),
			},
			wantOrder: func(order []NodeID) bool {
				if len(order) != 3 {
					return false
				}
				aIdx, bIdx, cIdx := -1, -1, -1
				for i, id := range order {
					switch id {
					case "a":
						aIdx = i
					case "b":
						bIdx = i
					case "c":
						cIdx = i
					}
				}
				return aIdx < bIdx && bIdx < cIdx
			},
		},
		{
			name: "diamond dependency",
			nodes: []*Node{
				NewNode("a", "a", []string{"echo a"}, nil),
				NewNode("b", "b", []string{"echo b"}, []string{"a"}),
				NewNode("c", "c", []string{"echo c"}, []string{"a"}),
				NewNode("d", "d", []string{"echo d"}, []string{"b", "c"}),
			},
			wantOrder: func(order []NodeID) bool {
				if len(order) != 4 {
					return false
				}
				pos := make(map[NodeID]int)
				for i, id := range order {
					pos[id] = i
				}
				return pos["a"] < pos["b"] && pos["a"] < pos["c"] &&
					pos["b"] < pos["d"] && pos["c"] < pos["d"]
			},
		},
		{
			name: "parallel independent",
			nodes: []*Node{
				NewNode("a", "a", []string{"echo a"}, nil),
				NewNode("b", "b", []string{"echo b"}, nil),
				NewNode("c", "c", []string{"echo c"}, nil),
			},
			wantOrder: func(order []NodeID) bool {
				return len(order) == 3
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dag := NewDAG()
			for _, n := range tt.nodes {
				dag.AddNode(n)
			}

			order, err := dag.TopologicalSort()
			if (err != nil) != tt.wantErr {
				t.Errorf("TopologicalSort() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !tt.wantOrder(order) {
				t.Errorf("TopologicalSort() order = %v, invalid order", order)
			}
		})
	}
}

func TestDetectCycle(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []*Node
		addEdges  [][2]NodeID
		wantCycle bool
	}{
		{
			name: "no cycle",
			nodes: []*Node{
				NewNode("a", "a", nil, nil),
				NewNode("b", "b", nil, []string{"a"}),
			},
			wantCycle: false,
		},
		{
			name: "direct cycle",
			nodes: []*Node{
				NewNode("a", "a", nil, nil),
				NewNode("b", "b", nil, nil),
			},
			addEdges: [][2]NodeID{
				{"a", "b"},
				{"b", "a"},
			},
			wantCycle: true,
		},
		{
			name: "indirect cycle",
			nodes: []*Node{
				NewNode("a", "a", nil, nil),
				NewNode("b", "b", nil, nil),
				NewNode("c", "c", nil, nil),
			},
			addEdges: [][2]NodeID{
				{"a", "b"},
				{"b", "c"},
				{"c", "a"},
			},
			wantCycle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dag := NewDAG()
			for _, n := range tt.nodes {
				dag.AddNode(n)
			}
			for _, edge := range tt.addEdges {
				dag.AddEdge(edge[0], edge[1])
			}

			_, hasCycle := dag.DetectCycle()
			if hasCycle != tt.wantCycle {
				t.Errorf("DetectCycle() = %v, want %v", hasCycle, tt.wantCycle)
			}
		})
	}
}

func TestGetReady(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []*Node
		completed []NodeID
		wantReady []NodeID
	}{
		{
			name: "all ready when no deps",
			nodes: []*Node{
				NewNode("a", "a", nil, nil),
				NewNode("b", "b", nil, nil),
			},
			completed: nil,
			wantReady: []NodeID{"a", "b"},
		},
		{
			name: "blocked by dep",
			nodes: []*Node{
				NewNode("a", "a", nil, nil),
				NewNode("b", "b", nil, []string{"a"}),
			},
			completed: nil,
			wantReady: []NodeID{"a"},
		},
		{
			name: "unblocked after completion",
			nodes: []*Node{
				NewNode("a", "a", nil, nil),
				NewNode("b", "b", nil, []string{"a"}),
			},
			completed: []NodeID{"a"},
			wantReady: []NodeID{"b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dag := NewDAG()
			for _, n := range tt.nodes {
				dag.AddNode(n)
			}
			for _, id := range tt.completed {
				dag.MarkComplete(id)
			}

			ready := dag.GetReady()

			if len(ready) != len(tt.wantReady) {
				t.Errorf("GetReady() len = %d, want %d", len(ready), len(tt.wantReady))
				return
			}

			readyMap := make(map[NodeID]bool)
			for _, id := range ready {
				readyMap[id] = true
			}
			for _, want := range tt.wantReady {
				if !readyMap[want] {
					t.Errorf("GetReady() missing %s", want)
				}
			}
		})
	}
}

func TestSubgraph(t *testing.T) {
	dag := NewDAG()
	dag.AddNode(NewNode("a", "a", nil, nil))
	dag.AddNode(NewNode("b", "b", nil, []string{"a"}))
	dag.AddNode(NewNode("c", "c", nil, []string{"a"}))
	dag.AddNode(NewNode("d", "d", nil, []string{"b", "c"}))
	dag.AddNode(NewNode("e", "e", nil, nil))

	sub := dag.Subgraph("d")

	if sub.Size() != 4 {
		t.Errorf("Subgraph size = %d, want 4", sub.Size())
	}

	for _, id := range []NodeID{"a", "b", "c", "d"} {
		if sub.GetNode(id) == nil {
			t.Errorf("Subgraph missing node %s", id)
		}
	}

	if sub.GetNode("e") != nil {
		t.Errorf("Subgraph should not contain node e")
	}
}

func TestAddNode(t *testing.T) {
	dag := NewDAG()

	err := dag.AddNode(NewNode("a", "a", nil, nil))
	if err != nil {
		t.Errorf("AddNode() unexpected error: %v", err)
	}

	err = dag.AddNode(NewNode("a", "a", nil, nil))
	if err == nil {
		t.Error("AddNode() should error on duplicate")
	}
}

func TestNodeState(t *testing.T) {
	dag := NewDAG()
	dag.AddNode(NewNode("a", "a", nil, nil))

	if dag.GetNode("a").State != Pending {
		t.Error("initial state should be Pending")
	}

	dag.MarkRunning("a")
	if dag.GetNode("a").State != Running {
		t.Error("state should be Running")
	}

	dag.MarkComplete("a")
	if dag.GetNode("a").State != Completed {
		t.Error("state should be Completed")
	}
}

func TestNodeStateString(t *testing.T) {
	tests := []struct {
		state NodeState
		want  string
	}{
		{Pending, "pending"},
		{Running, "running"},
		{Completed, "completed"},
		{Failed, "failed"},
		{Skipped, "skipped"},
		{NodeState(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("NodeState(%d).String() = %s, want %s", tt.state, got, tt.want)
		}
	}
}

func TestNewNode(t *testing.T) {
	node := NewNode("test", "Test Task", []string{"echo hello"}, []string{"dep1", "dep2"})

	if node.ID != "test" {
		t.Errorf("ID = %s, want test", node.ID)
	}
	if node.Name != "Test Task" {
		t.Errorf("Name = %s, want Test Task", node.Name)
	}
	if !reflect.DeepEqual(node.Commands, []string{"echo hello"}) {
		t.Errorf("Commands = %v, want [echo hello]", node.Commands)
	}
	if !reflect.DeepEqual(node.DeclaredDeps, []NodeID{"dep1", "dep2"}) {
		t.Errorf("DeclaredDeps = %v, want [dep1, dep2]", node.DeclaredDeps)
	}
	if node.State != Pending {
		t.Errorf("State = %v, want Pending", node.State)
	}
}
