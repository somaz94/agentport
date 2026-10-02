package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	fixture        = filepath.Join("..", "..", "internal", "convert", "testdata", "skills")
	commandFixture = filepath.Join("..", "..", "internal", "convert", "testdata", "commands")
)

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
		"missing path":   {"convert", filepath.Join(t.TempDir(), "missing.md"), "--to", "codex"},
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
	writeFile(t, filepath.Join(h, ".claude", "commands", "demo-skill.md"), "shadowed by the skill\n")
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
		"skipped (a skill named demo-skill exists and keeps the name)",
		"claude       agent    reviewer            codex: lossless; antigravity: lossless",
		"antigravity  agent    helper              claude: 1 dropped; codex: 1 dropped, 2 warn",
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
	// A skill is listed under its frontmatter name, not the name of the link to it.
	if !strings.Contains(stdout.String(), "skill  demo-skill") || !strings.Contains(stdout.String(), "agent  ok") {
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

func TestConvertCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cmds := filepath.Join(home, ".claude", "commands")
	copyTree(t, filepath.Join(commandFixture, "basic", "commands"), cmds)
	writeFile(t, filepath.Join(cmds, "frontend", "Component.md"), "Scaffold $0.\n")
	deploy := filepath.Join(cmds, "deploy.md")

	out, err := run(t, "convert", deploy, "--to", "antigravity", "--print")
	if err != nil || !strings.Contains(out, "Claude Code command deploy -> Antigravity skill deploy") ||
		!strings.Contains(out, "disable-model-invocation: true") {
		t.Errorf("command preview = %q, %v", out, err)
	}
	if out, err := run(t, "convert", deploy, "--to", "antigravity", "--print", "--model-invocable"); err != nil ||
		strings.Contains(out, "disable-model-invocation: true") {
		t.Errorf("--model-invocable preview = %q, %v", out, err)
	}
	if out, err := run(t, "convert", filepath.Join(cmds, "frontend", "Component.md"), "--to", "codex"); err != nil ||
		!strings.Contains(out, "Codex skill frontend-component") {
		t.Errorf("nested command = %q, %v", out, err)
	}
	dst := t.TempDir()
	if _, err := run(t, "convert", deploy, "--to", "codex", "--out", dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "deploy", "agents", "openai.yaml")); err != nil {
		t.Errorf("the Codex policy sidecar was not written: %v", err)
	}

	// A skill of the same name, or another command deriving it, keeps a command from converting.
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), "---\nname: deploy\n---\n")
	if _, err := run(t, "convert", deploy, "--to", "codex"); err == nil || !strings.Contains(err.Error(), "not converted") {
		t.Errorf("a command shadowed by a skill: %v", err)
	}
	writeFile(t, filepath.Join(cmds, "Frontend-component.md"), "x\n")
	if _, err := run(t, "convert", filepath.Join(cmds, "frontend", "Component.md"), "--to", "codex"); err == nil {
		t.Error("two commands deriving one skill name converted")
	}

	// Outside every commands directory a command needs --from and is named after its file.
	loose := filepath.Join(t.TempDir(), "Loose.md")
	writeFile(t, loose, "body\n")
	if _, err := run(t, "convert", loose, "--to", "codex"); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Errorf("an undetectable command: %v", err)
	}
	if out, err := run(t, "convert", loose, "--from", "claude", "--to", "codex"); err != nil || !strings.Contains(out, "Codex skill loose") {
		t.Errorf("--from claude = %q, %v", out, err)
	}
	for _, from := range []string{"codex", "cursor"} {
		if _, err := run(t, "convert", loose, "--from", from, "--to", "claude"); err == nil {
			t.Errorf("--from %s read a command", from)
		}
	}
	text := filepath.Join(t.TempDir(), "notes.txt")
	writeFile(t, text, "x\n")
	if _, err := run(t, "convert", text, "--from", "claude", "--to", "codex"); err == nil || !strings.Contains(err.Error(), "neither") {
		t.Errorf("a non-Markdown file: %v", err)
	}
}

// TestCommandsThroughLinks lays commands out behind symbolic links, which Claude Code follows, so
// the name checks must see through them too.
func TestCommandsThroughLinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target, team := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(target, "deploy.md"), "---\ndescription: d\n---\nShip it.\n")
	writeFile(t, filepath.Join(target, "team-x.md"), "x\n")
	writeFile(t, filepath.Join(team, "x.md"), "x\n")
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), "---\nname: deploy\n---\n")
	cmds := filepath.Join(home, ".claude", "commands")
	if err := os.Symlink(target, cmds); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(team, filepath.Join(target, "team")); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"deploy.md", "team-x.md", "team/x.md"} {
		if _, err := run(t, "convert", filepath.Join(cmds, filepath.FromSlash(rel)), "--to", "codex"); err == nil || !strings.Contains(err.Error(), "not converted") {
			t.Errorf("%s converted through a link: %v", rel, err)
		}
	}
	if out, err := run(t, "scan", "--tool", "claude"); err != nil || !strings.Contains(out, "team/x") || strings.Count(out, "skipped (") != 3 {
		t.Errorf("scan through links = %q, %v", out, err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root lists every directory")
	}
	// Reachable by path but not listable: Claude Code never loads it, so it has no name to keep.
	locked := filepath.Join(target, "locked")
	writeFile(t, filepath.Join(locked, "y.md"), "y\n")
	if err := os.Chmod(locked, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := run(t, "convert", filepath.Join(cmds, "locked", "y.md"), "--to", "codex"); err == nil || !strings.Contains(err.Error(), "never reaches it") {
		t.Errorf("a command below an unlistable directory: %v", err)
	}
}

// TestConvertCommandRerun converts a command into the same Codex directory with and without
// --model-invocable: the policy agentport generated follows each run's flags.
func TestConvertCommandRerun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cmds := filepath.Join(home, ".claude", "commands")
	copyTree(t, filepath.Join(commandFixture, "basic", "commands"), cmds)
	dst := t.TempDir()
	for i, flags := range [][]string{nil, {"--force", "--model-invocable"}, {"--force"}} {
		out, err := run(t, append([]string{"convert", filepath.Join(cmds, "deploy.md"), "--to", "codex", "--out", dst}, flags...)...)
		side, _ := os.ReadFile(filepath.Join(dst, "deploy", "agents", "openai.yaml"))
		want := "allow_implicit_invocation: false"
		if i == 1 {
			want = "allow_implicit_invocation: true"
		}
		if err != nil || !strings.Contains(string(side), want) || strings.Contains(out, "kept it although") {
			t.Errorf("run %d %v: sidecar %q, %v\n%s", i, flags, side, err, out)
		}
	}
}

// TestScanUncheckedCommands makes the skills directory unreadable: no command can be checked for a
// skill of its name, so scan skips them all, as convert refuses them, and warns only once.
func TestScanUncheckedCommands(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude", "commands", "a-b.md"), "x\n")
	writeFile(t, filepath.Join(root, ".claude", "commands", "A", "b.md"), "x\n")
	skills := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skills, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(skills, 0o755) })

	cmd := NewRootCmd()
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"scan", "--root", root, "--tool", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout.String(), "skipped (name collisions not checked") != 2 {
		t.Errorf("commands were graded without a name check:\n%s", stdout.String())
	}
	if strings.Count(stderr.String(), "warning:") != 1 {
		t.Errorf("want one warning:\n%s", stderr.String())
	}
	if _, err := run(t, "convert", filepath.Join(root, ".claude", "commands", "a-b.md"), "--to", "codex"); err == nil || !strings.Contains(err.Error(), "check name collisions") {
		t.Errorf("convert without a name check: %v", err)
	}
}

func TestConvertAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agents := filepath.Join(home, ".claude", "agents")
	reviewer := filepath.Join(agents, "reviewer.md")
	writeFile(t, reviewer, "---\nname: reviewer\ndescription: Reviews diffs.\ntools: Read, Grep\n---\nHand fixes to `fixer`.\n")
	writeFile(t, filepath.Join(agents, "team", "fixer.md"), "---\nname: fixer\ndescription: Fixes.\n---\nFix it.\n")

	out, err := run(t, "convert", reviewer, "--to", "antigravity", "--print")
	for _, want := range []string{"Claude Code agent reviewer -> Antigravity agent reviewer", "  - view_file", "mainAgent: false",
		"refers to fixer, which Antigravity does not have; convert it too"} {
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("preview lacks %q: %v\n%s", want, err, out)
		}
	}
	// Once Antigravity has the delegate, the reference resolves.
	writeFile(t, filepath.Join(home, ".gemini", "config", "agents", "fixer.md"), "---\nname: fixer\ndescription: f\n---\n")
	if out, err := run(t, "convert", reviewer, "--to", "antigravity"); err != nil || strings.Contains(out, "refers to") {
		t.Errorf("a delegate Antigravity has was reported: %v\n%s", err, out)
	}

	dst := t.TempDir()
	if _, err := run(t, "convert", reviewer, "--to", "codex", "--out", dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "reviewer.toml")); err != nil || !strings.Contains(string(data), "developer_instructions = '''") {
		t.Errorf("the Codex role was not written: %v\n%s", err, data)
	}
	if _, err := run(t, "convert", reviewer, "--to", "codex", "--out", dst); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("an existing role was replaced without --force: %v", err)
	}
	if _, err := run(t, "convert", reviewer, "--to", "codex", "--out", dst, "--force"); err != nil {
		t.Errorf("--force: %v", err)
	}
	// An agent goes into --out itself, so a linked --out is followed, as it is for a skill.
	linkedOut := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dst, linkedOut); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "convert", reviewer, "--to", "antigravity", "--out", linkedOut); err != nil {
		t.Errorf("a linked --out: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "reviewer.md")); err != nil {
		t.Errorf("the agent did not land behind the link: %v", err)
	}

	role := filepath.Join(home, ".codex", "agents", "worker.toml")
	writeFile(t, role, "name = \"worker\"\ndescription = \"w\"\ndeveloper_instructions = \"Work.\"\n")
	if out, err := run(t, "convert", role, "--to", "claude"); err != nil || !strings.Contains(out, "Codex agent worker -> Claude Code agent worker") {
		t.Errorf("Codex role: %v\n%s", err, out)
	}

	loose := filepath.Join(t.TempDir(), "loose.md")
	writeFile(t, loose, "---\nname: loose\ndescription: l\n---\nbody\n")
	writeFile(t, filepath.Join(home, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\n")
	writeFile(t, filepath.Join(home, "skills", "s", "notes.md"), "notes\n")
	if _, err := run(t, "convert", loose, "--kind", "agent", "--to", "codex"); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Errorf("an agent outside every agents directory: %v", err)
	}
	if _, err := run(t, "convert", loose, "--to", "codex"); err == nil || !strings.Contains(err.Error(), "--kind agent") {
		t.Errorf("a loose file without --from does not suggest --kind agent: %v", err)
	}
	if out, err := run(t, "convert", loose, "--kind", "agent", "--from", "claude", "--to", "codex"); err != nil || !strings.Contains(out, "Codex agent loose") {
		t.Errorf("--kind agent --from claude: %v\n%s", err, out)
	}
	if out, err := run(t, "convert", loose, "--kind", "command", "--from", "claude", "--to", "codex"); err != nil || !strings.Contains(out, "Claude Code command loose") {
		t.Errorf("--kind command: %v\n%s", err, out)
	}
	for name, args := range map[string][]string{
		"unknown kind":                 {"convert", loose, "--kind", "rule", "--to", "codex"},
		"agent directory":              {"convert", agents, "--kind", "agent", "--to", "codex"},
		"skill from a file":            {"convert", loose, "--kind", "skill", "--to", "codex"},
		"bad --from":                   {"convert", reviewer, "--from", "cursor", "--to", "codex"},
		"unreadable agent":             {"convert", filepath.Join(home, ".gemini", "config", "agents", "fixer.md"), "--to", "codex"},
		"skill from a file in a skill": {"convert", filepath.Join(home, "skills", "s", "notes.md"), "--kind", "skill", "--to", "codex"},
	} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%s: converted", name)
		}
	}
}

// TestAgentsSharingAName puts two agents with one name in ~/.claude/agents: Claude Code loads one of
// them, so neither converts and scan says why.
func TestAgentsSharingAName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agents := filepath.Join(home, ".claude", "agents")
	writeFile(t, filepath.Join(agents, "a.md"), "---\nname: dup\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(agents, "team", "b.md"), "---\nname: dup\ndescription: d\n---\nbody\n")
	if _, err := run(t, "convert", filepath.Join(agents, "a.md"), "--to", "codex"); err == nil || !strings.Contains(err.Error(), "is also named dup") {
		t.Errorf("an agent sharing its name converted: %v", err)
	}
	// Read as Claude Code's explicitly, the same check applies.
	if _, err := run(t, "convert", filepath.Join(agents, "a.md"), "--from", "claude", "--to", "codex"); err == nil {
		t.Error("--from claude skipped the name check")
	}
	if out, err := run(t, "scan", "--tool", "claude"); err != nil || strings.Count(out, "skipped (") != 2 {
		t.Errorf("scan = %q, %v", out, err)
	}
}
