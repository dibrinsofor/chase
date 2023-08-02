package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/alecthomas/repr"

	"github.com/dibrinsofor/chase/src"
)

// todo: add file watcher to detect chasefile in root directory and generate dependency graph

var filename = "Chasefile"

type env struct {
	shell []string
	vars  map[string]string
}

func main() {
	// e := env{}

	// check if chasefile exists
	info, err := os.Stat(filename)
	if os.IsNotExist(err) || info.IsDir() || err != nil {
		panic(fmt.Errorf("chase: error opening chasefile: %w", err))
	}

	b, err := os.ReadFile(filename)
	if err != nil {
		panic(fmt.Errorf("chase: error opening chasefile: %w", err))
	}

	// do we want to do any linting or add. checks before attempting to parse?
	ast, err := src.ChasefileParser.ParseBytes(filename, b)
	if err != nil {
		panic(fmt.Errorf("chase: error parsing chasefile: %w", err))
	}

	repr.Println(ast)

	// if no "set shell" then try sh
	if runtime.GOOS == "windows" {
		// redirect to path for windows
	}
}
