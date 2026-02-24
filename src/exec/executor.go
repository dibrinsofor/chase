package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/graph"
	"github.com/dibrinsofor/chase/src/state"
	"github.com/dibrinsofor/chase/src/tracer"
)

type Result struct {
	NodeID     graph.NodeID
	Success    bool
	Error      error
	Output     string
	FileAccess []tracer.FileAccess
}

type Executor struct {
	dag     *graph.DAG
	env     *src.ChaseEnv
	workers int
	cache   *state.BuildState
}

const queueBufferSize = 64

func New(dag *graph.DAG, env *src.ChaseEnv, workers int, cache *state.BuildState) *Executor {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return &Executor{
		dag:     dag,
		env:     env,
		workers: workers,
		cache:   cache,
	}
}

func (e *Executor) SaveCache() error {
	if e.cache != nil {
		return e.cache.Save()
	}
	return nil
}

func (e *Executor) Run(ctx context.Context) error {
	if e.dag.Size() == 0 {
		return nil
	}

	if _, err := e.dag.TopologicalSort(); err != nil {
		return err
	}

	q := boundedQueueSize()
	jobs := make(chan graph.NodeID, q)
	results := make(chan Result, q)

	for i := 0; i < e.workers; i++ {
		go e.worker(ctx, jobs, results)
	}

	return e.coordinate(ctx, jobs, results)
}

func boundedQueueSize() int {
	return queueBufferSize
}

func (e *Executor) coordinate(ctx context.Context, jobs chan<- graph.NodeID, results <-chan Result) error {
	pending := e.dag.Size()
	inFlight := 0

	dispatch := func() {
		for _, id := range e.dag.GetReady() {
			e.dag.MarkRunning(id)
			jobs <- id
			inFlight++
		}
	}

	dispatch()

	for pending > 0 {
		select {
		case <-ctx.Done():
			close(jobs)
			return ctx.Err()

		case res := <-results:
			inFlight--
			pending--

			if res.Success {
				e.dag.MarkComplete(res.NodeID)
				if len(res.FileAccess) > 0 {
					node := e.dag.GetNode(res.NodeID)
					if node != nil {
						inputs, outputs := categorizeAccesses(res.FileAccess)
						node.InputFiles = inputs
						node.OutputFiles = outputs
					}
				}
				if res.Output != "" {
					fmt.Print(res.Output)
				}
			} else {
				e.dag.MarkFailed(res.NodeID, res.Error)
				close(jobs)
				return fmt.Errorf("task %s failed: %w", res.NodeID, res.Error)
			}

			dispatch()
		}
	}

	close(jobs)
	return nil
}

func (e *Executor) worker(ctx context.Context, jobs <-chan graph.NodeID, results chan<- Result) {
	for id := range jobs {
		select {
		case <-ctx.Done():
			results <- Result{NodeID: id, Success: false, Error: ctx.Err()}
			return
		default:
			result := e.execute(ctx, id)
			results <- result
		}
	}
}

func (e *Executor) execute(ctx context.Context, id graph.NodeID) Result {
	node := e.dag.GetNode(id)
	if node == nil {
		return Result{NodeID: id, Success: false, Error: fmt.Errorf("node not found")}
	}

	// Target-level cache check
	needsBuild := true
	reason := "no previous build"

	if e.cache != nil {
		needsBuild, reason = e.cache.NeedsBuild(string(id))
		if !needsBuild {
			return Result{NodeID: id, Success: true, Output: fmt.Sprintf("[cached] %s\n", id)}
		}
		if reason != "" && reason != "no previous build" {
			fmt.Printf("[rebuild] %s: %s\n", id, reason)
		}
	}

	return e.executeWithTracing(ctx, id, node)
}

// executeWithTracing runs the command with fsatrace to capture file accesses.
func (e *Executor) executeWithTracing(ctx context.Context, id graph.NodeID, node *graph.Node) Result {
	shell := e.env.Shell()
	var output string
	var allAccesses []tracer.FileAccess

	for _, cmdStr := range node.Commands {
		out, accesses, err := e.runTracedCommand(ctx, shell, cmdStr)
		output += out
		allAccesses = append(allAccesses, accesses...)
		if err != nil {
			return Result{NodeID: id, Success: false, Error: err, Output: output, FileAccess: allAccesses}
		}
	}

	if e.cache != nil && len(allAccesses) > 0 {
		inputs, outputs := categorizeAccesses(allAccesses)
		e.cache.RecordBuild(string(id), inputs, outputs)
	}

	return Result{NodeID: id, Success: true, Output: output, FileAccess: allAccesses}
}

// runTracedCommand executes a single command with tracing.
func (e *Executor) runTracedCommand(ctx context.Context, shell []string, cmdStr string) (string, []tracer.FileAccess, error) {
	cfg := tracer.DefaultConfig()

	t, err := tracer.New(cfg)
	if err != nil {
		// No tracing available, run directly without tracing
		return e.runDirectCommand(ctx, shell, cmdStr)
	}
	defer t.Cleanup()

	wrapped, err := t.WrapCommand(shell, cmdStr)
	if err != nil {
		// Fall back to direct execution
		return e.runDirectCommand(ctx, shell, cmdStr)
	}

	wrapped.Env = os.Environ()
	for key, value := range e.env.Vars() {
		wrapped.Env = append(wrapped.Env, fmt.Sprintf("%s=%s", key, value))
	}

	var outBuf bytes.Buffer
	wrapped.Stdout = &outBuf
	wrapped.Stderr = &outBuf

	if err := wrapped.Run(); err != nil {
		return outBuf.String(), nil, err
	}

	accesses, _ := t.ParseOutput()
	return outBuf.String(), accesses, nil
}

// runDirectCommand executes a command without tracing.
func (e *Executor) runDirectCommand(ctx context.Context, shell []string, cmdStr string) (string, []tracer.FileAccess, error) {
	args := append(shell[1:], cmdStr)
	cmd := exec.CommandContext(ctx, shell[0], args...)
	cmd.Env = os.Environ()

	for key, value := range e.env.Vars() {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	out, err := cmd.CombinedOutput()
	return string(out), nil, err
}

func categorizeAccesses(accesses []tracer.FileAccess) (inputs, outputs []string) {
	seen := make(map[string]bool)
	for _, a := range accesses {
		if seen[a.Path] {
			continue
		}
		seen[a.Path] = true

		switch a.Operation {
		case tracer.OpRead:
			inputs = append(inputs, a.Path)
		case tracer.OpWrite:
			outputs = append(outputs, a.Path)
		}
	}
	return
}

func BuildDAG(env *src.ChaseEnv) *graph.DAG {
	dag := graph.NewDAG()

	for _, dash := range env.Dashes() {
		node := graph.NewNode(
			graph.NodeID(dash.Name()),
			dash.Name(),
			dash.Cmds(),
			dash.DeclaredDeps(),
		)
		node.Summary = dash.Summary()
		dag.AddNode(node)
	}

	return dag
}
