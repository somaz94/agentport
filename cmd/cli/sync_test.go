package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syncHome builds a home directory with a small hub and set-up Antigravity and Codex directories,
// and points the settings file at an empty directory.
func syncHome(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "skills", "alpha", "SKILL.md"), "---\nname: alpha\ndescription: Alpha\n---\nDo alpha.\n")
	writeFile(t, filepath.Join(home, ".claude", "commands", "deploy.md"), "---\ndescription: Deploy\nallowed-tools: Bash\n---\nDeploy $ARGUMENTS.\n")
	writeFile(t, filepath.Join(home, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews\ntools: Read, Grep\n---\nReview.\n")
	for _, d := range []string{filepath.Join(".gemini", "config"), ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestSyncPreviewApplyCheck(t *testing.T) {
	home := syncHome(t)
	out, err := run(t, "sync", "--root", home)
	if err != nil || !strings.Contains(out, "antigravity: 3 new") || !strings.Contains(out, "codex: 3 new") ||
		!strings.Contains(out, "new  .gemini/config/skills/deploy") || !strings.Contains(out, "dry run") {
		t.Fatalf("preview = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "skills")); !os.IsNotExist(err) {
		t.Fatal("the preview wrote files")
	}
	if out, err := run(t, "sync", "--root", home, "--apply"); err != nil || strings.Contains(out, "dry run") {
		t.Fatalf("apply = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "agents", "reviewer.md")); err != nil {
		t.Fatal("apply did not write the agent")
	}
	if out, err := run(t, "sync", "--root", home, "--check"); err != nil || !strings.Contains(out, "antigravity: 3 unchanged") {
		t.Fatalf("check after apply = %q, %v", out, err)
	}

	writeFile(t, filepath.Join(home, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews code\ntools: Read\n---\nReview.\n")
	var stdout, stderr bytes.Buffer
	if code := runMain([]string{"sync", "--root", home, "--check"}, &stdout, &stderr); code != exitOutOfSync {
		t.Errorf("check with a changed hub exit = %d; want %d", code, exitOutOfSync)
	}
	if !strings.Contains(stdout.String(), "update  .gemini/config/agents/reviewer.md") {
		t.Errorf("check output = %q", stdout.String())
	}
}

func TestSyncProblems(t *testing.T) {
	home := syncHome(t)
	if _, err := run(t, "sync", "--root", home, "--apply", "--to", "antigravity"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".gemini", "config", "agents", "reviewer.md"), "edited\n")
	writeFile(t, filepath.Join(home, ".gemini", "config", "skills", "mine", "SKILL.md"), "---\nname: mine\ndescription: mine\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "commands", "alpha.md"), "---\ndescription: shadowed\n---\nx\n")
	out, err := run(t, "status", "--root", home, "--to", "antigravity")
	for _, want := range []string{
		"drift      .gemini/config/agents/reviewer.md  edited since agentport wrote it",
		"skipped    .claude/commands/alpha.md",
		"unmanaged  .gemini/config/skills/mine",
		"unchanged  .gemini/config/skills/alpha",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if err != nil {
		t.Error(err)
	}
	if out, _ := run(t, "sync", "--root", home, "--to", "antigravity"); strings.Contains(out, "  unchanged") || strings.Contains(out, "  unmanaged") {
		t.Errorf("sync lists unchanged or unmanaged units:\n%s", out)
	}
	if _, err := run(t, "sync", "--root", home, "--to", "antigravity", "--apply", "--force"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".gemini", "config", "agents", "reviewer.md")); string(data) == "edited\n" {
		t.Error("--force did not replace the edited agent")
	}
}

func TestSyncJSON(t *testing.T) {
	home := syncHome(t)
	out, err := run(t, "sync", "--root", home, "--to", "codex", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var got planJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v in %q", err, out)
	}
	if got.Applied || len(got.Targets) != 1 || got.Targets[0].Counts["new"] != 3 || len(got.Targets[0].Units) != 3 {
		t.Errorf("json = %+v", got)
	}
	for _, u := range got.Targets[0].Units {
		if u.Report == nil || len(u.Files) == 0 || u.Source == "" {
			t.Errorf("unit %+v lacks its report, files or source", u)
		}
	}
}

func TestSyncStrictWritesNothing(t *testing.T) {
	home := syncHome(t)
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"sync", "--root", home, "--apply", "--strict"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "lossy") {
		t.Errorf("--strict exit = %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "skills")); !os.IsNotExist(err) {
		t.Error("--strict wrote files")
	}
}

func TestSyncErrors(t *testing.T) {
	home := syncHome(t)
	for name, args := range map[string][]string{
		"check and apply": {"sync", "--root", home, "--check", "--apply"},
		"hub as target":   {"sync", "--root", home, "--to", "claude"},
		"unknown target":  {"sync", "--root", home, "--to", "cursor"},
		"no hub":          {"sync", "--root", t.TempDir()},
		"missing config":  {"sync", "--root", home, "--config", filepath.Join(home, "absent.yaml")},
		"status no hub":   {"status", "--root", t.TempDir()},
		"status config":   {"status", "--root", home, "--config", filepath.Join(home, "absent.yaml")},
	} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := os.RemoveAll(filepath.Join(home, ".codex")); err != nil {
		t.Fatal(err)
	}
	// Without --to or configured targets, a harness that is not set up is simply not a target.
	if out, err := run(t, "sync", "--root", home); err != nil || strings.Contains(out, "codex") {
		t.Errorf("sync with Codex missing = %q, %v", out, err)
	}
	out, err := run(t, "sync", "--root", home, "--apply", "--to", "antigravity,codex")
	if err == nil || !strings.Contains(out, "codex: Codex is not set up") {
		t.Errorf("sync --to codex with Codex missing = %q, %v", out, err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".gemini", "config", "skills", "alpha", "SKILL.md")); statErr != nil {
		t.Error("one target failing stopped the others")
	}
	if err := os.RemoveAll(filepath.Join(home, ".gemini")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "sync", "--root", home); err == nil || !strings.Contains(err.Error(), "no target harness is set up") {
		t.Errorf("sync with nothing set up: %v", err)
	}
}

func TestSyncConfigFile(t *testing.T) {
	home := syncHome(t)
	writeFile(t, filepath.Join(home, ".claude", "agents-ko", "reviewer.md"), "---\nname: reviewer\ndescription: 리뷰\n---\n리뷰.\n")
	cfg := filepath.Join(home, "agentport.yaml")
	writeFile(t, cfg, "targets: [antigravity]\npairs: ['-ko']\nskip: ['commands/*']\nmodelInvocableCommands: true\n")
	out, err := run(t, "sync", "--root", home, "--config", cfg, "--apply")
	if err != nil || strings.Contains(out, "codex") {
		t.Fatalf("sync = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "agents-ko", "reviewer.md")); err != nil {
		t.Error("the mirror agent was not synced")
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "skills", "deploy")); !os.IsNotExist(err) {
		t.Error("a skipped command was synced")
	}
}

func TestUnitState(t *testing.T) {
	if got := summarize(map[string]int{}); got != "nothing to sync" {
		t.Errorf("summarize(empty) = %q", got)
	}
}

func TestDoctor(t *testing.T) {
	home := syncHome(t)
	if _, err := run(t, "sync", "--root", home, "--apply"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "doctor", "--root", home)
	if err != nil || !strings.Contains(out, "antigravity manifest") || !strings.Contains(out, "3 files recorded") {
		t.Errorf("doctor = %q, %v", out, err)
	}
	out, err = run(t, "doctor", "--root", home, "-o", "json")
	var findings []map[string]string
	if err != nil || json.Unmarshal([]byte(out), &findings) != nil || len(findings) == 0 {
		t.Errorf("doctor -o json = %q, %v", out, err)
	}
	writeFile(t, filepath.Join(home, ".codex", ".agentport", "manifest.json"), "{")
	var stdout, stderr bytes.Buffer
	if code := runMain([]string{"doctor", "--root", home}, &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), "error") {
		t.Errorf("doctor with a broken manifest exit = %d, out %q", code, stdout.String())
	}
	if _, err := run(t, "doctor", "--scope", "global"); err == nil {
		t.Error("doctor accepted an unknown scope")
	}
	if out, err := run(t, "doctor", "--scope", "project", "--root", home); err != nil || strings.Contains(out, "manifest") {
		t.Errorf("doctor --scope project = %q, %v", out, err)
	}
}

func TestStatusMixedUnitAndWarnings(t *testing.T) {
	home := syncHome(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "alpha", "notes.md"), "v1\n")
	if _, err := run(t, "sync", "--root", home, "--apply", "--to", "antigravity"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude", "skills", "alpha", "notes.md"), "v2\n")
	writeFile(t, filepath.Join(home, ".gemini", "config", "skills", "alpha", "SKILL.md"), "edited\n")
	// An entry outside the target's directories is reported, never acted on.
	data, _ := os.ReadFile(filepath.Join(home, ".gemini", "config", ".agentport", "manifest.json"))
	writeFile(t, filepath.Join(home, ".gemini", "config", ".agentport", "manifest.json"),
		strings.Replace(string(data), `"entries": {`, `"entries": {".gemini/config/AGENTS.md": {"source": "x", "sourceHash": "", "outputHash": "", "mode": "0644", "generator": ""},`, 1))
	out, err := run(t, "status", "--root", home, "--to", "antigravity")
	for _, want := range []string{
		"update     .gemini/config/skills/alpha",
		"drift      .gemini/config/skills/alpha/SKILL.md",
		"warning: antigravity: manifest entry .gemini/config/AGENTS.md is outside",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if err != nil {
		t.Error(err)
	}
}
