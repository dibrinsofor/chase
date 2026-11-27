package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/debug"
	"github.com/dibrinsofor/chase/src/graph"
	"github.com/dibrinsofor/chase/src/state"
	"github.com/dibrinsofor/chase/src/tracer"
)

func dbg(format string, args ...any) {
	debug.Log("exec: "+format, args...)
}

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
	force   bool

	// Tracing status tracking
	tracingTested sync.Once
	tracingFailed bool
	warningsMu    sync.Mutex
	warnings      []error
}

const queueBufferSize = 64

func New(dag *graph.DAG, env *src.ChaseEnv, workers int, cache *state.BuildState, force bool) *Executor {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return &Executor{
		dag:     dag,
		env:     env,
		workers: workers,
		cache:   cache,
		force:   force,
	}
}

func (e *Executor) SaveCache() error {
	if e.cache != nil {
		return e.cache.Save()
	}
	return nil
}

func (e *Executor) Warnings() []error {
	e.warningsMu.Lock()
	defer e.warningsMu.Unlock()
	return e.warnings
}

func (e *Executor) addWarning(err error) {
	e.warningsMu.Lock()
	defer e.warningsMu.Unlock()
	e.warnings = append(e.warnings, err)
}

func (e *Executor) Run(ctx context.Context) error {
	dbg("Run: dag size=%d", e.dag.Size())
	if e.dag.Size() == 0 {
		return nil
	}

	if _, err := e.dag.TopologicalSort(); err != nil {
		return err
	}

	q := boundedQueueSize()
	jobs := make(chan graph.NodeID, q)
	results := make(chan Result, q)

	dbg("Run: starting %d workers", e.workers)
	for i := 0; i < e.workers; i++ {
		go e.worker(ctx, jobs, results)
	}

	dbg("Run: calling coordinate")
	err := e.coordinate(ctx, jobs, results)
	dbg("Run: coordinate returned, err=%v", err)
	return err
}

func boundedQueueSize() int {
	return queueBufferSize
}

func (e *Executor) coordinate(ctx context.Context, jobs chan<- graph.NodeID, results <-chan Result) error {
	pending := e.dag.Size()
	inFlight := 0
	dbg("coordinate: pending=%d", pending)

	dispatch := func() {
		ready := e.dag.GetReady()
		dbg("coordinate: dispatch ready=%v", ready)
		for _, id := range ready {
			e.dag.MarkRunning(id)
			jobs <- id
			inFlight++
		}
	}

	dispatch()

	for pending > 0 {
		dbg("coordinate: waiting for result, pending=%d inFlight=%d", pending, inFlight)
		select {
		case <-ctx.Done():
			close(jobs)
			return ctx.Err()

		case res := <-results:
			dbg("coordinate: got result for %s, success=%v", res.NodeID, res.Success)
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

	dbg("coordinate: all done, closing jobs")
	close(jobs)
	return nil
}

func (e *Executor) worker(ctx context.Context, jobs <-chan graph.NodeID, results chan<- Result) {
	for id := range jobs {
		dbg("worker: got job %s", id)
		select {
		case <-ctx.Done():
			results <- Result{NodeID: id, Success: false, Error: ctx.Err()}
			return
		default:
			result := e.execute(ctx, id)
			dbg("worker: job %s done, success=%v", id, result.Success)
			results <- result
		}
	}
	dbg("worker: exiting (jobs channel closed)")
}

func (e *Executor) execute(ctx context.Context, id graph.NodeID) Result {
	node := e.dag.GetNode(id)
	if node == nil {
		return Result{NodeID: id, Success: false, Error: fmt.Errorf("node not found")}
	}

	// Target-level cache check
	needsBuild := true
	reason := "no previous build"

	if e.cache != nil && !e.force {
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

// executeWithTracing runs commands with fsatrace to capture file accesses.
// It skips commands that don't need rebuilding based on cached input hashes.
func (e *Executor) executeWithTracing(ctx context.Context, id graph.NodeID, node *graph.Node) Result {
	shell := e.env.Shell()
	var output string
	var allAccesses []tracer.FileAccess
	var allInputs, allOutputs []string

	for i, cmdStr := range node.Commands {
		cmdID := fmt.Sprintf("%s:%d", id, i)
		dbg("executeWithTracing: cmd %d/%d: %s", i+1, len(node.Commands), cmdStr[:min(60, len(cmdStr))])

		// Check if this specific command needs to run
		if e.cache != nil && !e.force {
			needsBuild, reason := e.cache.NeedsCommandBuild(cmdID)
			if !needsBuild {
				// Command cached - skip tracing entirely
				output += fmt.Sprintf("[skip] %s (cached)\n", cmdStr[:min(40, len(cmdStr))])
				// Preserve cached inputs/outputs for aggregation
				if cs := e.cache.GetCommandState(cmdID); cs != nil {
					allInputs = append(allInputs, cs.Inputs...)
					allOutputs = append(allOutputs, cs.Outputs...)
				}
				dbg("executeWithTracing: cmd %d skipped (cached)", i+1)
				continue
			}
			if reason != "never traced" {
				output += fmt.Sprintf("[run] %s: %s\n", cmdStr[:min(40, len(cmdStr))], reason)
			}
		}

		// Command needs to run - execute with tracing
		out, accesses, err := e.runTracedCommand(ctx, shell, cmdStr)
		dbg("executeWithTracing: cmd %d returned, accesses=%d, err=%v", i+1, len(accesses), err)
		output += out
		allAccesses = append(allAccesses, accesses...)

		if err != nil {
			return Result{NodeID: id, Success: false, Error: err, Output: output, FileAccess: allAccesses}
		}

		// Record this command's state immediately
		dbg("executeWithTracing: cmd %d recording cache state", i+1)
		if e.cache != nil && len(accesses) > 0 {
			inputs, outputs := categorizeAccesses(accesses)
			dbg("executeWithTracing: cmd %d categorized: %d inputs, %d outputs", i+1, len(inputs), len(outputs))
			for _, p := range inputs {
				dbg("  traced input: %s", p)
			}
			for _, p := range outputs {
				dbg("  traced output: %s", p)
			}
			allInputs = append(allInputs, inputs...)
			allOutputs = append(allOutputs, outputs...)

			// Hash inputs now while files are fresh
			dbg("executeWithTracing: cmd %d hashing inputs", i+1)
			inputHashes := make(map[string]string)
			for _, path := range inputs {
				if hash, err := state.HashFile(path); err == nil {
					inputHashes[path] = hash
				}
			}
			dbg("executeWithTracing: cmd %d hashing outputs", i+1)
			outputHashes := make(map[string]string)
			for _, path := range outputs {
				if hash, err := state.HashFile(path); err == nil {
					outputHashes[path] = hash
				}
			}
			dbg("executeWithTracing: cmd %d hashing done", i+1)

			e.cache.RecordCommandState(cmdID, &state.CommandState{
				Command:      cmdStr,
				Inputs:       inputs,
				Outputs:      outputs,
				InputHashes:  inputHashes,
				OutputHashes: outputHashes,
			})
		}
	}

	// Update target-level cache with aggregated inputs/outputs
	if e.cache != nil {
		if len(allInputs) > 0 || len(allOutputs) > 0 {
			e.cache.RecordBuild(string(id), unique(allInputs), unique(allOutputs))
		}
	}

	return Result{NodeID: id, Success: true, Output: output, FileAccess: allAccesses}
}

// unique removes duplicates from a string slice
func unique(slice []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// min returns the smaller of two ints
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// runTracedCommand executes a single command with tracing.
func (e *Executor) runTracedCommand(ctx context.Context, shell []string, cmdStr string) (string, []tracer.FileAccess, error) {
	dbg("runTracedCommand: starting cmd=%q", cmdStr)
	cfg := tracer.DefaultConfig()

	t, err := tracer.New(cfg)
	if err != nil {
		// No tracing available, run directly without tracing
		e.tracingTested.Do(func() {
			e.tracingFailed = true
			e.addWarning(fmt.Errorf("file tracing unavailable: %w; incremental builds disabled", err))
		})
		dbg("runTracedCommand: no tracer, running direct")
		return e.runDirectCommand(ctx, shell, cmdStr)
	}
	defer t.Cleanup()

	dbg("runTracedCommand: wrapping command")
	wrapped, err := t.WrapCommand(shell, cmdStr)
	if err != nil {
		// Fall back to direct execution
		e.tracingTested.Do(func() {
			e.tracingFailed = true
			e.addWarning(fmt.Errorf("file tracing failed to wrap command: %w; incremental builds disabled", err))
		})
		dbg("runTracedCommand: wrap failed, running direct")
		return e.runDirectCommand(ctx, shell, cmdStr)
	}

	wrapped.Env = os.Environ()
	for key, value := range e.env.Vars() {
		wrapped.Env = append(wrapped.Env, fmt.Sprintf("%s=%s", key, value))
	}

	var outBuf bytes.Buffer
	wrapped.Stdout = &outBuf
	wrapped.Stderr = &outBuf

	dbg("runTracedCommand: running wrapped command")
	if err := wrapped.Run(); err != nil {
		dbg("runTracedCommand: command failed err=%v", err)
		return outBuf.String(), nil, err
	}
	dbg("runTracedCommand: command completed")

	accesses, parseErr := t.ParseOutput()
	if parseErr != nil {
		e.tracingTested.Do(func() {
			e.tracingFailed = true
			e.addWarning(fmt.Errorf("file tracing output parse failed: %w; incremental builds disabled", parseErr))
		})
	} else if len(accesses) == 0 {
		e.tracingTested.Do(func() {
			e.tracingFailed = true
			e.addWarning(fmt.Errorf("file tracing captured no accesses (SIP may be blocking on macOS); incremental builds disabled"))
		})
	}
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

		// Skip temp directories - these are intermediate files that get deleted
		if isTempPath(a.Path) {
			continue
		}

		switch a.Operation {
		case tracer.OpRead:
			inputs = append(inputs, a.Path)
		case tracer.OpWrite:
			outputs = append(outputs, a.Path)
		}
	}
	return
}

// isTempPath returns true for paths that are temporary/intermediate files
func isTempPath(path string) bool {
	// Temp directories
	tempPrefixes := []string{
		"/var/folders/",
		"/private/var/folders/",
		"/tmp/",
		"/private/tmp/",
	}
	for _, prefix := range tempPrefixes {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	// Temp file patterns (e.g., foo.o.tmp, foo-abc123.o.tmp)
	if len(path) > 4 && path[len(path)-4:] == ".tmp" {
		return true
	}
	return false
}

func BuildDAG(env *src.ChaseEnv) *graph.DAG {
	dag := graph.NewDAG()

	for _, dash := range env.Dashes() {
		node := graph.NewNode(
			graph.NodeID(dash.Name()),
			dash.Name(),
			dash.Cmds(),
			dash.DeclaredDeps(),
			dash.Inputs(),
			dash.Outputs(),
		)
		node.Summary = dash.Summary()
		dag.AddNode(node)
	}

	return dag
}
