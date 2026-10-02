package textdiff

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func numbered(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestUnified(t *testing.T) {
	if got := Unified("a", "b", []byte("x\n"), []byte("x\n")); got != "" {
		t.Errorf("equal texts = %q", got)
	}
	got := Unified("a/f", "b/f", []byte("one\ntwo\nthree\n"), []byte("one\n2\nthree\n"))
	want := "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n one\n-two\n+2\n three\n"
	if got != want {
		t.Errorf("Unified = %q; want %q", got, want)
	}
	got = Unified("a", "b", nil, []byte("new\n"))
	if want := "--- a\n+++ b\n@@ -0,0 +1,1 @@\n+new\n"; got != want {
		t.Errorf("from empty = %q; want %q", got, want)
	}
	got = Unified("a", "b", []byte("x\ny"), []byte("x\nz"))
	if !strings.Contains(got, "-y\n\\ No newline at end of file\n+z\n\\ No newline at end of file\n") {
		t.Errorf("no final newline = %q", got)
	}
}

// TestMatchesDiff compares against diff -u where it is installed: two changes far apart make two
// hunks, two close together share one.
func TestMatchesDiff(t *testing.T) {
	bin, err := exec.LookPath("diff")
	if err != nil {
		t.Skip("diff is not installed")
	}
	a := numbered(1, 40)
	b := strings.Replace(strings.Replace(strings.Replace(a, "line 3\n", "line three\n", 1), "line 9\n", "", 1), "line 30\n", "line 30\nextra\n", 1)
	dir := t.TempDir()
	for name, text := range map[string]string{"a": a, "b": b} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "-u", "--label", "a", "--label", "b", "a", "b")
	cmd.Dir = dir
	want, _ := cmd.Output()
	if got := Unified("a", "b", []byte(a), []byte(b)); got != string(want) {
		t.Errorf("Unified:\n%s\ndiff -u:\n%s", got, want)
	}
}

func TestLargeMiddleIsReplaced(t *testing.T) {
	a, b := numbered(1, 2100), numbered(3000, 5100)
	got := Unified("a", "b", []byte(a), []byte(b))
	if !strings.HasPrefix(got, "--- a\n+++ b\n@@ -1,2100 +1,2101 @@\n-line 1\n") {
		t.Errorf("large diff starts %q", got[:min(len(got), 80)])
	}
}
