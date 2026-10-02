package golden

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// recorder captures failures instead of failing the real test.
type recorder struct {
	testing.TB
	failures []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestAssertCompares(t *testing.T) {
	// make golden sets the switch for every package, this one included.
	t.Setenv(UpdateEnv, "")
	path := filepath.Join(t.TempDir(), "case.golden")
	must(t, os.WriteFile(path, []byte("want\n"), 0o644))

	r := &recorder{TB: t}
	Assert(r, path, []byte("want\n"))
	if len(r.failures) != 0 {
		t.Errorf("matching output failed: %v", r.failures)
	}
	Assert(r, path, []byte("other\n"))
	if len(r.failures) != 1 {
		t.Errorf("mismatch recorded %d failures; want 1", len(r.failures))
	}

	missing := &recorder{TB: t}
	Assert(missing, filepath.Join(t.TempDir(), "absent.golden"), []byte("x"))
	if len(missing.failures) == 0 {
		t.Error("a missing golden file did not fail")
	}
}

func TestAssertUpdates(t *testing.T) {
	t.Setenv(UpdateEnv, "1")
	path := filepath.Join(t.TempDir(), "nested", "case.golden")
	r := &recorder{TB: t}
	Assert(r, path, []byte("fresh\n"))
	if got, err := os.ReadFile(path); err != nil || string(got) != "fresh\n" || len(r.failures) != 0 {
		t.Errorf("update wrote %q, %v, failures %v", got, err, r.failures)
	}

	file := filepath.Join(t.TempDir(), "file")
	must(t, os.WriteFile(file, nil, 0o644))
	blocked := &recorder{TB: t}
	Assert(blocked, filepath.Join(file, "sub", "case.golden"), []byte("x"))
	if len(blocked.failures) == 0 {
		t.Error("an unwritable golden path did not fail")
	}
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "isdir.golden"), 0o755))
	isDir := &recorder{TB: t}
	Assert(isDir, filepath.Join(dir, "isdir.golden"), []byte("x"))
	if len(isDir.failures) == 0 {
		t.Error("writing a golden over a directory did not fail")
	}
}
