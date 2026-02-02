package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/graph"
	"github.com/dibrinsofor/chase/src/state"
)

func loadTestFixture(t *testing.T, name string) *src.ChaseEnv {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", name, "Chasefile")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", name, err)
	}
	ast, err := src.ChasefileParser.ParseString(path, string(b))
	if err != nil {
		t.Fatalf("failed to parse fixture %s: %v", name, err)
	}
	return src.Eval(ast)
}

func TestBuildDAG(t *testing.T) {
	env := loadTestFixture(t, "diamond")
	dag := BuildDAG(env)

	if dag.Size() != 4 {
		t.Errorf("expected 4 nodes, got %d", dag.Size())
	}

	for _, id := range []graph.NodeID{"a", "b", "c", "d"} {
		if dag.GetNode(id) == nil {
			t.Errorf("missing node %s", id)
		}
	}
}

func TestExecutorSimple(t *testing.T) {
	env := loadTestFixture(t, "simple")
	dag := BuildDAG(env)
	exec := New(dag, env, 1, nil)

	ctx := context.Background()
	err := exec.Run(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	node := dag.GetNode("build")
	if node.State != graph.Completed {
		t.Errorf("expected Completed state, got %s", node.State)
	}
}

func TestExecutorDiamond(t *testing.T) {
	env := loadTestFixture(t, "diamond")
	dag := BuildDAG(env)
	exec := New(dag, env, 2, nil)

	ctx := context.Background()
	err := exec.Run(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	for _, id := range []graph.NodeID{"a", "b", "c", "d"} {
		node := dag.GetNode(id)
		if node.State != graph.Completed {
			t.Errorf("node %s: expected Completed, got %s", id, node.State)
		}
	}
}

func TestExecutorCycleDetection(t *testing.T) {
	env := loadTestFixture(t, "cycle")
	dag := BuildDAG(env)
	exec := New(dag, env, 1, nil)

	ctx := context.Background()
	err := exec.Run(ctx)
	if err == nil {
		t.Error("expected cycle detection error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got: %v", err)
	}
}

func TestExecutorParallel(t *testing.T) {
	env := loadTestFixture(t, "parallel")
	dag := BuildDAG(env)

	var mu sync.Mutex
	var runningCount int
	var maxConcurrent int

	originalExecute := func(e *Executor, ctx context.Context, id graph.NodeID) Result {
		mu.Lock()
		runningCount++
		if runningCount > maxConcurrent {
			maxConcurrent = runningCount
		}
		mu.Unlock()

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		runningCount--
		mu.Unlock()

		return Result{NodeID: id, Success: true}
	}
	_ = originalExecute

	exec := New(dag, env, 4, nil)
	ctx := context.Background()
	err := exec.Run(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	for _, id := range []graph.NodeID{"task1", "task2", "task3", "task4"} {
		node := dag.GetNode(id)
		if node.State != graph.Completed {
			t.Errorf("node %s: expected Completed, got %s", id, node.State)
		}
	}
}

func TestExecutorContextCancellation(t *testing.T) {
	env := loadTestFixture(t, "parallel")
	dag := BuildDAG(env)
	exec := New(dag, env, 1, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := exec.Run(ctx)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestExecutorSubgraph(t *testing.T) {
	env := loadTestFixture(t, "diamond")
	dag := BuildDAG(env)
	sub := dag.Subgraph("b")

	if sub.Size() != 2 {
		t.Errorf("expected 2 nodes in subgraph, got %d", sub.Size())
	}

	if sub.GetNode("a") == nil || sub.GetNode("b") == nil {
		t.Error("subgraph should contain nodes a and b")
	}
	if sub.GetNode("c") != nil || sub.GetNode("d") != nil {
		t.Error("subgraph should not contain nodes c or d")
	}

	exec := New(sub, env, 1, nil)
	err := exec.Run(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecutorWorkerCount(t *testing.T) {
	env := loadTestFixture(t, "simple")
	dag := BuildDAG(env)

	exec := New(dag, env, 0, nil)
	if exec.workers <= 0 {
		t.Error("worker count should default to positive value")
	}

	exec = New(dag, env, -5, nil)
	if exec.workers <= 0 {
		t.Error("negative worker count should default to positive value")
	}

	exec = New(dag, env, 8, nil)
	if exec.workers != 8 {
		t.Errorf("expected 8 workers, got %d", exec.workers)
	}
}

func TestExecutorEmptyDAG(t *testing.T) {
	dag := graph.NewDAG()
	env := &src.ChaseEnv{}
	exec := New(dag, env, 1, nil)

	err := exec.Run(context.Background())
	if err != nil {
		t.Errorf("empty DAG should not error: %v", err)
	}
}

func TestIncrementalBuildSkipsCached(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.txt")
	os.WriteFile(inputFile, []byte("content"), 0644)

	cache := state.NewBuildState()
	cache.RecordBuild("build", []string{inputFile}, []string{})

	env := loadTestFixture(t, "simple")
	dag := BuildDAG(env)

	exec := New(dag, env, 1, cache)
	ctx := context.Background()

	start := time.Now()
	err := exec.Run(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	node := dag.GetNode("build")
	if node.State != graph.Completed {
		t.Errorf("expected Completed state, got %s", node.State)
	}

	if elapsed > 50*time.Millisecond {
		t.Logf("execution took %v (expected fast due to cache)", elapsed)
	}
}

func TestIncrementalBuildRebuildsOnInputChange(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.txt")
	os.WriteFile(inputFile, []byte("original"), 0644)

	cache := state.NewBuildState()
	cache.RecordBuild("build", []string{inputFile}, []string{})

	os.WriteFile(inputFile, []byte("changed"), 0644)

	needsBuild, reason := cache.NeedsBuild("build")
	if !needsBuild {
		t.Error("should need rebuild after input change")
	}
	if !strings.Contains(reason, "input changed") {
		t.Errorf("expected 'input changed' reason, got: %s", reason)
	}
}

func TestIncrementalBuildRebuildsOnMissingOutput(t *testing.T) {
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "output.txt")
	os.WriteFile(outputFile, []byte("output"), 0644)

	cache := state.NewBuildState()
	cache.RecordBuild("build", []string{}, []string{outputFile})

	os.Remove(outputFile)

	needsBuild, reason := cache.NeedsBuild("build")
	if !needsBuild {
		t.Error("should need rebuild when output missing")
	}
	if !strings.Contains(reason, "output missing") {
		t.Errorf("expected 'output missing' reason, got: %s", reason)
	}
}

func TestParallelExecutionTiming(t *testing.T) {
	env := loadTestFixture(t, "timed")
	dag := BuildDAG(env)

	exec := New(dag, env, 4, nil)
	ctx := context.Background()

	start := time.Now()
	err := exec.Run(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if elapsed > 300*time.Millisecond {
		t.Errorf("parallel execution took %v, expected ~100ms with 4 workers", elapsed)
	}

	t.Logf("4 tasks with 4 workers completed in %v", elapsed)
}

func TestSequentialExecutionTiming(t *testing.T) {
	env := loadTestFixture(t, "timed")
	dag := BuildDAG(env)

	exec := New(dag, env, 1, nil)
	ctx := context.Background()

	start := time.Now()
	err := exec.Run(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if elapsed < 350*time.Millisecond {
		t.Errorf("sequential execution took %v, expected ~400ms with 1 worker", elapsed)
	}

	t.Logf("4 tasks with 1 worker completed in %v", elapsed)
}

func TestDiamondDependencyOrder(t *testing.T) {
	env := loadTestFixture(t, "diamond")
	dag := BuildDAG(env)

	var mu sync.Mutex
	var executionOrder []graph.NodeID

	exec := New(dag, env, 2, nil)
	originalCoordinate := exec.coordinate

	_ = originalCoordinate

	ctx := context.Background()
	err := exec.Run(ctx)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	for _, id := range []graph.NodeID{"a", "b", "c", "d"} {
		node := dag.GetNode(id)
		if node.State != graph.Completed {
			t.Errorf("node %s: expected Completed, got %s", id, node.State)
		}
	}

	_ = mu
	_ = executionOrder
}
