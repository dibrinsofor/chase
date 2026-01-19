package graph

import "fmt"

type DAG struct {
	Nodes   map[NodeID]*Node
	Edges   map[NodeID][]NodeID // node -> its dependencies
	Reverse map[NodeID][]NodeID // node -> nodes that depend on it
}

func NewDAG() *DAG {
	return &DAG{
		Nodes:   make(map[NodeID]*Node),
		Edges:   make(map[NodeID][]NodeID),
		Reverse: make(map[NodeID][]NodeID),
	}
}

func (d *DAG) AddNode(n *Node) error {
	if _, exists := d.Nodes[n.ID]; exists {
		return fmt.Errorf("node %s already exists", n.ID)
	}

	d.Nodes[n.ID] = n
	d.Edges[n.ID] = n.AllDeps()

	for _, dep := range n.AllDeps() {
		d.Reverse[dep] = append(d.Reverse[dep], n.ID)
	}

	return nil
}

func (d *DAG) AddEdge(from, to NodeID) error {
	if _, exists := d.Nodes[from]; !exists {
		return fmt.Errorf("node %s not found", from)
	}
	if _, exists := d.Nodes[to]; !exists {
		return fmt.Errorf("node %s not found", to)
	}

	d.Edges[from] = append(d.Edges[from], to)
	d.Reverse[to] = append(d.Reverse[to], from)
	return nil
}

func (d *DAG) TopologicalSort() ([]NodeID, error) {
	inDegree := make(map[NodeID]int)
	for id := range d.Nodes {
		inDegree[id] = len(d.Edges[id])
	}

	var queue []NodeID
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	var result []NodeID
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		for _, dependent := range d.Reverse[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(result) != len(d.Nodes) {
		return nil, fmt.Errorf("cycle detected in dependency graph")
	}

	return result, nil
}

func (d *DAG) DetectCycle() ([]NodeID, bool) {
	_, err := d.TopologicalSort()
	if err != nil {
		return d.findCycle(), true
	}
	return nil, false
}

func (d *DAG) findCycle() []NodeID {
	visited := make(map[NodeID]bool)
	recStack := make(map[NodeID]bool)
	var cycle []NodeID

	var dfs func(id NodeID) bool
	dfs = func(id NodeID) bool {
		visited[id] = true
		recStack[id] = true
		cycle = append(cycle, id)

		for _, dep := range d.Edges[id] {
			if !visited[dep] {
				if dfs(dep) {
					return true
				}
			} else if recStack[dep] {
				cycle = append(cycle, dep)
				return true
			}
		}

		cycle = cycle[:len(cycle)-1]
		recStack[id] = false
		return false
	}

	for id := range d.Nodes {
		if !visited[id] {
			if dfs(id) {
				return cycle
			}
		}
	}

	return nil
}

func (d *DAG) GetReady() []NodeID {
	var ready []NodeID
	for id, node := range d.Nodes {
		if node.State != Pending {
			continue
		}

		allDepsComplete := true
		for _, dep := range d.Edges[id] {
			depNode := d.Nodes[dep]
			if depNode == nil || depNode.State != Completed {
				allDepsComplete = false
				break
			}
		}

		if allDepsComplete {
			ready = append(ready, id)
		}
	}

	return ready
}

func (d *DAG) MarkRunning(id NodeID) {
	if node, ok := d.Nodes[id]; ok {
		node.State = Running
	}
}

func (d *DAG) MarkComplete(id NodeID) {
	if node, ok := d.Nodes[id]; ok {
		node.State = Completed
	}
}

func (d *DAG) MarkFailed(id NodeID, err error) {
	if node, ok := d.Nodes[id]; ok {
		node.State = Failed
		node.Error = err
	}
}

func (d *DAG) Subgraph(target NodeID) *DAG {
	sub := NewDAG()
	visited := make(map[NodeID]bool)

	var collect func(id NodeID)
	collect = func(id NodeID) {
		if visited[id] {
			return
		}
		visited[id] = true

		if node, ok := d.Nodes[id]; ok {
			nodeCopy := *node
			nodeCopy.State = Pending
			sub.Nodes[id] = &nodeCopy
			sub.Edges[id] = d.Edges[id]

			for _, dep := range d.Edges[id] {
				sub.Reverse[dep] = append(sub.Reverse[dep], id)
				collect(dep)
			}
		}
	}

	collect(target)
	return sub
}

func (d *DAG) GetNode(id NodeID) *Node {
	return d.Nodes[id]
}

func (d *DAG) Size() int {
	return len(d.Nodes)
}
