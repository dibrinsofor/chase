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
	NodeID      graph.NodeID
	Success     bool
	Error       error
	Output      string
	FileAccess  []tracer.FileAccess
}

type Executor struct {
	dag      *graph.DAG
	env      *src.ChaseEnv
	workers  int
	tracing  bool
	cache    *state.BuildState
}

func New(dag *graph.DAG, env *src.ChaseEnv, workers int) *Executor {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return &Executor{
		dag:      dag,
		env:      env,
		workers:  workers,
		tracing:  false,
	}
}

func (e *Executor) EnableTracing() {
	e.tracing = true
}

func (e *Executor) SetCache(cache *state.BuildState) {
	e.cache = cache
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

	jobs := make(chan graph.NodeID, e.dag.Size())
	results := make(chan Result, e.dag.Size())

	for i := 0; i < e.workers; i++ {
		go e.worker(ctx, jobs, results)
	}

	return e.coordinate(ctx, jobs, results)
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

	if e.cache != nil && e.tracing {
		needsBuild, reason := e.cache.NeedsBuild(string(id))
		if !needsBuild {
			return Result{NodeID: id, Success: true, Output: fmt.Sprintf("[cached] %s\n", id)}
		}
		if reason != "" && reason != "no previous build" {
			fmt.Printf("[rebuild] %s: %s\n", id, reason)
		}
	}

	shell := e.env.Shell()
	var output string
	var allAccesses []tracer.FileAccess

	for _, cmdStr := range node.Commands {
		cmd := exec.CommandContext(ctx, shell[0], append(shell[1:], cmdStr)...)
		cmd.Env = os.Environ()

		for key, value := range e.env.Vars() {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}

		if e.tracing {
			out, accesses, err := e.executeWithTracing(ctx, cmd)
			output += out
			allAccesses = append(allAccesses, accesses...)
			if err != nil {
				return Result{NodeID: id, Success: false, Error: err, Output: output, FileAccess: allAccesses}
			}
		} else {
			out, err := cmd.CombinedOutput()
			output += string(out)
			if err != nil {
				return Result{NodeID: id, Success: false, Error: err, Output: output}
			}
		}
	}

	if e.cache != nil && e.tracing && len(allAccesses) > 0 {
		inputs, outputs := categorizeAccesses(allAccesses)
		e.cache.RecordBuild(string(id), inputs, outputs)
	}

	return Result{NodeID: id, Success: true, Output: output, FileAccess: allAccesses}
}

func categorizeAccesses(accesses []tracer.FileAccess) (inputs, outputs []string) {
	seen := make(map[string]bool)
	for _, a := range accesses {
		if seen[a.Path] {
			continue
		}
		seen[a.Path] = true

		switch a.Operation {
		case tracer.OpRead, tracer.OpOpen:
			if a.Flags&0x3 == 0 {
				inputs = append(inputs, a.Path)
			}
		case tracer.OpWrite:
			outputs = append(outputs, a.Path)
		}
	}
	return
}

func (e *Executor) executeWithTracing(ctx context.Context, cmd *exec.Cmd) (string, []tracer.FileAccess, error) {
	cfg := tracer.DefaultConfig()
	cfg.FollowChildren = true

	t, err := tracer.New(cfg)
	if err != nil {
		out, cmdErr := cmd.CombinedOutput()
		return string(out), nil, cmdErr
	}

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		return "", nil, err
	}

	if err := t.Start(ctx, cmd.Process.Pid); err != nil {
		cmd.Wait()
		return outBuf.String(), nil, fmt.Errorf("failed to start tracer: %w", err)
	}

	cmdErr := cmd.Wait()

	accesses, stopErr := t.Stop()
	if stopErr != nil && cmdErr == nil {
		cmdErr = stopErr
	}

	return outBuf.String(), accesses, cmdErr
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
