// Command vent is the bootstrap interpreter for the vent language.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/qnighy/vent/internal/check"
	"github.com/qnighy/vent/internal/interp"
)

const usage = `usage: vent <command> [arguments]

commands:
  run <file>                  run a vent program; static errors abort only
                              when the code containing them is executed
  check <file>...             report the static errors in each file
  test [-v] <file|dir>...     run the tests (functions named test_*) in each
                              file; directories are searched for *.vent files
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
	case "check":
		if len(args) < 2 {
			fmt.Fprint(stderr, usage)
			return 2
		}
		return checkFiles(args[1:], stdout, stderr)
	case "test":
		return testPaths(args[1:], stdout, stderr)
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

func testPaths(args []string, stdout, stderr io.Writer) int {
	verbose := false
	if len(args) > 0 && args[0] == "-v" {
		verbose = true
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var files []string
	for _, path := range args {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(stderr, "vent: %v\n", err)
			return 1
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".vent") {
				files = append(files, p)
			}
			return err
		})
		if err != nil {
			fmt.Fprintf(stderr, "vent: %v\n", err)
			return 1
		}
	}

	status := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(stderr, "vent: %v\n", err)
			status = 1
			continue
		}
		if !interp.RunTests(file, src, stdout, verbose) {
			status = 1
		}
	}
	return status
}

func checkFiles(paths []string, stdout, stderr io.Writer) int {
	status := 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "vent: %v\n", err)
			status = 1
			continue
		}
		for _, err := range check.CheckFile(path, src) {
			fmt.Fprintln(stdout, err)
			status = 1
		}
	}
	return status
}
