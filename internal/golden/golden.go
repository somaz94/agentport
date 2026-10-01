// Package golden compares test output with checked-in golden files.
package golden

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// UpdateEnv rewrites golden files instead of comparing when set to 1. An environment variable
// rather than a -update flag, because `go test ./... -update` fails in every package that does
// not define the flag.
const UpdateEnv = "AGENTPORT_UPDATE_GOLDEN"

// Assert fails t when got differs from the golden file at path.
func Assert(t testing.TB, path string, got []byte) {
	t.Helper()
	if os.Getenv(UpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with %s=1 to create it): %v", path, UpdateEnv, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch (run with %s=1 to update)\n--- got\n%s\n--- want\n%s", path, UpdateEnv, got, want)
	}
}
