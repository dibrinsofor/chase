package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/graph"
)

type Result struct {
	NodeID  graph.NodeID
	Success bool
	Error   error
	Output  string
}

type Executor struct {
	dag     *graph.DAG
	env     *src.ChaseEnv
	workers int
}

func New(dag *graph.DAG, env *src.ChaseEnv, workers int) *Executor {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return &Executor{
		dag:     dag,
		env:     env,
		workers: workers,
	}
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

	shell := e.env.Shell()
	var output string

	for _, cmdStr := range node.Commands {
		cmd := exec.CommandContext(ctx, shell[0], append(shell[1:], cmdStr)...)
		cmd.Env = os.Environ()

		for key, value := range e.env.Vars() {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}

		out, err := cmd.CombinedOutput()
		output += string(out)

		if err != nil {
			return Result{NodeID: id, Success: false, Error: err, Output: output}
		}
	}

	return Result{NodeID: id, Success: true, Output: output}
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
