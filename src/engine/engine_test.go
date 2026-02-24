package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dibrinsofor/chase/src"
)

func writeChasefile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Chasefile")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write chasefile: %v", err)
	}
	return path
}

func TestComputeParsed(t *testing.T) {
	path := writeChasefile(t, `
set shell = ["sh", "-c"]
build:
    cmds: "echo ok"
`)

	e := New(path, 1)
	r := e.Compute(context.Background(), ComputeKey{Kind: KeyParsed})
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	if r.Value == nil {
		t.Fatal("expected parsed AST value")
	}
}

func TestComputeMarshaled(t *testing.T) {
	path := writeChasefile(t, `
set shell = ["sh", "-c"]
build:
    summary: "build target"
    cmds: "echo ok"
`)

	e := New(path, 1)
	r := e.Compute(context.Background(), ComputeKey{Kind: KeyMarshaled})
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	env, ok := r.Value.(*src.ChaseEnv)
	if !ok {
		t.Fatalf("unexpected value type: %T", r.Value)
	}
	if len(env.Dashes()) != 1 {
		t.Fatalf("dashes count = %d, want 1", len(env.Dashes()))
	}
}

func TestComputeTransformedBuildsDAGAndSubgraph(t *testing.T) {
	path := writeChasefile(t, `
set shell = ["sh", "-c"]
a:
    cmds: "echo a"
b:
    uses: a
    cmds: "echo b"
c:
    uses: b
    cmds: "echo c"
`)

	e := New(path, 1)
	full := e.Compute(context.Background(), ComputeKey{Kind: KeyTransformed})
	if full.Err != nil {
		t.Fatalf("unexpected full transform error: %v", full.Err)
	}
	fullPlan := full.Value.(*TransformedPlan)
	if fullPlan.DAG.Size() != 3 {
		t.Fatalf("full dag size = %d, want 3", fullPlan.DAG.Size())
	}

	sub := e.Compute(context.Background(), ComputeKey{Kind: KeyTransformed, Target: "b"})
	if sub.Err != nil {
		t.Fatalf("unexpected subgraph transform error: %v", sub.Err)
	}
	subPlan := sub.Value.(*TransformedPlan)
	if subPlan.DAG.Size() != 2 {
		t.Fatalf("subgraph dag size = %d, want 2", subPlan.DAG.Size())
	}
}

func TestComputeDeduplicatesInFlightAndCachedResults(t *testing.T) {
	path := writeChasefile(t, `
set shell = ["sh", "-c"]
build:
    cmds: "echo ok"
`)

	e := New(path, 1)
	const n = 24
	results := make([]ComputeResult, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = e.Compute(context.Background(), ComputeKey{Kind: KeyParsed})
		}(i)
	}
	wg.Wait()

	first, ok := results[0].Value.(*src.Chasefile)
	if results[0].Err != nil {
		t.Fatalf("unexpected error in first result: %v", results[0].Err)
	}
	if !ok {
		t.Fatalf("unexpected value type: %T", results[0].Value)
	}

	for i := 1; i < n; i++ {
		if results[i].Err != nil {
			t.Fatalf("unexpected error in result %d: %v", i, results[i].Err)
		}
		got, ok := results[i].Value.(*src.Chasefile)
		if !ok {
			t.Fatalf("unexpected value type in result %d: %T", i, results[i].Value)
		}
		if got != first {
			t.Fatalf("result %d did not reuse cached parsed value pointer", i)
		}
	}
}

func TestComputeRespectsCanceledContext(t *testing.T) {
	path := writeChasefile(t, `
set shell = ["sh", "-c"]
build:
    cmds: "echo ok"
`)

	e := New(path, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := e.Compute(ctx, ComputeKey{Kind: KeyParsed})
	if r.Err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestComputePropagatesParseErrorToMarshaledPhase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MissingChasefile")
	e := New(path, 1)

	r := e.Compute(context.Background(), ComputeKey{Kind: KeyMarshaled})
	if r.Err == nil {
		t.Fatal("expected parse/open error")
	}
}

func TestComputeTransformedMissingTargetReturnsError(t *testing.T) {
	path := writeChasefile(t, `
set shell = ["sh", "-c"]
build:
    cmds: "echo ok"
`)

	e := New(path, 1)
	r := e.Compute(context.Background(), ComputeKey{Kind: KeyTransformed, Target: "does_not_exist"})
	if r.Err == nil {
		t.Fatal("expected missing target error")
	}
}

func TestComputeExecutedRunsOnlyTargetSubgraph(t *testing.T) {
	tmpDir := t.TempDir()
	chasefile := filepath.Join(tmpDir, "Chasefile")
	logPath := filepath.Join(tmpDir, "run.log")

	content := `
set shell = ["sh", "-c"]
a:
    cmds: "echo a >> ` + logPath + `"
b:
    uses: a
    cmds: "echo b >> ` + logPath + `"
c:
    uses: a
    cmds: "echo c >> ` + logPath + `"
`
	if err := os.WriteFile(chasefile, []byte(content), 0644); err != nil {
		t.Fatalf("write chasefile: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	e := New(chasefile, 1)
	r := e.Compute(context.Background(), ComputeKey{Kind: KeyExecuted, Target: "b"})
	if r.Err != nil {
		t.Fatalf("unexpected execute error: %v", r.Err)
	}

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	log := string(b)
	if !strings.Contains(log, "a\n") {
		t.Fatalf("expected dependency task a to run, log=%q", log)
	}
	if !strings.Contains(log, "b\n") {
		t.Fatalf("expected target task b to run, log=%q", log)
	}
	if strings.Contains(log, "c\n") {
		t.Fatalf("unexpected unrelated task c run, log=%q", log)
	}
}
