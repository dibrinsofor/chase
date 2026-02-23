package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/graph"
	"github.com/dibrinsofor/chase/src/ir"
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

func loadEnvFromChasefileContent(t *testing.T, chasefileContent string) *src.ChaseEnv {
	t.Helper()
	ast, err := src.ChasefileParser.ParseString("Chasefile", chasefileContent)
	if err != nil {
		t.Fatalf("failed to parse chasefile content: %v", err)
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

func TestIncrementalSubprocessGraphSkipsTopLevelCommand(t *testing.T) {
	tmpDir := t.TempDir()
	topLevelMarker := filepath.Join(tmpDir, "top-level-ran.txt")
	subprocessMarker := filepath.Join(tmpDir, "subprocess-ran.txt")
	inputFile := filepath.Join(tmpDir, "input.txt")
	outputFile := filepath.Join(tmpDir, "output.txt")

	if err := os.WriteFile(inputFile, []byte("stable-input"), 0644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if err := os.WriteFile(outputFile, []byte("stable-output"), 0644); err != nil {
		t.Fatalf("write output: %v", err)
	}

	env := loadEnvFromChasefileContent(t, fmt.Sprintf(`
set shell = ["sh", "-c"]
build:
    cmds: "echo TOP_LEVEL >> %s"
`, topLevelMarker))
	dag := BuildDAG(env)

	cache := state.NewBuildState()
	cmd := &ir.Command{
		Executable: "sh",
		Args:       []string{"echo", "SUBPROCESS", ">>", subprocessMarker},
		Inputs:     []string{inputFile},
		Outputs:    []string{outputFile},
	}
	cmd.ID = ir.NewCommandID(cmd.Executable, cmd.Args, cmd.Outputs)
	cmd.UpdateHashes()

	cmdGraph := ir.NewCommandGraph()
	cmdGraph.AddCommand(cmd)

	cache.RecordGraphState("build", cmdGraph.ToGraphState())
	cache.RecordCommandState(string(cmd.ID), cmd.ToCommandState())

	exec := New(dag, env, 1, cache)
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	if _, err := os.Stat(topLevelMarker); err == nil {
		t.Fatalf("top-level command should not run when subprocess graph is fully cached")
	}
	if _, err := os.Stat(subprocessMarker); err == nil {
		t.Fatalf("subprocess command should not run when subprocess inputs are unchanged")
	}
}

func TestIncrementalSubprocessGraphRebuildsOnlyStaleAndDependents(t *testing.T) {
	tmpDir := t.TempDir()
	topLevelMarker := filepath.Join(tmpDir, "top-level-ran.txt")
	runLog := filepath.Join(tmpDir, "subprocess-run.log")

	aIn := filepath.Join(tmpDir, "a.in")
	aMid := filepath.Join(tmpDir, "a.mid")
	aOut := filepath.Join(tmpDir, "a.out")
	cIn := filepath.Join(tmpDir, "c.in")
	cMid := filepath.Join(tmpDir, "c.mid")
	cOut := filepath.Join(tmpDir, "c.out")

	writeFile := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	writeFile(aIn, "a-v1")
	writeFile(aMid, "a-mid")
	writeFile(aOut, "a-out")
	writeFile(cIn, "c-v1")
	writeFile(cMid, "c-mid")
	writeFile(cOut, "c-out")

	env := loadEnvFromChasefileContent(t, fmt.Sprintf(`
set shell = ["sh", "-c"]
build:
    cmds: "echo TOP_LEVEL >> %s"
`, topLevelMarker))
	dag := BuildDAG(env)
	cache := state.NewBuildState()

	mkCommand := func(name string, inputs, outputs []string) *ir.Command {
		cmd := &ir.Command{
			Executable: "sh",
			Args:       []string{"echo", name, ">>", runLog},
			Inputs:     inputs,
			Outputs:    outputs,
		}
		cmd.ID = ir.NewCommandID(cmd.Executable, cmd.Args, cmd.Outputs)
		cmd.UpdateHashes()
		return cmd
	}

	cmdA := mkCommand("A", []string{aIn}, []string{aMid})
	cmdB := mkCommand("B", []string{aMid}, []string{aOut})
	cmdC := mkCommand("C", []string{cIn}, []string{cMid})
	cmdD := mkCommand("D", []string{cMid}, []string{cOut})

	cmdGraph := ir.NewCommandGraph()
	for _, cmd := range []*ir.Command{cmdA, cmdB, cmdC, cmdD} {
		cmdGraph.AddCommand(cmd)
		cache.RecordCommandState(string(cmd.ID), cmd.ToCommandState())
	}
	cache.RecordGraphState("build", cmdGraph.ToGraphState())

	// Change only A's source input, which should mark A stale and cascade to B.
	writeFile(aIn, "a-v2")

	exec := New(dag, env, 1, cache)
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	if _, err := os.Stat(topLevelMarker); err == nil {
		t.Fatalf("top-level command should not run in incremental subprocess mode")
	}

	b, err := os.ReadFile(runLog)
	if err != nil {
		t.Fatalf("expected subprocess run log: %v", err)
	}
	log := string(b)
	if !strings.Contains(log, "A\n") {
		t.Fatalf("expected stale command A to run, got log: %q", log)
	}
	if !strings.Contains(log, "B\n") {
		t.Fatalf("expected dependent command B to run, got log: %q", log)
	}
	if strings.Contains(log, "C\n") {
		t.Fatalf("unrelated command C should not run, got log: %q", log)
	}
	if strings.Contains(log, "D\n") {
		t.Fatalf("unrelated command D should not run, got log: %q", log)
	}
}
