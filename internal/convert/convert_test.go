package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/somaz94/agentport/internal/golden"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/skilldir"
)

var cases = map[string]harness.ID{
	"claude-basic":      harness.Claude,
	"claude-flags":      harness.Claude,
	"antigravity-flags": harness.Antigravity,
	"codex-sidecar":     harness.Codex,
}

// bundle renders a conversion as one reviewable text: every file with its mode, then the report.
func bundle(t *testing.T, res Result) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, f := range res.Files {
		fmt.Fprintf(&b, "== %s (%04o)\n%s", f.Path, f.Mode, f.Data)
		if len(f.Data) > 0 && f.Data[len(f.Data)-1] != '\n' {
			b.WriteString("\n")
		}
	}
	b.WriteString("== loss report\n")
	if err := loss.WriteJSON(&b, []loss.Report{res.Report}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSkillGolden(t *testing.T) {
	for name, from := range cases {
		for _, to := range harness.All {
			t.Run(name+"/"+string(to), func(t *testing.T) {
				dir := filepath.Join("testdata", "skills", name)
				item, err := ReadSkill(from, filepath.Join(dir, "skill"))
				if err != nil {
					t.Fatal(err)
				}
				item.Source.Path = ""
				res, err := Skill(item, to)
				if err != nil {
					t.Fatal(err)
				}
				golden.Assert(t, filepath.Join(dir, string(to)+".golden"), bundle(t, res))
			})
		}
	}
}

// TestRoundTripThroughEachTarget writes a Claude skill to another harness, reads it back as that
// harness, converts it to Claude again and checks what must survive: body, invocation, resources.
func TestRoundTripThroughEachTarget(t *testing.T) {
	for _, name := range []string{"claude-basic", "claude-flags"} {
		src, err := ReadSkill(harness.Claude, filepath.Join("testdata", "skills", name, "skill"))
		if err != nil {
			t.Fatal(err)
		}
		for _, via := range []harness.ID{harness.Codex, harness.Antigravity} {
			t.Run(name+"/"+string(via), func(t *testing.T) {
				out, err := Skill(src, via)
				if err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(t.TempDir(), src.Name)
				if err := skilldir.Write(dir, out.Files); err != nil {
					t.Fatal(err)
				}
				mid, err := ReadSkill(via, dir)
				if err != nil {
					t.Fatal(err)
				}
				back, err := Skill(mid, harness.Claude)
				if err != nil {
					t.Fatal(err)
				}
				final, err := ReadSkill(harness.Claude, writeTemp(t, back))
				if err != nil {
					t.Fatal(err)
				}
				if final.Body != src.Body {
					t.Errorf("body changed:\n%q\n%q", src.Body, final.Body)
				}
				wantInv := src.Invocation
				if via == harness.Codex {
					wantInv.UserInvocable = true
				}
				if final.Invocation != wantInv {
					t.Errorf("invocation = %+v; want %+v", final.Invocation, wantInv)
				}
				if !reflect.DeepEqual(final.Resources, src.Resources) {
					t.Errorf("resources changed: %+v", final.Resources)
				}
			})
		}
	}
}

func writeTemp(t *testing.T, res Result) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), res.Item.Name)
	if err := skilldir.Write(dir, res.Files); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCodexRejectsLongNames(t *testing.T) {
	item := &ir.Item{Kind: ir.KindSkill, Name: string(bytes.Repeat([]byte("a"), 65)), Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true}}
	if _, err := Skill(item, harness.Codex); err == nil {
		t.Error("Codex accepted a 65-character skill name")
	}
	if _, err := Skill(item, "cursor"); err == nil {
		t.Error("an unknown target succeeded")
	}
	if _, err := ReadSkill("cursor", "."); err == nil {
		t.Error("an unknown source succeeded")
	}
}

func TestDetectSkill(t *testing.T) {
	home := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]harness.ID{
		".claude/skills/a":        harness.Claude,
		"repo/.claude/skills/b":   harness.Claude,
		".gemini/config/skills/c": harness.Antigravity,
		".agents/skills/d":        harness.Codex,
	}
	for rel, want := range cases {
		if got, err := DetectSkill(mk(rel), home); err != nil || got != want {
			t.Errorf("DetectSkill(%s) = %q, %v; want %q", rel, got, err, want)
		}
	}
	// Link mode: a Codex skill symlinked to a hub skill belongs to the hub.
	if err := os.Symlink(mk(".claude/skills/linked"), filepath.Join(mk(".agents/skills"), "linked")); err != nil {
		t.Fatal(err)
	}
	if got, err := DetectSkill(filepath.Join(home, ".agents", "skills", "linked"), home); err != nil || got != harness.Claude {
		t.Errorf("symlinked skill = %q, %v; want claude", got, err)
	}
	// A directory merely under ~/.codex is not a Codex skill location, and the legacy one needs --from.
	for _, rel := range []string{".codex/worktrees/repo/.agents/skills/w", ".codex/skills/e", "elsewhere/skills/z"} {
		if _, err := DetectSkill(mk(rel), home); !errors.Is(err, ErrAmbiguous) {
			t.Errorf("DetectSkill(%s) error = %v; want ErrAmbiguous", rel, err)
		}
	}
	shared := mk("repo/.agents/skills/f")
	if _, err := DetectSkill(shared, home); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("project .agents/skills error = %v; want ErrAmbiguous", err)
	}
	mk("repo/.agents/skills/f/agents")
	if err := os.WriteFile(filepath.Join(shared, "agents", "openai.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := DetectSkill(shared, home); err != nil || got != harness.Codex {
		t.Errorf("project skill with a Codex sidecar = %q, %v; want codex", got, err)
	}
}

// TestReportsAreValidJSON guards the loss report shape the golden files embed.
func TestReportsAreValidJSON(t *testing.T) {
	item, err := ReadSkill(harness.Claude, filepath.Join("testdata", "skills", "claude-basic", "skill"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Skill(item, harness.Antigravity)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := loss.WriteJSON(&buf, []loss.Report{res.Report}); err != nil {
		t.Fatal(err)
	}
	var decoded []loss.Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil || len(decoded[0].Entries) == 0 {
		t.Errorf("report JSON = %s, %v", buf.String(), err)
	}
}
