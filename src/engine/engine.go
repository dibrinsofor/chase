package engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/dibrinsofor/chase/src"
	executor "github.com/dibrinsofor/chase/src/exec"
	"github.com/dibrinsofor/chase/src/graph"
	"github.com/dibrinsofor/chase/src/state"
)

type KeyKind int

const (
	KeyParsed KeyKind = iota
	KeyMarshaled
	KeyTransformed
	KeyExecuted
)

type ComputeKey struct {
	Kind   KeyKind
	Target string
}

type ComputeResult struct {
	Value any
	Err   error
}

type TransformedPlan struct {
	Env *src.ChaseEnv
	DAG *graph.DAG
}

type ExecutionSummary struct {
	Warnings []error
}

type future struct {
	done   chan struct{}
	result ComputeResult
}

type Engine struct {
	mu       sync.Mutex
	cache    map[ComputeKey]*future
	filename string
	workers  int
}

func New(filename string, workers int) *Engine {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	return &Engine{
		cache:    make(map[ComputeKey]*future),
		filename: filename,
		workers:  workers,
	}
}

func (e *Engine) Compute(ctx context.Context, key ComputeKey) ComputeResult {
	e.mu.Lock()
	if f, ok := e.cache[key]; ok {
		e.mu.Unlock()
		<-f.done
		return f.result
	}

	f := &future{done: make(chan struct{})}
	e.cache[key] = f
	e.mu.Unlock()

	f.result = e.run(ctx, key)
	close(f.done)
	return f.result
}

func (e *Engine) run(ctx context.Context, key ComputeKey) ComputeResult {
	select {
	case <-ctx.Done():
		return ComputeResult{Err: ctx.Err()}
	default:
	}

	switch key.Kind {
	case KeyParsed:
		path := key.Target
		if path == "" {
			path = e.filename
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return ComputeResult{Err: fmt.Errorf("chase: error opening chasefile: %w", err)}
		}
		ast, err := src.ChasefileParser.ParseString(path, string(b))
		if err != nil {
			return ComputeResult{Err: fmt.Errorf("chase: error parsing chasefile: %w", err)}
		}
		return ComputeResult{Value: ast}

	case KeyMarshaled:
		parsed := e.Compute(ctx, ComputeKey{Kind: KeyParsed, Target: e.filename})
		if parsed.Err != nil {
			return ComputeResult{Err: parsed.Err}
		}
		ast, ok := parsed.Value.(*src.Chasefile)
		if !ok {
			return ComputeResult{Err: fmt.Errorf("invalid parsed value type: %T", parsed.Value)}
		}
		return ComputeResult{Value: src.Eval(ast)}

	case KeyTransformed:
		marshaled := e.Compute(ctx, ComputeKey{Kind: KeyMarshaled, Target: e.filename})
		if marshaled.Err != nil {
			return ComputeResult{Err: marshaled.Err}
		}
		env, ok := marshaled.Value.(*src.ChaseEnv)
		if !ok {
			return ComputeResult{Err: fmt.Errorf("invalid marshaled value type: %T", marshaled.Value)}
		}

		dag := executor.BuildDAG(env)
		if key.Target != "" && key.Target != e.filename {
			targetID := graph.NodeID(key.Target)
			if dag.GetNode(targetID) == nil {
				return ComputeResult{Err: fmt.Errorf("chase: target '%s' not found", key.Target)}
			}
			dag = dag.Subgraph(targetID)
		}

		return ComputeResult{Value: &TransformedPlan{Env: env, DAG: dag}}

	case KeyExecuted:
		transformed := e.Compute(ctx, ComputeKey{Kind: KeyTransformed, Target: key.Target})
		if transformed.Err != nil {
			return ComputeResult{Err: transformed.Err}
		}
		plan, ok := transformed.Value.(*TransformedPlan)
		if !ok {
			return ComputeResult{Err: fmt.Errorf("invalid transformed value type: %T", transformed.Value)}
		}

		if _, err := src.SetupEnv(plan.Env); err != nil {
			return ComputeResult{Err: fmt.Errorf("chase: error setting up shell: %w", err)}
		}

		summary := &ExecutionSummary{}
		cache, err := state.Load("")
		if err != nil {
			summary.Warnings = append(summary.Warnings, fmt.Errorf("failed to load state cache: %w", err))
			cache = state.NewBuildState()
		}

		ex := executor.New(plan.DAG, plan.Env, e.workers, cache)
		if err := ex.Run(ctx); err != nil {
			return ComputeResult{Err: fmt.Errorf("chase: error running commands: %w", err)}
		}

		if err := ex.SaveCache(); err != nil {
			summary.Warnings = append(summary.Warnings, fmt.Errorf("failed to save state cache: %w", err))
		}

		return ComputeResult{Value: summary}

	default:
		return ComputeResult{Err: fmt.Errorf("unknown key kind: %d", key.Kind)}
	}
}
