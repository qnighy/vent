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
// Regenerate expectations with: go test ./internal/interp -run TestGolden -update
func TestGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "run", "*.vent"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".vent")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := Run(name+".vent", src, &out); err != nil {
				out.WriteString("error: " + err.Error() + "\n")
			}

			golden := strings.TrimSuffix(file, ".vent") + ".out"
			if *update {
				if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file (run with -update to create): %v", err)
			}
			if diff := cmp.Diff(string(want), out.String()); diff != "" {
				t.Errorf("output mismatch for %s (-want +got):\n%s", file, diff)
			}
		})
	}
}
