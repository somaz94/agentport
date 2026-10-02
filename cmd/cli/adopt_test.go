package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/reconcile"
)

func TestAdoptPreviewAndApply(t *testing.T) {
	home := syncHome(t)
	if _, err := run(t, "sync", "--root", home, "--apply", "--to", "antigravity"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".gemini", "config", "skills", "alpha", "SKILL.md")
	data, _ := os.ReadFile(target)
	writeFile(t, target, strings.Replace(string(data), "Do alpha.", "Do alpha well.", 1))

	out, err := run(t, "adopt", target, "--root", home)
	if err != nil || !strings.Contains(out, "adopt .gemini/config/skills/alpha (antigravity) into .claude/skills/alpha") ||
		!strings.Contains(out, "-Do alpha.\n+Do alpha well.\n") || !strings.Contains(out, "dry run") {
		t.Fatalf("preview = %q, %v", out, err)
	}
	if hub, _ := os.ReadFile(filepath.Join(home, ".claude", "skills", "alpha", "SKILL.md")); strings.Contains(string(hub), "well") {
		t.Fatal("the preview wrote the hub")
	}
	out, err = run(t, "adopt", target, "--root", home, "--apply")
	if err != nil || !strings.Contains(out, "now matches what the hub converts to") {
		t.Fatalf("apply = %q, %v", out, err)
	}
	if out, err := run(t, "sync", "--root", home, "--to", "antigravity", "--check"); err != nil {
		t.Errorf("sync --check after adopting = %q, %v", out, err)
	}
	out, err = run(t, "adopt", target, "--root", home)
	if err != nil || !strings.Contains(out, "nothing to adopt") {
		t.Errorf("adopting again = %q, %v", out, err)
	}
}

func TestAdoptJSONAndNotes(t *testing.T) {
	home := syncHome(t)
	if _, err := run(t, "sync", "--root", home, "--apply", "--to", "antigravity"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".gemini", "config", "agents", "reviewer.md")
	data, _ := os.ReadFile(target)
	edited := strings.Replace(string(data), "Review.", "Review twice.", 1)
	edited = strings.Replace(edited, "mainAgent: false", "mainAgent: true", 1)
	writeFile(t, target, edited)

	out, err := run(t, "adopt", target, "--root", home, "-o", "json", "--apply")
	if err != nil {
		t.Fatal(err)
	}
	var got adoptionJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v in %q", err, out)
	}
	if !got.Applied || got.InSync || len(got.Changes) != 1 || got.Changes[0].Path != ".claude/agents/reviewer.md" ||
		len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "mainAgent") {
		t.Errorf("json = %+v", got)
	}
	out, err = run(t, "adopt", target, "--root", home)
	if err != nil || !strings.Contains(out, "not adopted:\n  mainAgent") {
		t.Errorf("text with notes = %q, %v", out, err)
	}
}

func TestAdoptErrors(t *testing.T) {
	home := syncHome(t)
	if _, err := run(t, "sync", "--root", home, "--apply", "--to", "antigravity"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".gemini", "config", "agents", "reviewer.md")
	data, _ := os.ReadFile(target)
	writeFile(t, target, strings.Replace(string(data), "Review.", "Review twice.", 1))
	writeFile(t, filepath.Join(home, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews more\ntools: Read, Grep\n---\nReview.\n")
	if _, err := run(t, "adopt", target, "--root", home); err == nil || !strings.Contains(err.Error(), "changes made to .claude/agents/reviewer.md since") {
		t.Errorf("adopt over a changed hub: %v", err)
	}
	// --force adopts the target as it is, undoing the hub's own change; the printed diff shows that.
	if out, err := run(t, "adopt", target, "--root", home, "--force", "--apply"); err != nil ||
		!strings.Contains(out, "-description: Reviews more\n+description: 'Reviews'\n") || !strings.Contains(out, "now matches") {
		t.Errorf("--force = %q, %v", out, err)
	}
	for name, args := range map[string][]string{
		"no argument": {"adopt", "--root", home},
		"no --to":     {"adopt", target, "--root", home, "--to", "codex"},
		"unmanaged":   {"adopt", filepath.Join(home, ".gemini", "config"), "--root", home},
		"bad config":  {"adopt", target, "--root", home, "--config", filepath.Join(home, "absent.yaml")},
	} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestChangeDiff(t *testing.T) {
	if got := changeDiff(reconcileChange("a.sh", "x", "x", 0o755)); got != "mode of a.sh becomes 0755\n" {
		t.Errorf("mode-only change = %q", got)
	}
	if got := changeDiff(reconcileChange("a.bin", "\xff", "\xfe", 0o644)); got != "binary file a.bin differs\n" {
		t.Errorf("binary change = %q", got)
	}
	c := reconcileChange("new.md", "", "hi\n", 0o644)
	c.Old = nil
	if got := changeDiff(c); !strings.HasPrefix(got, "--- /dev/null\n+++ new.md\n") {
		t.Errorf("new file = %q", got)
	}
}

func reconcileChange(path, old, new string, mode os.FileMode) reconcile.Change {
	return reconcile.Change{Path: path, Old: []byte(old), New: []byte(new), Mode: mode}
}
