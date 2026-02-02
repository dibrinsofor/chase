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

func runLint(env *src.ChaseEnv) {
	cache, err := state.Load("")
	if err != nil {
		fmt.Println("No state cache found. Run a build first to trace dependencies.")
		return
	}

	hasIssues := false

	for _, dash := range env.Dashes() {
		ts := cache.GetTarget(dash.Name())
		if ts == nil {
			fmt.Printf("%s:\n  ⚠ no traced data (run build first)\n\n", dash.Name())
			continue
		}

		declaredInputs := toSet(dash.Inputs())
		declaredOutputs := toSet(dash.Outputs())
		tracedInputs := toSet(ts.TracedInputs)
		tracedOutputs := toSet(ts.TracedOutputs)

		undeclaredInputs := diff(tracedInputs, declaredInputs)
		undeclaredOutputs := diff(tracedOutputs, declaredOutputs)

		if len(undeclaredInputs) == 0 && len(undeclaredOutputs) == 0 {
			fmt.Printf("%s:\n  ✓ all dependencies declared\n\n", dash.Name())
			continue
		}

		hasIssues = true
		fmt.Printf("%s:\n", dash.Name())

		if len(undeclaredInputs) > 0 {
			fmt.Println("  ⚠ undeclared inputs:")
			for _, p := range undeclaredInputs {
				fmt.Printf("    + %s\n", p)
			}
		}

		if len(undeclaredOutputs) > 0 {
			fmt.Println("  ⚠ undeclared outputs:")
			for _, p := range undeclaredOutputs {
				fmt.Printf("    + %s\n", p)
			}
		}

		fmt.Println()
		fmt.Println("  Suggested additions:")
		if len(undeclaredInputs) > 0 {
			fmt.Printf("    inputs: %v\n", undeclaredInputs)
		}
		if len(undeclaredOutputs) > 0 {
			fmt.Printf("    outputs: %v\n", undeclaredOutputs)
		}
		fmt.Println()
	}

	if hasIssues {
		os.Exit(1)
	}
}

func toSet(slice []string) map[string]bool {
	set := make(map[string]bool)
	for _, s := range slice {
		set[s] = true
	}
	return set
}

func diff(a, b map[string]bool) []string {
	var result []string
	for k := range a {
		if !b[k] {
			result = append(result, k)
		}
	}
	return result
}

func main() {
	// flags -l (list all sprints), -{sprint name}
	l := flag.Bool("l", false, "list all dashes in the chasefile")
	r := flag.String("r", "", "run specific dash")
	j := flag.Int("j", 0, "number of parallel workers (default: number of CPUs)")
	lint := flag.Bool("lint", false, "compare traced deps vs declared deps")

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

	if *l {
		src.ListDashes(chaseIR)
		return
	}

	if *lint {
		runLint(chaseIR)
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
