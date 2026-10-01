package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var fixture = filepath.Join("..", "..", "internal", "convert", "testdata", "skills")

// copyTree copies a fixture directory, keeping file modes.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConvertPreview(t *testing.T) {
	src := filepath.Join(fixture, "claude-basic", "skill")
	out, err := run(t, "convert", src, "--from", "claude", "--to", "antigravity")
	if err != nil || !strings.Contains(out, "Claude Code skill demo-skill -> Antigravity skill demo-skill") ||
		!strings.Contains(out, "scripts/run.sh (0755)") || strings.Contains(out, "==> SKILL.md") {
		t.Errorf("preview = %q, %v", out, err)
	}
	out, err = run(t, "convert", filepath.Join(src, "SKILL.md"), "--from", "claude", "--to", "codex", "--print")
	if err != nil || !strings.Contains(out, "==> SKILL.md <==") || !strings.Contains(out, "invoked as `$demo-skill") {
		t.Errorf("--print = %q, %v", out, err)
	}
	out, err = run(t, "convert", src, "--from", "claude", "--to", "codex", "-o", "json", "--print")
	var decoded struct {
		Report struct{ Entries []map[string]string }
		Files  []struct{ Path, Mode, Content string }
	}
	if err != nil || json.Unmarshal([]byte(out), &decoded) != nil || len(decoded.Files) != 3 || decoded.Files[0].Content == "" {
		t.Errorf("json = %q, %v", out, err)
	}
	out, _ = run(t, "convert", src, "--from", "claude", "--to", "codex", "-o", "json")
	if strings.Contains(out, `"content"`) {
		t.Error("json without --print includes file contents")
	}
}

func TestConvertOut(t *testing.T) {
	src := filepath.Join(fixture, "claude-basic", "skill")
	out := t.TempDir()
	if _, err := run(t, "convert", src, "--from", "claude", "--to", "antigravity", "--out", out); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(out, "demo-skill", "scripts", "run.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("written helper: %v, %v; want mode 0755", info, err)
	}
	if _, err := run(t, "convert", src, "--from", "claude", "--to", "antigravity", "--out", out); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("rewriting without --force: %v", err)
	}
	if _, err := run(t, "convert", src, "--from", "claude", "--to", "antigravity", "--out", out, "--force"); err != nil {
		t.Errorf("--force: %v", err)
	}
}

func TestConvertDetectsSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	skill := filepath.Join(home, ".claude", "skills", "demo-skill")
	copyTree(t, filepath.Join(fixture, "claude-basic", "skill"), skill)
	if out, err := run(t, "convert", skill, "--to", "codex"); err != nil || !strings.Contains(out, "Claude Code skill demo-skill") {
		t.Errorf("auto-detected conversion = %q, %v", out, err)
	}
	shared := filepath.Join(t.TempDir(), "repo", ".agents", "skills", "x")
	writeFile(t, filepath.Join(shared, "SKILL.md"), "---\nname: x\ndescription: d\n---\nbody\n")
	if _, err := run(t, "convert", shared, "--to", "claude"); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Errorf("ambiguous source error = %v; want a hint to pass --from", err)
	}
}

func TestConvertErrors(t *testing.T) {
	src := filepath.Join(fixture, "claude-basic", "skill")
	cases := map[string][]string{
		"missing --to":   {"convert", src, "--from", "claude"},
		"unknown --to":   {"convert", src, "--from", "claude", "--to", "cursor"},
		"unknown --from": {"convert", src, "--from", "cursor", "--to", "codex"},
		"no skill":       {"convert", t.TempDir(), "--from", "claude", "--to", "codex"},
		"no argument":    {"convert", "--to", "codex"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := run(t, args...); err == nil {
				t.Error("succeeded; want an error")
			}
		})
	}
	long := filepath.Join(t.TempDir(), "long")
	writeFile(t, filepath.Join(long, "SKILL.md"), "---\nname: "+strings.Repeat("a", 65)+"\ndescription: d\n---\n")
	if _, err := run(t, "convert", long, "--from", "claude", "--to", "codex"); err == nil {
		t.Error("a name Codex cannot load converted without an error")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocked, "")
	if _, err := run(t, "convert", src, "--from", "claude", "--to", "codex", "--out", filepath.Join(blocked, "sub")); err == nil {
		t.Error("--out under a regular file succeeded")
	}
}

// home lays out one item of every scanned kind for every harness, plus things scan must skip.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	copyTree(t, filepath.Join(fixture, "claude-basic", "skill"), filepath.Join(h, ".claude", "skills", "demo-skill"))
	writeFile(t, filepath.Join(h, ".claude", "skills", "synced", "abc", "SKILL.md"), "---\nname: synced\n---\n")
	writeFile(t, filepath.Join(h, ".claude", "skills", "not-a-skill", "notes.md"), "no SKILL.md here\n")
	writeFile(t, filepath.Join(h, ".claude", "skills", "stray.md"), "a file, not a skill directory\n")
	writeFile(t, filepath.Join(h, ".claude", "skills", "broken", "SKILL.md"), "---\nname: [unclosed\n")
	writeFile(t, filepath.Join(h, ".claude", "commands", "commit.md"), "---\ndescription: c\n---\n$ARGUMENTS\n")
	writeFile(t, filepath.Join(h, ".claude", "commands", "frontend", "component.md"), "body\n")
	writeFile(t, filepath.Join(h, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: r\n---\nbody\n")
	copyTree(t, filepath.Join(fixture, "antigravity-flags", "skill"), filepath.Join(h, ".gemini", "config", "skills", "ag-skill"))
	writeFile(t, filepath.Join(h, ".codex", "agents", "worker.toml"), "name = \"worker\"\n")
	writeFile(t, filepath.Join(h, ".gemini", "config", "agents", "helper.md"), "---\nname: helper\ndescription: h\n---\nbody\n")
	copyTree(t, filepath.Join(fixture, "codex-sidecar", "skill"), filepath.Join(h, ".agents", "skills", "codex-skill"))
	return h
}

func TestScanUser(t *testing.T) {
	root := home(t)
	out, err := run(t, "scan", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"claude       skill    demo-skill",
		"codex: 1 dropped; antigravity: 1 dropped",
		"antigravity  skill    ag-skill            claude: lossless; codex: 1 dropped",
		"claude       command  commit",
		"claude       command  frontend/component",
		"claude       agent    reviewer",
		"antigravity  skill    ag-skill",
		"codex        agent    worker",
		"antigravity  agent    helper",
		"codex        skill    codex-skill",
		"broken",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("scan output lacks %q:\n%s", want, out)
		}
	}
	for _, skip := range []string{"synced", "not-a-skill", "stray"} {
		if strings.Contains(out, skip) {
			t.Errorf("scan listed %q:\n%s", skip, out)
		}
	}
	if !strings.Contains(out, "error (") {
		t.Errorf("an unreadable skill is not reported as an error:\n%s", out)
	}
}

func TestScanFiltersAndFormats(t *testing.T) {
	root := home(t)
	out, err := run(t, "scan", "--root", root, "--tool", "codex", "-o", "json")
	var entries []Entry
	if err != nil || json.Unmarshal([]byte(out), &entries) != nil || len(entries) != 2 {
		t.Fatalf("scan --tool codex -o json = %q, %v", out, err)
	}
	if entries[0].Targets == nil && entries[1].Targets == nil {
		t.Error("the Codex skill has no portability summary")
	}

	project := t.TempDir()
	copyTree(t, filepath.Join(fixture, "antigravity-flags", "skill"), filepath.Join(project, ".agents", "skills", "ag-skill"))
	out, err = run(t, "scan", "--scope", "project", "--root", project)
	if err != nil || strings.Count(out, "ag-skill") != 1 || !strings.Contains(out, "codex,antigravity") {
		t.Errorf("a shared .agents/skills entry should be listed once for both harnesses:\n%s %v", out, err)
	}

	empty := t.TempDir()
	if out, err := run(t, "scan", "--root", empty); err != nil || !strings.Contains(out, "no skills") {
		t.Errorf("empty scan = %q, %v", out, err)
	}
	if out, err := run(t, "scan", "--root", empty, "-o", "json"); err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("empty scan json = %q, %v", out, err)
	}
	if _, err := run(t, "scan", "--scope", "global"); err == nil {
		t.Error("scan accepted an unknown scope")
	}
	if _, err := run(t, "scan", "--tool", "cursor"); err == nil {
		t.Error("scan accepted an unknown tool")
	}
}

func TestScanDefaultRoots(t *testing.T) {
	root := home(t)
	t.Setenv("HOME", root)
	if out, err := run(t, "scan", "--tool", "claude"); err != nil || !strings.Contains(out, "demo-skill") {
		t.Errorf("user scope without --root = %q, %v", out, err)
	}
	t.Chdir(root)
	if out, err := run(t, "scan", "--scope", "project", "--tool", "claude"); err != nil || !strings.Contains(out, "demo-skill") {
		t.Errorf("project scope without --root = %q, %v", out, err)
	}
}

func TestConvertStrictWritesNothing(t *testing.T) {
	out := t.TempDir()
	src := filepath.Join(fixture, "claude-basic", "skill")
	if _, err := run(t, "convert", src, "--from", "claude", "--to", "codex", "--out", out, "--strict"); err == nil {
		t.Fatal("a lossy --strict conversion succeeded")
	}
	if _, err := os.Stat(filepath.Join(out, "demo-skill")); err == nil {
		t.Error("--strict wrote the lossy output anyway")
	}
}

func TestConvertRefusesSymlinkedTarget(t *testing.T) {
	hub := filepath.Join(t.TempDir(), "demo-skill")
	copyTree(t, filepath.Join(fixture, "claude-basic", "skill"), hub)
	before, _ := os.ReadFile(filepath.Join(hub, "SKILL.md"))
	out := t.TempDir()
	if err := os.Symlink(hub, filepath.Join(out, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "convert", hub, "--from", "claude", "--to", "codex", "--out", out, "--force"); err == nil {
		t.Error("--force wrote through a symlinked target")
	}
	if after, _ := os.ReadFile(filepath.Join(hub, "SKILL.md")); string(after) != string(before) {
		t.Error("the linked hub skill was overwritten")
	}
}

func TestConvertForceKeepsTargetSidecar(t *testing.T) {
	out := t.TempDir()
	target := filepath.Join(out, "demo-skill")
	writeFile(t, filepath.Join(target, "agents", "openai.yaml"), "interface:\n  display_name: Demo\npolicy:\n  allow_implicit_invocation: false\n")
	writeFile(t, filepath.Join(target, "stale.txt"), "from an earlier run\n")
	root := NewRootCmd()
	var stdout, stderr strings.Builder
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"convert", filepath.Join(fixture, "claude-basic", "skill"), "--from", "claude", "--to", "codex", "--out", out, "--force"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	side, _ := os.ReadFile(filepath.Join(target, "agents", "openai.yaml"))
	if !strings.Contains(string(side), "display_name: Demo") || !strings.Contains(string(side), "allow_implicit_invocation: false") {
		t.Errorf("the target's sidecar was replaced or loosened:\n%s", side)
	}
	if !strings.Contains(stdout.String(), "kept it although the source allows it") {
		t.Errorf("keeping the stale policy is not reported:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "left in place, not produced by this conversion: stale.txt") {
		t.Errorf("stale files are not listed:\n%s", stderr.String())
	}
}

func TestScanSymlinksPermissionsAndSharedOwners(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(t.TempDir(), "linked-skill")
	copyTree(t, filepath.Join(fixture, "claude-basic", "skill"), hub)
	if err := os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hub, filepath.Join(root, ".claude", "skills", "linked-skill")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".claude", "agents", "ok.md"), "---\nname: ok\ndescription: d\n---\n")
	locked := filepath.Join(root, ".claude", "commands", "locked")
	writeFile(t, filepath.Join(locked, "secret.md"), "x\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	cmd := NewRootCmd()
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"scan", "--root", root, "--tool", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "linked-skill") || !strings.Contains(stdout.String(), "ok") {
		t.Errorf("a symlinked skill or a readable agent is missing:\n%s", stdout.String())
	}
	if os.Geteuid() != 0 && !strings.Contains(stderr.String(), "warning:") {
		t.Errorf("an unreadable directory produced no warning:\n%s", stderr.String())
	}

	project := t.TempDir()
	copyTree(t, filepath.Join(fixture, "antigravity-flags", "skill"), filepath.Join(project, ".agents", "skills", "ag-skill"))
	out, err := run(t, "scan", "--scope", "project", "--root", project, "-o", "json", "--tool", "antigravity", "--tool", "codex", "--tool", "codex")
	var entries []Entry
	if err != nil || json.Unmarshal([]byte(out), &entries) != nil || len(entries) != 1 {
		t.Fatalf("shared scan = %q, %v", out, err)
	}
	e := entries[0]
	if len(e.Harnesses) != 2 || len(e.Targets) != 1 || e.Targets["claude"].Lossy {
		t.Errorf("a shared Antigravity skill should be graded only against Claude, losslessly: %+v", e)
	}
}
