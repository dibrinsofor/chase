package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/exec"
	"github.com/dibrinsofor/chase/src/graph"
	"github.com/dibrinsofor/chase/src/state"
)

// todo: add file watcher to detect chasefile in root directory and generate dependency graph

var filename = "Chasefile"

func main() {
	// flags -l (list all sprints), -{sprint name}
	l := flag.Bool("l", false, "list all dashes in the chasefile")
	r := flag.String("r", "", "run specific dash")
	j := flag.Int("j", 0, "number of parallel workers (default: number of CPUs)")

	flag.Parse()

	// check if chasefile exists
	info, err := os.Stat(filename)
	if os.IsNotExist(err) || info.IsDir() || err != nil {
		panic(fmt.Errorf("chase: error opening chasefile: %w", err))
	}

	b, err := os.ReadFile(filename)
	if err != nil {
		panic(fmt.Errorf("chase: error opening chasefile: %w", err))
	}

	ast, err := src.ChasefileParser.ParseString(filename, string(b))
	if err != nil {
		panic(fmt.Errorf("chase: error parsing chasefile: %w", err))
	}

	// todo: rename
	chaseIR := src.Eval(ast)

	// check flags and run commands
	if *l {
		src.ListDashes(chaseIR)
		return
	}

	_, err = src.SetupEnv(chaseIR)
	if err != nil {
		panic(fmt.Errorf("chase: error setting up shell: %w", err))
	}

	dag := executor.BuildDAG(chaseIR)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	var targetDAG *graph.DAG
	if *r != "" {
		targetID := graph.NodeID(*r)
		if dag.GetNode(targetID) == nil {
			panic(fmt.Errorf("chase: target '%s' not found", *r))
		}
		targetDAG = dag.Subgraph(targetID)
	} else {
		targetDAG = dag
	}

	cache, err := state.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load state cache: %v\n", err)
		cache = state.NewBuildState()
	}

	ex := executor.New(targetDAG, chaseIR, *j, cache)
	if err := ex.Run(ctx); err != nil {
		panic(fmt.Errorf("chase: error running commands: %w", err))
	}

	if err := ex.SaveCache(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save state cache: %v\n", err)
	}

}
