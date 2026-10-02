package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/harness"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, err := DefaultPath(); err != nil || p != filepath.Join("/xdg", "agentport", "config.yaml") {
		t.Errorf("DefaultPath = %q, %v", p, err)
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	if p, err := DefaultPath(); err != nil || p != filepath.Join(home, ".config", "agentport", "config.yaml") {
		t.Errorf("DefaultPath without XDG_CONFIG_HOME = %q, %v", p, err)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load("")
	if err != nil || c.Hub != harness.Claude || c.Targets != nil || c.Path != "" {
		t.Errorf("Load with no file = %+v, %v; want the defaults", c, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing file named on the command line was accepted")
	}
}

func TestLoad(t *testing.T) {
	p := write(t, "hub: claude-code\ntargets: [agy]\npairs: ['-ko']\nskip: ['commands/internal', 'agents/*-draft.md']\nmodelInvocableCommands: true\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hub != harness.Claude || !slices.Equal(c.Targets, []harness.ID{harness.Antigravity}) || !slices.Equal(c.Pairs, []string{"-ko"}) ||
		len(c.Skip) != 2 || !c.ModelInvocableCommands || c.Path != p {
		t.Errorf("Load = %+v", c)
	}
	if c, err := Load(write(t, "")); err != nil || c.Targets != nil {
		t.Errorf("empty file = %+v, %v; want the defaults", c, err)
	}
}

func TestLoadRejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":      "exclude: ['*-private']\n",
		"not a mapping":    "just text\n",
		"unknown hub":      "hub: cursor\n",
		"non-claude hub":   "hub: codex\n",
		"unknown target":   "targets: [cursor]\n",
		"hub as target":    "targets: [claude]\n",
		"repeated target":  "targets: [codex, codex]\n",
		"no targets":       "targets: []\n",
		"empty pair":       "pairs: ['']\n",
		"pair with slash":  "pairs: ['/ko']\n",
		"repeated pair":    "pairs: ['-ko', '-ko']\n",
		"bad pattern":      "skip: ['[']\n",
		"absolute pattern": "skip: ['/etc']\n",
		"backslash":        "skip: ['commands\\\\x']\n",
		"empty pattern":    "skip: ['']\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := write(t, body)
			_, err := Load(p)
			if err == nil {
				t.Fatal("Load succeeded")
			}
			if !strings.Contains(err.Error(), p) {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Error("Load of a directory succeeded")
	}
}

func TestSkipped(t *testing.T) {
	c := &Config{Skip: []string{"commands/internal", "agents/*-draft.md", "skills/x"}}
	for rel, want := range map[string]bool{
		"commands/internal/a.md":     true,
		"commands/internal":          true,
		"commands/public.md":         false,
		"agents/review-draft.md":     true,
		"agents/sub/review-draft.md": false,
		"skills/x":                   true,
		"skills/xy":                  false,
		"":                           false,
	} {
		if got := c.Skipped(rel); got != want {
			t.Errorf("Skipped(%q) = %v; want %v", rel, got, want)
		}
	}
}

// TestDesignExample keeps the settings example in docs/design.md loadable and complete.
func TestDesignExample(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "design.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	section := doc[strings.Index(doc, "## Configuration"):]
	start := strings.Index(section, "```yaml\n")
	if start < 0 {
		t.Fatal("no yaml example under ## Configuration")
	}
	block := section[start+len("```yaml\n"):]
	block = block[:strings.Index(block, "```")]
	if _, err := parse([]byte(block)); err != nil {
		t.Errorf("the example does not load: %v", err)
	}
	ft := reflect.TypeFor[file]()
	for i := range ft.NumField() {
		if key := strings.Split(ft.Field(i).Tag.Get("yaml"), ",")[0]; !strings.Contains(block, "\n"+key+":") && !strings.HasPrefix(block, key+":") {
			t.Errorf("the example leaves out %s", key)
		}
	}
}
