package crosswalk

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/paths"
)

func TestEveryRowCoversEveryHarness(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rows {
		if seen[r.Kind] {
			t.Errorf("duplicate kind %q", r.Kind)
		}
		seen[r.Kind] = true
		for _, h := range harness.All {
			if r.Terms[h] == "" {
				t.Errorf("row %q has no term for %s", r.Kind, h)
			}
		}
	}
}

func TestFilter(t *testing.T) {
	all, err := Filter("")
	if err != nil || len(all) != len(Rows) {
		t.Fatalf("Filter(\"\") = %d rows, %v", len(all), err)
	}
	one, err := Filter("agent")
	if err != nil || len(one) != 1 || one[0].Kind != "agent" {
		t.Fatalf("Filter(agent) = %v, %v", one, err)
	}
	if _, err := Filter("workflow"); err == nil {
		t.Error("Filter(workflow) succeeded; want an error")
	}
	if got := Kinds(); len(got) != len(Rows) || got[0] != Rows[0].Kind {
		t.Errorf("Kinds() = %v", got)
	}
}

// TestLocationsMatchPathsTable keeps the display text from drifting away from internal/paths, the
// only source of locations.
func TestLocationsMatchPathsTable(t *testing.T) {
	for _, r := range Rows {
		kind, err := ir.ParseKind(r.Kind)
		if err != nil {
			continue
		}
		for _, h := range harness.All {
			l, err := paths.For(h)
			if err != nil {
				t.Fatal(err)
			}
			p, err := l.Path(paths.ScopeProject, kind, "<name>")
			if errors.Is(err, paths.ErrUnsupported) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(r.Terms[h], filepath.ToSlash(p)) {
				t.Errorf("%s/%s: %q does not mention %q", r.Kind, h, r.Terms[h], filepath.ToSlash(p))
			}
		}
	}
}
