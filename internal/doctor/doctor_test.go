package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/paths"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(body), 0o644))
}

func fakeVersions(t *testing.T, probes map[harness.ID]func(context.Context) (string, string, error)) {
	t.Helper()
	saved := Versions
	Versions = probes
	t.Cleanup(func() { Versions = saved })
}

func find(findings []Finding, check string) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func TestRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fakeVersions(t, map[harness.ID]func(context.Context) (string, string, error){
		harness.Claude:      func(context.Context) (string, string, error) { return "2.1.285", "/bin/claude", nil },
		harness.Codex:       func(context.Context) (string, string, error) { return "", "", ErrNotInstalled },
		harness.Antigravity: func(context.Context) (string, string, error) { return "2.20.0", "/Applications/Antigravity.app", nil },
	})
	root := t.TempDir()
	put(t, root, ".claude/skills/a/SKILL.md", "---\nname: a\n---\n")
	put(t, root, ".gemini/config/AGENTS.md", "x\n")
	put(t, root, ".gemini/config/GEMINI.md", "x\n")
	put(t, root, ".gemini/config/workflows/old.md", "x\n")
	must(t, os.MkdirAll(filepath.Join(root, ".gemini/config/global_workflows"), 0o755))
	put(t, root, ".codex/prompts/p.md", "x\n")
	m := manifest.New()
	m.Entries[".gemini/config/skills/a/SKILL.md"] = manifest.Entry{OutputHash: "x", Mode: "0644"}
	must(t, m.Save(filepath.Join(root, ".gemini/config")))

	got := Run(context.Background(), Options{Scope: paths.ScopeUser, Root: root})
	checks := map[string]Status{
		"config": OK, "claude version": OK, "codex version": Info, "antigravity version": Warn,
		"codex legacy": Warn, "antigravity legacy": Warn, "antigravity manifest": Warn,
		"antigravity instructions": Warn, "codex manifest": Info,
	}
	for check, want := range checks {
		fs := find(got, check)
		if len(fs) != 1 || fs[0].Status != want {
			t.Errorf("%s = %+v; want one %s finding", check, fs, want)
		}
	}
	if f := find(got, "antigravity legacy"); len(f) == 1 && !strings.Contains(f[0].Message, "workflows") {
		t.Errorf("legacy finding = %+v; the empty global_workflows must not count", f)
	}
	if len(find(got, "claude manifest")) != 0 {
		t.Error("the hub got a manifest check")
	}
	if f := find(got, "claude locations"); len(f) != 3 || !strings.Contains(f[0].Message, "(1 entries)") || !strings.Contains(f[1].Message, "none yet") {
		t.Errorf("claude locations = %+v", f)
	}
}

func TestRunErrors(t *testing.T) {
	fakeVersions(t, map[harness.ID]func(context.Context) (string, string, error){
		harness.Claude:      func(context.Context) (string, string, error) { return "", "", errors.New("boom") },
		harness.Codex:       func(context.Context) (string, string, error) { return "", "", ErrNotInstalled },
		harness.Antigravity: func(context.Context) (string, string, error) { return "", "", ErrNotInstalled },
	})
	root := t.TempDir()
	cfg := filepath.Join(root, "config.yaml")
	put(t, root, "config.yaml", "unknown: 1\n")
	put(t, root, ".codex/.agentport/manifest.json", "{")
	got := Run(context.Background(), Options{Scope: paths.ScopeUser, Root: root, Config: cfg})
	if f := find(got, "config"); len(f) != 1 || f[0].Status != Error {
		t.Errorf("config = %+v", f)
	}
	if f := find(got, "codex manifest"); len(f) != 1 || f[0].Status != Error {
		t.Errorf("codex manifest = %+v", f)
	}
	if f := find(got, "claude version"); len(f) != 1 || f[0].Status != Warn || !strings.Contains(f[0].Message, "boom") {
		t.Errorf("claude version = %+v", f)
	}
	if f := find(got, "claude locations"); len(f) != 1 || f[0].Status != Info {
		t.Errorf("a missing hub = %+v", f)
	}
}

func TestProjectScope(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fakeVersions(t, map[harness.ID]func(context.Context) (string, string, error){
		harness.Claude:      func(context.Context) (string, string, error) { return "", "", ErrNotInstalled },
		harness.Codex:       func(context.Context) (string, string, error) { return "", "", ErrNotInstalled },
		harness.Antigravity: func(context.Context) (string, string, error) { return "", "", ErrNotInstalled },
	})
	repo := t.TempDir()
	put(t, repo, "_agents/workflows/w.md", "x\n")
	put(t, repo, ".agents/skills/s/SKILL.md", "x\n")
	got := Run(context.Background(), Options{Scope: paths.ScopeProject, Root: repo})
	if f := find(got, "antigravity legacy"); len(f) != 1 || !strings.HasPrefix(f[0].Message, filepath.Join("_agents", "workflows")) {
		t.Errorf("legacy = %+v", f)
	}
	if len(find(got, "antigravity manifest"))+len(find(got, "antigravity instructions")) != 0 {
		t.Error("project scope got user-scope checks")
	}
}

func TestDisplay(t *testing.T) {
	home, err := os.UserHomeDir()
	must(t, err)
	if got := display(Options{Scope: paths.ScopeUser, Root: home}, ".codex"); got != filepath.Join("~", ".codex") {
		t.Errorf("display = %q", got)
	}
	if got := display(Options{Scope: paths.ScopeUser, Root: "/r"}, ".codex"); got != filepath.Join("/r", ".codex") {
		t.Errorf("display = %q", got)
	}
}

func TestCommandVersion(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	write := func(name, script string) {
		must(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755))
	}
	write("good", "echo 'tool 1.2.3 (build)'\n")
	write("silent", "echo 'no version'\n")
	write("broken", "exit 3\n")
	t.Setenv("PATH", dir)
	if v, where, err := commandVersion("good")(context.Background()); err != nil || v != "1.2.3" || where != filepath.Join(dir, "good") {
		t.Errorf("good = %q, %q, %v", v, where, err)
	}
	for _, name := range []string{"silent", "broken"} {
		if _, _, err := commandVersion(name)(context.Background()); err == nil || errors.Is(err, ErrNotInstalled) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := commandVersion("absent")(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("absent: %v", err)
	}
}

func TestAntigravityVersion(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if _, _, err := antigravityVersion(context.Background()); err == nil {
			t.Error("read an app bundle version off macOS")
		}
		return
	}
	saved := antigravityApp
	t.Cleanup(func() { antigravityApp = saved })
	antigravityApp = t.TempDir()
	if _, _, err := antigravityVersion(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("missing bundle: %v", err)
	}
	put(t, antigravityApp, "Contents/Info.plist", "<plist><dict><key>CFBundleShortVersionString</key>\n\t<string>9.8.7</string></dict></plist>")
	if v, _, err := antigravityVersion(context.Background()); err != nil || v != "9.8.7" {
		t.Errorf("version = %q, %v", v, err)
	}
	put(t, antigravityApp, "Contents/Info.plist", "<plist></plist>")
	if _, _, err := antigravityVersion(context.Background()); err == nil {
		t.Error("a plist without a version was accepted")
	}
}
