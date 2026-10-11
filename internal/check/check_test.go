package check

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files with actual output")

// TestGolden checks each testdata/*.vent file and compares the reported
// errors with the sibling .out file.
//
// Regenerate expectations with: go test ./internal/check -run TestGolden -update
func TestGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.vent"))
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
			var sb strings.Builder
			for _, err := range CheckFile(name+".vent", src) {
				sb.WriteString(err.Error() + "\n")
			}
			got := sb.String()

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
