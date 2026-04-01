package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "lint-mutation":
		runLintMutation(os.Args[2:])
	case "fmt-sql":
		runFmtSQL(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `usage: go-spantool <command> [flags] [args...]

commands:
  lint-mutation  Validate spanner mutation map literals against DDL
  fmt-sql        Format SQL in spanner.Statement literals`)
}
