package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/graph"
	"github.com/dibrinsofor/chase/src/ir"
	"github.com/dibrinsofor/chase/src/state"
	"github.com/dibrinsofor/chase/src/tracer"
)

type Result struct {
	NodeID     graph.NodeID
	Success    bool
	Error      error
	Output     string
	FileAccess []tracer.FileAccess
	Processes  []tracer.ProcessInfo
}

type Executor struct {
	dag     *graph.DAG
	env     *src.ChaseEnv
	workers int
	cache   *state.BuildState
}

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

	q := boundedQueueSize(e.workers)
	jobs := make(chan graph.NodeID, q)
	results := make(chan Result, q)

	for i := 0; i < e.workers; i++ {
		go e.worker(ctx, jobs, results)
	}

	return e.coordinate(ctx, jobs, results)
}

func boundedQueueSize(workers int) int {
	if workers < 1 {
		workers = 1
	}
	n := workers * 4
	if n < 8 {
		return 8
	}
	if n > 256 {
		return 256
	}
	return n
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

	// Check if we have a traced command graph from a previous run
	if e.cache != nil {
		if gs := e.cache.GetGraphState(string(id)); gs != nil {
			cmdGraph := ir.FromGraphState(gs, e.cache.Commands)
			if cmdGraph.Len() > 0 {
				return e.executeIncremental(ctx, id, node, cmdGraph)
			}
		}
	}

	// Fall back to target-level cache check
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

	return e.executeWithFullTrace(ctx, id, node)
}

// executeWithFullTrace runs the user command, traces all subprocesses, and builds the IR.
func (e *Executor) executeWithFullTrace(ctx context.Context, id graph.NodeID, node *graph.Node) Result {
	shell := e.env.Shell()
	var output string
	var allAccesses []tracer.FileAccess
	var allProcs []tracer.ProcessInfo

	for _, cmdStr := range node.Commands {
		cmd := exec.CommandContext(ctx, shell[0], append(shell[1:], cmdStr)...)
		cmd.Env = os.Environ()

		for key, value := range e.env.Vars() {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}

		out, accesses, procs, err := e.executeWithTracing(ctx, cmd)
		output += out
		allAccesses = append(allAccesses, accesses...)
		allProcs = append(allProcs, procs...)
		if err != nil {
			return Result{NodeID: id, Success: false, Error: err, Output: output, FileAccess: allAccesses, Processes: allProcs}
		}
	}

	if e.cache != nil && len(allAccesses) > 0 {
		inputs, outputs := categorizeAccesses(allAccesses)
		e.cache.RecordBuild(string(id), inputs, outputs)

		// Build command IR from traced subprocesses if we captured any
		if len(allProcs) > 0 {
			cmdGraph := ir.BuildFromTrace(allProcs, allAccesses)
			e.cache.RecordGraphState(string(id), cmdGraph.ToGraphState())
			for _, cmd := range cmdGraph.Commands() {
				e.cache.RecordCommandState(string(cmd.ID), cmd.ToCommandState())
			}
			output += fmt.Sprintf("[built] IR with %d commands, %d edges\n",
				cmdGraph.Len(), cmdGraph.EdgeCount())
		}
	}

	return Result{NodeID: id, Success: true, Output: output, FileAccess: allAccesses, Processes: allProcs}
}

// executeIncremental uses a previously traced command graph to selectively rebuild
// only the subprocesses whose inputs have changed.
func (e *Executor) executeIncremental(ctx context.Context, id graph.NodeID, node *graph.Node, cmdGraph *ir.CommandGraph) Result {
	stale := cmdGraph.GetStale(e.cache)

	if len(stale) == 0 {
		total := cmdGraph.Len()
		return Result{
			NodeID:  id,
			Success: true,
			Output:  fmt.Sprintf("[cached] all %d subprocesses unchanged\n", total),
		}
	}

	total := cmdGraph.Len()
	var output string
	output += fmt.Sprintf("[rebuild] %d of %d subprocesses\n", len(stale), total)

	shell := e.env.Shell()
	var allAccesses []tracer.FileAccess
	var allProcs []tracer.ProcessInfo

	for _, cmd := range stale {
		// Re-run the stale subprocess directly
		args := strings.Join(cmd.Args, " ")
		execCmd := exec.CommandContext(ctx, shell[0], append(shell[1:], args)...)
		execCmd.Env = os.Environ()

		for key, value := range e.env.Vars() {
			execCmd.Env = append(execCmd.Env, fmt.Sprintf("%s=%s", key, value))
		}

		out, accesses, procs, err := e.executeWithTracing(ctx, execCmd)
		output += out
		allAccesses = append(allAccesses, accesses...)
		allProcs = append(allProcs, procs...)

		if err != nil {
			return Result{NodeID: id, Success: false, Error: err, Output: output, FileAccess: allAccesses, Processes: allProcs}
		}

		// Update the cache for this individual command
		if e.cache != nil {
			cmd.UpdateHashes()
			e.cache.RecordCommandState(string(cmd.ID), cmd.ToCommandState())
		}
	}

	// Re-record the target-level state
	if e.cache != nil {
		inputs, outputs := categorizeAccesses(allAccesses)
		existingInputs, existingOutputs := cmdGraph.AllFiles()
		inputs = mergeUnique(existingInputs, inputs)
		outputs = mergeUnique(existingOutputs, outputs)
		e.cache.RecordBuild(string(id), inputs, outputs)
	}

	return Result{NodeID: id, Success: true, Output: output, FileAccess: allAccesses, Processes: allProcs}
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var result []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
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

func (e *Executor) executeWithTracing(ctx context.Context, cmd *exec.Cmd) (string, []tracer.FileAccess, []tracer.ProcessInfo, error) {
	cfg := tracer.DefaultConfig()
	cfg.FollowChildren = true

	t, err := tracer.New(cfg)
	if err != nil {
		out, cmdErr := cmd.CombinedOutput()
		return string(out), nil, nil, cmdErr
	}

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		return "", nil, nil, err
	}

	if err := t.Start(ctx, cmd.Process.Pid); err != nil {
		cmd.Wait()
		return outBuf.String(), nil, nil, fmt.Errorf("failed to start tracer: %w", err)
	}

	cmdErr := cmd.Wait()

	accesses, procs, stopErr := t.Stop()
	if stopErr != nil && cmdErr == nil {
		cmdErr = stopErr
	}

	return outBuf.String(), accesses, procs, cmdErr
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
