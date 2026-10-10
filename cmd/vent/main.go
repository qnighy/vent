// Command vent is the bootstrap interpreter for the vent language.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/qnighy/vent/internal/interp"
)

const usage = `usage: vent <command> [arguments]

commands:
  run <file>    run a vent program
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		if len(args) != 2 {
			fmt.Fprint(stderr, usage)
			return 2
		}
		return runFile(args[1], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "vent: unknown command %q\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}

func runFile(path string, stdout, stderr io.Writer) int {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "vent: %v\n", err)
		return 1
	}
	if err := interp.Run(path, src, stdout); err != nil {
		fmt.Fprintf(stderr, "vent: %v\n", err)
		return 1
	}
	return 0
}
