package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dibrinsofor/chase/src"
)

// todo: add file watcher to detect chasefile in root directory and generate dependency graph

var filename = "Chasefile"

func main() {
	// flags -l (list all sprints), -{sprint name}
	l := flag.Bool("l", false, "list all dashes in the chasefile")
	// should we want to run more than 1 dash?
	r := flag.String("r", "", "run specific dash")

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

	if *r != "" {
		err := src.ExecDash(chaseIR, r)
		if err != nil {
			panic(fmt.Errorf("chase: error running commands: %w", err))
		}
	}

	// run all dashes
	src.ExecAllDashes(chaseIR)

}
