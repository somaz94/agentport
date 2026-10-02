package reconcile

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/manifest"
)

func adopt(t *testing.T, opts Options, rel string) *Adoption {
	t.Helper()
	a, err := Adopt(opts, filepath.Join(opts.Root, filepath.FromSlash(rel)))
	must(t, err)
	must(t, a.Apply())
	return a
}

func unitState(t *testing.T, opts Options, unit string) []manifest.State {
	t.Helper()
	var out []manifest.State
	for _, tg := range plan(t, opts).Targets {
		for _, u := range tg.Units {
			if u.Path == unit {
				for _, f := range u.Files {
					out = append(out, f.State)
				}
			}
		}
	}
	return out
}

func allUnchanged(states []manifest.State) bool {
	for _, s := range states {
		if s != manifest.StateUnchanged {
			return false
		}
	}
	return len(states) > 0
}

func TestAdoptSkillBodyKeepsFrontmatter(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/skills/alpha/SKILL.md"
	put(t, root, rel, strings.Replace(read(t, root, rel), "Do alpha.", "Do alpha twice.", 1), 0o644)

	a := adopt(t, opts, rel)
	if len(a.Changes) != 1 || a.Changes[0].Path != ".claude/skills/alpha/SKILL.md" || len(a.Notes) != 0 {
		t.Fatalf("adoption = %+v", a)
	}
	if got := read(t, root, ".claude/skills/alpha/SKILL.md"); got != strings.Replace(alphaSkill, "Do alpha.", "Do alpha twice.", 1) {
		t.Errorf("hub = %q; want the frontmatter byte for byte and the new body", got)
	}
	if s := unitState(t, opts, ".gemini/config/skills/alpha"); !allUnchanged(s) {
		t.Errorf("after adopting, the unit is %v; want unchanged", s)
	}
}

func TestAdoptCommand(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/skills/deploy/SKILL.md"
	edited := strings.Replace(read(t, root, rel), "Deploy a service", "Deploy one service", 1)
	edited = strings.Replace(edited, "Deploy $ARGUMENTS now.", "Deploy $ARGUMENTS carefully.", 1)
	edited = strings.Replace(edited, "disable-model-invocation: true\n", "", 1)
	put(t, root, rel, edited, 0o644)

	a := adopt(t, opts, ".gemini/config/skills/deploy")
	hub := read(t, root, ".claude/commands/deploy.md")
	if !strings.Contains(hub, "description: 'Deploy one service'") || !strings.Contains(hub, "Deploy $ARGUMENTS carefully.") ||
		strings.Contains(hub, "agentport:args") || !strings.Contains(hub, "argument-hint: <service>") {
		t.Errorf("hub command = %q", hub)
	}
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "modelInvocableCommands") {
		t.Errorf("notes = %v; want the invocation policy note", a.Notes)
	}
}

func TestAdoptAgentTools(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/agents/reviewer.md"
	edited := strings.Replace(read(t, root, rel), "  - grep_search\n", "  - run_command\n  - manage_task\n  - schedule\n", 1)
	edited = strings.Replace(edited, "mainAgent: false", "preloadSkills:\n  - alpha\nmainAgent: true", 1)
	put(t, root, rel, edited, 0o644)

	a := adopt(t, opts, rel)
	hub := read(t, root, ".claude/agents/reviewer.md")
	if !strings.Contains(hub, "tools: Read, Bash\n") || !strings.Contains(hub, "skills:\n  - alpha\n") {
		t.Errorf("hub agent = %q", hub)
	}
	want := []string{"mainAgent: Antigravity-only; not adopted", "tools: Antigravity has no Claude Code counterpart for schedule; not adopted"}
	if !slices.Equal(a.Notes, want) {
		t.Errorf("notes = %q; want %q", a.Notes, want)
	}
}

func TestAdoptAllToolsAgentUsesDisallowedTools(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".claude/agents/reviewer.md", "---\nname: reviewer\ndescription: Reviews code\n---\nReview it.\n", 0o644)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/agents/reviewer.md"
	put(t, root, rel, strings.Replace(read(t, root, rel), "  - write_to_file\n", "", 1), 0o644)
	adopt(t, opts, rel)
	if hub := read(t, root, ".claude/agents/reviewer.md"); !strings.Contains(hub, "disallowedTools: Write\n") || strings.Contains(hub, "tools: ") && !strings.Contains(hub, "disallowedTools") {
		t.Errorf("hub agent = %q", hub)
	}
	if s := unitState(t, opts, rel); !allUnchanged(s) {
		t.Errorf("after adopting, the agent is %v", s)
	}
}

func TestAdoptResources(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	put(t, root, ".gemini/config/skills/alpha/scripts/run.sh", "#!/bin/sh\necho changed\n", 0o755)
	put(t, root, ".gemini/config/skills/alpha/notes.md", "new\n", 0o644)
	a := adopt(t, opts, ".gemini/config/skills/alpha/scripts/run.sh")
	if len(a.Changes) != 2 || read(t, root, ".claude/skills/alpha/scripts/run.sh") != "#!/bin/sh\necho changed\n" ||
		read(t, root, ".claude/skills/alpha/notes.md") != "new\n" {
		t.Errorf("adoption = %+v", a.Changes)
	}
	if info, _ := os.Stat(filepath.Join(root, ".claude/skills/alpha/scripts/run.sh")); info.Mode().Perm() != 0o755 {
		t.Errorf("adopted script mode = %v", info.Mode().Perm())
	}

	must(t, os.Remove(filepath.Join(root, ".gemini/config/skills/alpha/notes.md")))
	sync(t, opts)
	must(t, os.Remove(filepath.Join(root, ".gemini/config/skills/alpha/notes.md")))
	a, err := Adopt(opts, filepath.Join(root, ".gemini/config/skills/alpha"))
	must(t, err)
	if len(a.Changes) != 0 || len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "notes.md was deleted") {
		t.Errorf("deleted resource: %+v", a)
	}
}

func TestAdoptCodex(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Codex)
	sync(t, opts)
	rel := ".codex/agents/reviewer.toml"
	edited := strings.Replace(read(t, root, rel), "description = \"Reviews code\"", "description = \"Reviews all code\"\nmodel_reasoning_effort = \"high\"\nmodel = \"gpt-5\"", 1)
	put(t, root, rel, edited+"\n[features]\nshell_tool = false\n", 0o644)
	a := adopt(t, opts, rel)
	hub := read(t, root, ".claude/agents/reviewer.md")
	if !strings.Contains(hub, "description: 'Reviews all code'") || !strings.Contains(hub, "effort: high") || strings.Contains(hub, "gpt-5") {
		t.Errorf("hub agent = %q", hub)
	}
	for _, want := range []string{"features: Codex-only; not adopted", "model: Claude Code has no gpt-5 model; not adopted", "tools: Codex has no per-agent tool list; not adopted"} {
		if !slices.Contains(a.Notes, want) {
			t.Errorf("notes = %q; want %q", a.Notes, want)
		}
	}

	// A skill's invocation policy lives in the sidecar.
	sidecar := ".agents/skills/alpha/agents/openai.yaml"
	put(t, root, sidecar, "policy:\n  allow_implicit_invocation: false\n", 0o644)
	adopt(t, opts, ".agents/skills/alpha")
	if hub := read(t, root, ".claude/skills/alpha/SKILL.md"); !strings.Contains(hub, "disable-model-invocation: true") {
		t.Errorf("hub skill = %q; want model invocation off", hub)
	}
}

func TestAdoptRefusals(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/agents/reviewer.md"
	put(t, root, rel, strings.Replace(read(t, root, rel), "Review it.", "Review it twice.", 1), 0o644)
	put(t, root, ".claude/agents/reviewer.md", strings.Replace(reviewer, "Reviews code", "Reviews any code", 1), 0o644)
	if _, err := Adopt(opts, filepath.Join(root, rel)); !errors.Is(err, ErrHubChanged) {
		t.Errorf("adopting over a changed hub: %v; want ErrHubChanged", err)
	}
	forced := opts
	forced.Force = true
	if _, err := Adopt(forced, filepath.Join(root, rel)); err != nil {
		t.Errorf("--force: %v", err)
	}

	put(t, root, ".gemini/config/skills/mine/SKILL.md", "---\nname: mine\ndescription: x\n---\n", 0o644)
	must(t, os.Remove(filepath.Join(root, ".claude/commands/ops/check.md")))
	for name, target := range map[string]string{
		"unmanaged":       ".gemini/config/skills/mine",
		"source gone":     ".gemini/config/skills/ops-check",
		"outside targets": ".gemini/config/AGENTS.md",
		"hub file":        ".claude/skills/alpha",
	} {
		if _, err := Adopt(opts, filepath.Join(root, filepath.FromSlash(target))); err == nil {
			t.Errorf("%s: adopted", name)
		}
	}
	if _, err := Adopt(opts, t.TempDir()); err == nil {
		t.Error("a path outside the root was adopted")
	}
	if a, err := Adopt(opts, filepath.Join(root, ".gemini/config/skills/alpha")); err != nil || len(a.Changes)+len(a.Notes) != 0 {
		t.Errorf("an unedited unit: %+v, %v; want nothing to adopt", a, err)
	}
}

func TestAdoptSkillInvocation(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".claude/skills/alpha/SKILL.md", "---\nname: alpha\ndescription: Alpha skill\nargument-hint: <x>\nlicense: MIT\n---\nDo alpha with $ARGUMENTS.\n", 0o644)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/skills/alpha/SKILL.md"
	edited := strings.Replace(read(t, root, rel), "license: MIT\n", "disable-model-invocation: true\ndisable-slash-command: true\n", 1)
	edited = strings.Replace(edited, "`/alpha <x>`", "`/alpha <y>`", 1)
	put(t, root, rel, edited, 0o644)
	a := adopt(t, opts, rel)
	hub := read(t, root, ".claude/skills/alpha/SKILL.md")
	for _, want := range []string{"argument-hint: <y>", "disable-model-invocation: true", "user-invocable: false"} {
		if !strings.Contains(hub, want) {
			t.Errorf("hub lacks %q:\n%s", want, hub)
		}
	}
	if strings.Contains(hub, "license") || len(a.Notes) != 0 {
		t.Errorf("hub = %q, notes %v; want the removed portable key gone and no notes", hub, a.Notes)
	}
	if s := unitState(t, opts, ".gemini/config/skills/alpha"); !allUnchanged(s) {
		t.Errorf("after adopting, the unit is %v", s)
	}
}

func TestAdoptNeverClaimsSomeoneElsesFile(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Codex)
	sync(t, opts)
	sidecar := ".agents/skills/alpha/agents/openai.yaml"
	custom := "interface:\n  display_name: Alpha\npolicy:\n  allow_implicit_invocation: false\n"
	put(t, root, sidecar, custom, 0o644)
	a := adopt(t, opts, ".agents/skills/alpha")
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "Codex-only settings") {
		t.Errorf("notes = %v", a.Notes)
	}
	forced := opts
	forced.Force = true
	sync(t, forced)
	if read(t, root, sidecar) != custom {
		t.Error("sync --force overwrote a sidecar agentport never wrote")
	}
}

func TestAdoptWritesThroughALinkedHubFile(t *testing.T) {
	root := hubRoot(t)
	dotfiles := filepath.Join(t.TempDir(), "reviewer.md")
	must(t, os.Rename(filepath.Join(root, ".claude/agents/reviewer.md"), dotfiles))
	must(t, os.Symlink(dotfiles, filepath.Join(root, ".claude/agents/reviewer.md")))
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/agents/reviewer.md"
	put(t, root, rel, strings.Replace(read(t, root, rel), "Review it.", "Review it closely.", 1), 0o644)
	adopt(t, opts, rel)
	info, err := os.Lstat(filepath.Join(root, ".claude/agents/reviewer.md"))
	must(t, err)
	data, _ := os.ReadFile(dotfiles)
	if info.Mode()&os.ModeSymlink == 0 || !strings.Contains(string(data), "Review it closely.") {
		t.Errorf("the link was replaced, or its target was not updated: %v %q", info.Mode(), data)
	}
}

func TestAdoptRefusesASourceOutsideTheRoot(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	m, err := manifest.Load(filepath.Join(root, ".gemini/config"))
	must(t, err)
	e := m.Entries[".gemini/config/agents/reviewer.md"]
	e.Source = ".claude/agents/../../outside.md"
	m.Entries[".gemini/config/agents/reviewer.md"] = e
	must(t, m.Save(filepath.Join(root, ".gemini/config")))
	put(t, root, ".gemini/config/agents/reviewer.md", "edited\n", 0o644)
	if _, err := Adopt(opts, filepath.Join(root, ".gemini/config/agents/reviewer.md")); err == nil || !strings.Contains(err.Error(), "not a path below") {
		t.Errorf("a source climbing out of the root: %v", err)
	}
}

func TestAdoptIgnoresLeftoversAndReportsReformatting(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	put(t, root, ".gemini/config/skills/alpha/SKILL.md~", "backup\n", 0o644)
	put(t, root, ".gemini/config/skills/alpha/scripts/__pycache__/x.pyc", "cache\n", 0o644)
	if a, err := Adopt(opts, filepath.Join(root, ".gemini/config/skills/alpha")); err != nil || len(a.Changes) != 0 || a.Reformat {
		t.Errorf("leftovers: %+v, %v; want nothing to adopt", a, err)
	}
	rel := ".gemini/config/skills/alpha/SKILL.md"
	put(t, root, rel, strings.Replace(read(t, root, rel), "description: 'Alpha skill'", "description: Alpha skill", 1), 0o644)
	a, err := Adopt(opts, filepath.Join(root, ".gemini/config/skills/alpha"))
	if err != nil || len(a.Changes) != 0 || !a.Reformat {
		t.Errorf("a quoting change: %+v, %v; want it reported as formatting only", a, err)
	}
}

func TestAdoptSkipsLinksTheHubDoesNotRead(t *testing.T) {
	root := hubRoot(t)
	shared := t.TempDir()
	must(t, os.Rename(filepath.Join(root, ".claude/skills/alpha/scripts"), filepath.Join(shared, "scripts")))
	must(t, os.Symlink(filepath.Join(shared, "scripts"), filepath.Join(root, ".claude/skills/alpha/scripts")))
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	put(t, root, ".gemini/config/skills/alpha/scripts/new.sh", "#!/bin/sh\n", 0o755)
	a, err := Adopt(opts, filepath.Join(root, ".gemini/config/skills/alpha"))
	must(t, err)
	must(t, a.Apply())
	if len(a.Changes) != 0 || len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "a link the hub does not read through") {
		t.Errorf("adoption = %+v", a)
	}
	if _, err := os.Stat(filepath.Join(shared, "scripts", "new.sh")); err == nil {
		t.Error("adopt wrote through a link the hub reader skips")
	}
}

func TestAdoptKeepsUntouchedFilesUpdatable(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Codex)
	sync(t, opts)
	// As if an older agentport wrote this SKILL.md differently, and nobody touched it since.
	rel := ".agents/skills/alpha/SKILL.md"
	older := "older output\n" + read(t, root, rel)
	put(t, root, rel, older, 0o644)
	m, err := manifest.Load(filepath.Join(root, ".codex"))
	must(t, err)
	e := m.Entries[rel]
	e.OutputHash = manifest.Hash([]byte(older))
	m.Entries[rel] = e
	must(t, m.Save(filepath.Join(root, ".codex")))
	put(t, root, ".agents/skills/alpha/agents/openai.yaml", "policy:\n  allow_implicit_invocation: false\n", 0o644)

	if _, err := Adopt(opts, filepath.Join(root, ".agents/skills/alpha")); err == nil || !strings.Contains(err.Error(), "what agentport now writes") {
		t.Errorf("adopting over a converter change: %v", err)
	}
	forced := opts
	forced.Force = true
	adopt(t, forced, ".agents/skills/alpha")
	if got := states(plan(t, opts))[rel]; got != string(manifest.StateUpdate) {
		t.Errorf("the untouched SKILL.md is %s after adopting; want update, so sync still rewrites it", got)
	}
}
