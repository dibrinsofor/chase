package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dibrinsofor/chase/src"
	"github.com/dibrinsofor/chase/src/engine"
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
	eng := engine.New(filename, *j)

	if *l {
		res := eng.Compute(context.Background(), engine.ComputeKey{Kind: engine.KeyMarshaled, Target: filename})
		if res.Err != nil {
			panic(res.Err)
		}
		chaseIR, ok := res.Value.(*src.ChaseEnv)
		if !ok {
			panic(fmt.Errorf("chase: invalid marshaled value type: %T", res.Value))
		}
		src.ListDashes(chaseIR)
		return
	}

	if *lint {
		res := eng.Compute(context.Background(), engine.ComputeKey{Kind: engine.KeyMarshaled, Target: filename})
		if res.Err != nil {
			panic(res.Err)
		}
		chaseIR, ok := res.Value.(*src.ChaseEnv)
		if !ok {
			panic(fmt.Errorf("chase: invalid marshaled value type: %T", res.Value))
		}
		runLint(chaseIR)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	res := eng.Compute(ctx, engine.ComputeKey{Kind: engine.KeyExecuted, Target: *r})
	if res.Err != nil {
		panic(res.Err)
	}
	summary, ok := res.Value.(*engine.ExecutionSummary)
	if !ok {
		panic(fmt.Errorf("chase: invalid execution value type: %T", res.Value))
	}
	for _, w := range summary.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %v\n", w)
	}
}
