package interp

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// testPrefix marks test functions. This naming convention is a stopgap until
// the language has a proper way to declare tests.
const testPrefix = "test_"

// RunTests runs the tests in src and writes a report to out. It reports
// whether the file loaded and all of its tests passed.
//
// A test is a top-level function without parameters whose name starts with
// "test_". Tests run in source order. An abort fails only the running test.
// The output of a test is captured and reported if the test fails, or always
// if verbose is set.
func RunTests(filename string, src []byte, out io.Writer, verbose bool) bool {
	var captured bytes.Buffer
	in, err := load(src, &captured)
	if err != nil {
		fmt.Fprintf(out, "FAIL %s\n", filename)
		writeIndented(out, withFilename(filename, err).Error())
		return false
	}

	passed, failed := 0, 0
	for _, decl := range in.prog.Fns {
		if !strings.HasPrefix(decl.Name, testPrefix) {
			continue
		}
		captured.Reset()
		var err error
		if len(decl.Params) != 0 {
			err = errorf(decl.Pos, "test function must not take parameters")
		} else {
			_, err = in.callFunc(decl.Pos, &FuncValue{Decl: decl}, nil)
		}
		if err == nil {
			passed++
			if verbose {
				fmt.Fprintf(out, "--- PASS: %s\n", decl.Name)
				writeIndented(out, captured.String())
			}
			continue
		}
		failed++
		fmt.Fprintf(out, "--- FAIL: %s\n", decl.Name)
		writeIndented(out, captured.String())
		writeIndented(out, withFilename(filename, err).Error())
	}

	switch {
	case failed > 0:
		fmt.Fprintf(out, "FAIL %s (%d failed, %d passed)\n", filename, failed, passed)
	case passed == 0:
		fmt.Fprintf(out, "ok   %s (no tests)\n", filename)
	default:
		fmt.Fprintf(out, "ok   %s (%d passed)\n", filename, passed)
	}
	return failed == 0
}

// writeIndented writes each line of s indented by four spaces.
func writeIndented(out io.Writer, s string) {
	if s == "" {
		return
	}
	for _, line := range strings.SplitAfter(strings.TrimSuffix(s, "\n"), "\n") {
		fmt.Fprintf(out, "    %s", line)
	}
	fmt.Fprintln(out)
}
