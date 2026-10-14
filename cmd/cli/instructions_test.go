package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude/CLAUDE.md"), "## A\n\na\n\n## B\n\nb\n")

	out, err := run(t, "instructions", "--root", home)
	if err != nil || !strings.Contains(out, "codex") || !strings.Contains(out, "no-file") || !strings.Contains(out, "antigravity") {
		t.Errorf("no AGENTS.md anywhere = %q, %v", out, err)
	}
	if _, err := run(t, "instructions", "--root", home, "--mark"); err == nil || !strings.Contains(err.Error(), "nothing to mark") {
		t.Errorf("--mark with no instruction file = %v", err)
	}

	writeFile(t, filepath.Join(home, ".gemini/config/AGENTS.md"), "condensed\n")
	out, err = run(t, "instructions", "--root", home, "--to", "antigravity")
	if err != nil || !strings.Contains(out, "unrecorded") || strings.Contains(out, "codex") {
		t.Errorf("unrecorded = %q, %v", out, err)
	}
	out, err = run(t, "instructions", "--root", home, "--to", "antigravity", "--mark")
	if err != nil || !strings.Contains(out, "marked reviewed against 2 sections") {
		t.Errorf("--mark = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini/config/.agentport/instructions.json")); err != nil {
		t.Errorf("--mark wrote no record: %v", err)
	}
	out, err = run(t, "instructions", "--root", home, "--to", "antigravity")
	if err != nil || !strings.Contains(out, "current") || !strings.Contains(out, "(2 sections)") {
		t.Errorf("current = %q, %v", out, err)
	}

	writeFile(t, filepath.Join(home, ".claude/CLAUDE.md"), "## A\n\na\n\n## B\n\nb edited\n\n## C\n\nc\n")
	out, err = run(t, "instructions", "--root", home, "--to", "antigravity", "-o", "json")
	var rows []instructionsJSON
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil {
		t.Fatalf("-o json = %q, %v", out, err)
	}
	if len(rows) != 1 || rows[0].State != "changed" || strings.Join(rows[0].Added, ",") != "C" || strings.Join(rows[0].Changed, ",") != "B" {
		t.Errorf("changed rows = %+v", rows)
	}
	out, err = run(t, "instructions", "--root", home, "--to", "antigravity")
	if err != nil || !strings.Contains(out, `added "C"; changed "B"`) {
		t.Errorf("changed text = %q, %v", out, err)
	}
	if body, _ := os.ReadFile(filepath.Join(home, ".gemini/config/AGENTS.md")); string(body) != "condensed\n" {
		t.Error("instructions touched AGENTS.md")
	}
}

func TestInstructionsStates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".gemini/config/AGENTS.md"), "condensed\n")
	out, err := run(t, "instructions", "--root", home, "--to", "antigravity")
	if err != nil || !strings.Contains(out, "no-hub") || !strings.Contains(out, "nothing to compare against") {
		t.Errorf("no CLAUDE.md = %q, %v", out, err)
	}
	out, err = run(t, "instructions", "--root", t.TempDir(), "--to", "codex", "-o", "json")
	if err != nil || !strings.Contains(out, `"state": "no-file"`) {
		t.Errorf("json no-file = %q, %v", out, err)
	}

	writeFile(t, filepath.Join(home, ".claude/CLAUDE.md"), "## A\n")
	writeFile(t, filepath.Join(home, ".gemini/config/.agentport/instructions.json"), "{")
	if _, err := run(t, "instructions", "--root", home, "--to", "antigravity"); err == nil {
		t.Error("a malformed record was accepted")
	}
	writeFile(t, filepath.Join(home, ".gemini/config/.agentport/instructions.json"), `{"version":1,"sections":{}}`)
	if err := os.Chmod(filepath.Join(home, ".gemini/config/.agentport"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, ".gemini/config/.agentport"), 0o755) })
	if _, err := run(t, "instructions", "--root", home, "--to", "antigravity", "--mark"); err == nil {
		t.Error("--mark into a read-only directory succeeded")
	}
	if _, err := run(t, "instructions", "--root", home, "--to", "claude"); err == nil {
		t.Error("the hub was accepted as a target")
	}
	if _, err := run(t, "instructions", "--root", home, "--config", filepath.Join(home, "missing.yaml")); err == nil {
		t.Error("a missing settings file was accepted")
	}
}

func TestInstructionsJSONEmpty(t *testing.T) {
	var b strings.Builder
	cmd := NewRootCmd()
	cmd.SetOut(&b)
	if err := writeInstructions(cmd, outputJSON, nil); err != nil || strings.TrimSpace(b.String()) != "[]" {
		t.Errorf("empty json = %q, %v", b.String(), err)
	}
}

func TestHomeDisplay(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := homeDisplay(home, ".codex"); got != filepath.Join("~", ".codex") {
		t.Errorf("homeDisplay = %q", got)
	}
	if got := homeDisplay("/r", ".codex"); got != filepath.Join("/r", ".codex") {
		t.Errorf("homeDisplay = %q", got)
	}
}
