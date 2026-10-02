package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
)

func skillDir(t *testing.T, frontmatter string, extra map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "s")
	files := map[string]string{"SKILL.md": "---\n" + frontmatter + "---\nbody\n"}
	for k, v := range extra {
		files[k] = v
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadersRejectBadValues(t *testing.T) {
	cases := []struct {
		name  string
		h     harness.ID
		fm    string
		extra map[string]string
	}{
		{"claude disable-model-invocation", harness.Claude, "disable-model-invocation: maybe\n", nil},
		{"claude user-invocable", harness.Claude, "user-invocable: [x]\n", nil},
		{"antigravity disable-slash-command", harness.Antigravity, "disable-slash-command: yes please\n", nil},
		{"yaml 1.1 boolean", harness.Claude, "disable-model-invocation: yes\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ReadSkill(c.h, skillDir(t, c.fm, c.extra)); err == nil {
				t.Error("ReadSkill succeeded; want an error")
			}
		})
	}
	for _, h := range harness.All {
		if _, err := ReadSkill(h, t.TempDir()); err == nil {
			t.Errorf("%s ReadSkill of an empty directory succeeded", h)
		}
	}
}

func TestDefaultsAndEdges(t *testing.T) {
	// A skill without a name takes its directory name; without a description or body, targets warn.
	item, err := ReadSkill(harness.Claude, skillDir(t, "", nil))
	if err != nil || item.Name != "s" {
		t.Fatalf("name defaulting: %+v, %v", item, err)
	}
	item.Body = ""
	item.Name = "Bad_Name"
	res, err := Skill(item, harness.Antigravity, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(res.Report, "name", loss.Warn) || !hasEntry(res.Report, "description", loss.Warn) {
		t.Errorf("report = %+v; want warnings for the name and the empty description", res.Report.Entries)
	}

	long := &ir.Item{Name: "s", Description: strings.Repeat("d", 1025), Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true}, Source: ir.Source{Harness: harness.Claude}}
	if res, _ := Skill(long, harness.Codex, Options{}); !hasEntry(res.Report, "description", loss.Warn) {
		t.Error("no warning for a description Codex truncates")
	}

	hint := &ir.Item{Name: "s", Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true, ArgumentHint: "[x]"}, Source: ir.Source{Harness: harness.Claude}}
	if res, _ := Skill(hint, harness.Antigravity, Options{}); !hasEntry(res.Report, "argument-hint", loss.Dropped) {
		t.Error("a hint on a skill without placeholders is not reported as dropped")
	}

	foreign := &ir.Item{Name: "s", Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true},
		Extensions: []ir.Field{{Key: "disable-slash-command-extra"}}, Source: ir.Source{Harness: harness.Antigravity}}
	if res, _ := Skill(foreign, harness.Claude, Options{}); !hasEntry(res.Report, "disable-slash-command-extra", loss.Dropped) {
		t.Error("a foreign-only field written to Claude is not reported as dropped")
	}
}

func TestCodexSidecarUpdates(t *testing.T) {
	// An existing sidecar keeps its other fields when the policy flips, and a missing policy defaults to implicit.
	dir := skillDir(t, "name: s\ndescription: d\n", map[string]string{"agents/openai.yaml": "interface:\n  display_name: S\n"})
	item, err := ReadSkill(harness.Codex, dir)
	if err != nil || !item.Invocation.ModelInvocable {
		t.Fatalf("sidecar without policy: %+v, %v", item, err)
	}
	item.Invocation.ModelInvocable = false
	res, err := Skill(item, harness.Codex, Options{})
	if err != nil {
		t.Fatal(err)
	}
	side := sidecar(res)
	if !strings.Contains(side, "display_name: S") || !strings.Contains(side, "allow_implicit_invocation: false") {
		t.Errorf("updated sidecar =\n%s", side)
	}

	empty := skillDir(t, "name: s\ndescription: d\n", map[string]string{"agents/openai.yaml": ""})
	item, _ = ReadSkill(harness.Codex, empty)
	item.Invocation.ModelInvocable = false
	if res, err := Skill(item, harness.Codex, Options{}); err != nil || !strings.Contains(sidecar(res), "allow_implicit_invocation: false") {
		t.Errorf("empty sidecar: %q, %v", sidecar(res), err)
	}

	broken := &ir.Item{Name: "s", Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: false},
		Resources: []ir.Resource{{Path: "agents/openai.yaml", Data: []byte("policy: [\n")}}, Source: ir.Source{Harness: harness.Codex}}
	if _, err := Skill(broken, harness.Codex, Options{}); err == nil {
		t.Error("an unparsable sidecar was rewritten without an error")
	}
}

func sidecar(res Result) string {
	for _, f := range res.Files {
		if f.Path == "agents/openai.yaml" {
			return string(f.Data)
		}
	}
	return ""
}

func hasEntry(r loss.Report, field string, s loss.Status) bool {
	for _, e := range r.Entries {
		if e.Field == field && e.Status == s {
			return true
		}
	}
	return false
}

func TestUnsafeNamesAreRefused(t *testing.T) {
	for _, name := range []string{"../escape", "a/b", ".", ".."} {
		item := &ir.Item{Name: name, Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true}}
		for _, to := range harness.All {
			if _, err := Skill(item, to, Options{}); err == nil {
				t.Errorf("Skill(name=%q, to=%s) succeeded", name, to)
			}
		}
	}
	// Without a name key, the name is the directory's own name even for a relative path.
	dir := skillDir(t, "description: d\n", nil)
	t.Chdir(dir)
	item, err := ReadSkill(harness.Claude, ".")
	if err != nil || item.Name != "s" {
		t.Errorf("ReadSkill(.) name = %+v, %v; want s", item, err)
	}
}

func TestCodexKeepsABundledPolicyOff(t *testing.T) {
	dir := skillDir(t, "name: s\ndescription: d\n", map[string]string{"agents/openai.yaml": "policy:\n  allow_implicit_invocation: false\n"})
	item, err := ReadSkill(harness.Claude, dir)
	if err != nil || !item.Invocation.ModelInvocable {
		t.Fatalf("Claude reads the skill as model-invocable: %+v, %v", item, err)
	}
	res, err := Skill(item, harness.Codex, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sidecar(res), "allow_implicit_invocation: false") || !hasEntry(res.Report, "policy.allow_implicit_invocation", loss.Warn) {
		t.Errorf("the bundled policy was loosened or not reported:\n%s\n%+v", sidecar(res), res.Report.Entries)
	}
	// Going to Claude, the generated one-line sidecar is not copied; its meaning lives in the flags.
	item.Invocation.ModelInvocable = false
	if res, _ := Skill(item, harness.Claude, Options{}); sidecar(res) != "" || !hasEntry(res.Report, "agents/openai.yaml", loss.Mapped) {
		t.Errorf("a generated sidecar reached Claude: %q", sidecar(res))
	}
}

func TestCodexSidecarShapes(t *testing.T) {
	cases := map[string]string{
		"null document":  "null\n",
		"empty document": "---\n",
		"aliased policy": "base: &p\n  allow_implicit_invocation: true\npolicy: *p\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			item := &ir.Item{Name: "s", Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: false},
				Resources: []ir.Resource{{Path: "agents/openai.yaml", Data: []byte(body)}}, Source: ir.Source{Harness: harness.Codex}}
			res, err := Skill(item, harness.Codex, Options{})
			if err != nil || !strings.Contains(sidecar(res), "allow_implicit_invocation: false") {
				t.Errorf("sidecar = %q, %v", sidecar(res), err)
			}
		})
	}
	for name, body := range map[string]string{"list": "- a\n", "scalar policy": "policy: off\n"} {
		item := &ir.Item{Name: "s", Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: false},
			Resources: []ir.Resource{{Path: "agents/openai.yaml", Data: []byte(body)}}, Source: ir.Source{Harness: harness.Codex}}
		if _, err := Skill(item, harness.Codex, Options{}); err == nil {
			t.Errorf("%s: an unusable sidecar was rewritten without an error", name)
		}
	}
	// Codex ignores a sidecar it cannot parse, so reading one is a warning, not a failure.
	dir := skillDir(t, "name: s\ndescription: d\n", map[string]string{"agents/openai.yaml": "policy: [\n"})
	item, err := ReadSkill(harness.Codex, dir)
	if err != nil || !item.Invocation.ModelInvocable || len(item.Notes) != 1 {
		t.Fatalf("unparsable sidecar: %+v, %v", item, err)
	}
	if res, _ := Skill(item, harness.Antigravity, Options{}); !hasEntry(res.Report, "agents/openai.yaml", loss.Warn) {
		t.Error("the reader's note did not reach the report")
	}
}

func TestBodyReferencesAreReported(t *testing.T) {
	item, err := ReadSkill(harness.Claude, skillDir(t, "name: s\ndescription: d\narguments: [env, region]\n", nil))
	if err != nil {
		t.Fatal(err)
	}
	item.Body = "Deploy $env to $region with ${CLAUDE_SKILL_DIR}/scripts/x.sh, see @docs/guide.md.\n"
	res, err := Skill(item, harness.Antigravity, Options{})
	if err != nil {
		t.Fatal(err)
	}
	entry := string(res.Files[0].Data)
	if !strings.Contains(entry, "`$env`, `$region` are those arguments in that order") {
		t.Errorf("named arguments are not explained:\n%s", entry)
	}
	details := ""
	for _, e := range res.Report.Entries {
		details += e.Detail + "\n"
	}
	for _, want := range []string{"${CLAUDE_SKILL_DIR}", "@path", "named arguments"} {
		if !strings.Contains(details, want) {
			t.Errorf("report does not mention %s:\n%s", want, details)
		}
	}
}

func TestCodexLoadChecks(t *testing.T) {
	empty := &ir.Item{Name: "s", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true}, Source: ir.Source{Harness: harness.Claude}}
	if _, err := Skill(empty, harness.Codex, Options{}); err == nil {
		t.Error("Codex accepted a skill with no description and an empty body")
	}
	korean := &ir.Item{Name: "s", Description: strings.Repeat("가", 500), Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true}, Source: ir.Source{Harness: harness.Claude}}
	if res, _ := Skill(korean, harness.Codex, Options{}); hasEntry(res.Report, "description", loss.Warn) {
		t.Error("500 Korean characters counted as over the 1,024-character limit")
	}
	scalar := &ir.Item{Name: "s", Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true},
		Extensions: []ir.Field{{Key: "metadata", Value: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x"}}}, Source: ir.Source{Harness: harness.Claude}}
	res, err := Skill(scalar, harness.Codex, Options{})
	if err != nil || strings.Contains(string(res.Files[0].Data), "metadata") || !hasEntry(res.Report, "metadata", loss.Dropped) {
		t.Errorf("a scalar metadata reached Codex: %s %v", res.Files[0].Data, err)
	}
}

func TestCollectionValuesKeepTheirText(t *testing.T) {
	item, err := ReadSkill(harness.Claude, skillDir(t, "description: [beta]\nargument-hint: {path}\nwhen_to_use: ~\n", nil))
	if err != nil {
		t.Fatal(err)
	}
	if item.Description != "[beta]" || item.Invocation.ArgumentHint != "{path}" {
		t.Errorf("description %q, hint %q; want the bracketed text", item.Description, item.Invocation.ArgumentHint)
	}
	if res, _ := Skill(item, harness.Antigravity, Options{}); !hasEntry(res.Report, "when_to_use", loss.Dropped) {
		t.Error("an empty when_to_use is not reported")
	}
}

// A sidecar's policy only travels in the flags when the flags actually say it; otherwise the file
// is kept, because the Claude and Antigravity readers do not parse it.
func TestGeneratedLookingSidecarIsKeptWhenFlagsDisagree(t *testing.T) {
	dir := skillDir(t, "name: s\ndescription: d\n", map[string]string{"agents/openai.yaml": "policy:\n  allow_implicit_invocation: false\n"})
	item, err := ReadSkill(harness.Claude, dir)
	if err != nil || !item.Invocation.ModelInvocable {
		t.Fatalf("%+v, %v", item, err)
	}
	res, err := Skill(item, harness.Antigravity, Options{})
	if err != nil || sidecar(res) == "" {
		t.Errorf("the sidecar was dropped although no flag carries its policy: %v", err)
	}
}
