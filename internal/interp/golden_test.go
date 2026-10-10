package interp

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files with actual output")

// TestGolden runs each testdata/run/*.vent program and compares its output
// with the sibling .out file. A runtime error is appended to the output as
// "error: <message>" so that failing programs can be tested too.
//
// It also runs the tests in each testdata/test/*.vent file and compares the
// report with the sibling .out file. Files named verbose_* run verbosely.
//
// Regenerate expectations with: go test ./internal/interp -run TestGolden -update
func TestGolden(t *testing.T) {
	forEachGolden(t, "run", func(name string, src []byte) string {
		var out bytes.Buffer
		if err := Run(name+".vent", src, &out); err != nil {
			out.WriteString("error: " + err.Error() + "\n")
		}
		return out.String()
	})
	forEachGolden(t, "test", func(name string, src []byte) string {
		var out bytes.Buffer
		ok := RunTests(name+".vent", src, &out, strings.HasPrefix(name, "verbose_"))
		if !ok {
			out.WriteString("(failed)\n")
		}
		return out.String()
	})
}

func forEachGolden(t *testing.T, dir string, run func(name string, src []byte) string) {
	files, err := filepath.Glob(filepath.Join("testdata", dir, "*.vent"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".vent")
		t.Run(dir+"/"+name, func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			got := run(name, src)

			golden := strings.TrimSuffix(file, ".vent") + ".out"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file (run with -update to create): %v", err)
			}
			if diff := cmp.Diff(string(want), got); diff != "" {
				t.Errorf("output mismatch for %s (-want +got):\n%s", file, diff)
			}
		})
	}
}
