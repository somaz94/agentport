package reconcile

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/somaz94/agentport/internal/config"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/manifest"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, root, rel, content string, mode fs.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(content), mode))
	must(t, os.Chmod(p, mode))
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	must(t, err)
	return string(data)
}

func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

const (
	alphaSkill = "---\nname: alpha\ndescription: Alpha skill\nallowed-tools: Read\n---\nDo alpha.\n"
	deployCmd  = "---\ndescription: Deploy a service\nargument-hint: <service>\n---\nDeploy $ARGUMENTS now.\n"
	checkCmd   = "---\ndescription: Run checks\n---\nCheck everything.\n"
	reviewer   = "---\nname: reviewer\ndescription: Reviews code\ntools: Read, Grep\n---\nReview it.\n"
)

// hubRoot builds a home directory with a Claude Code hub, its Korean mirrors, and empty
// Antigravity and Codex configuration directories.
func hubRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, ".claude/skills/alpha/SKILL.md", alphaSkill, 0o644)
	put(t, root, ".claude/skills/alpha/scripts/run.sh", "#!/bin/sh\necho alpha\n", 0o755)
	put(t, root, ".claude/commands/deploy.md", deployCmd, 0o644)
	put(t, root, ".claude/commands/ops/check.md", checkCmd, 0o644)
	put(t, root, ".claude/agents/reviewer.md", reviewer, 0o644)
	put(t, root, ".claude/commands-ko/deploy.md", "---\ndescription: 서비스 배포\n---\n$ARGUMENTS 배포.\n", 0o644)
	put(t, root, ".claude/agents-ko/reviewer.md", "---\nname: reviewer\ndescription: 코드 리뷰\ntools: Read\n---\n리뷰.\n", 0o644)
	must(t, os.MkdirAll(filepath.Join(root, ".gemini/config"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, ".codex"), 0o755))
	return root
}

func options(root string, cfg *config.Config, targets ...harness.ID) Options {
	if cfg == nil {
		cfg = config.Default()
		cfg.Pairs = []string{"-ko"}
	}
	if len(targets) == 0 {
		targets = []harness.ID{harness.Antigravity, harness.Codex}
	}
	return Options{Root: root, Config: cfg, Targets: targets, Generator: "agentport test"}
}

func plan(t *testing.T, opts Options) *Plan {
	t.Helper()
	p, err := New(opts)
	must(t, err)
	for _, tg := range p.Targets {
		if tg.Err != nil {
			t.Fatalf("%s: %v", tg.Harness, tg.Err)
		}
	}
	return p
}

func sync(t *testing.T, opts Options) *Plan {
	t.Helper()
	p := plan(t, opts)
	must(t, p.Apply())
	return p
}

// states maps every file path of the plan to its state, and every unit with a status to it.
func states(p *Plan) map[string]string {
	out := map[string]string{}
	for _, tg := range p.Targets {
		for _, u := range tg.Units {
			if u.Status != "" {
				out[cmpKey(u)] = u.Status
				continue
			}
			for _, f := range u.Files {
				out[f.Path] = string(f.State)
			}
		}
	}
	return out
}

func TestSyncWritesEveryTarget(t *testing.T) {
	root := hubRoot(t)
	p := sync(t, options(root, nil))

	for _, rel := range []string{
		".gemini/config/skills/alpha/SKILL.md",
		".gemini/config/skills/deploy/SKILL.md",
		".gemini/config/skills/ops-check/SKILL.md",
		".gemini/config/agents/reviewer.md",
		".gemini/config/skills-ko/deploy/SKILL.md",
		".gemini/config/agents-ko/reviewer.md",
		".agents/skills/deploy/SKILL.md",
		".agents/skills/deploy/agents/openai.yaml",
		".codex/agents/reviewer.toml",
		".codex/agents-ko/reviewer.toml",
	} {
		if !exists(root, rel) {
			t.Errorf("%s was not written", rel)
		}
	}
	info, err := os.Stat(filepath.Join(root, ".gemini/config/skills/alpha/scripts/run.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("bundled script mode = %v, %v; want 0755", info, err)
	}
	if !strings.Contains(read(t, root, ".gemini/config/skills/deploy/SKILL.md"), "disable-model-invocation: true") {
		t.Error("a converted command can be started by the model")
	}
	m, err := manifest.Load(filepath.Join(root, ".gemini/config"))
	must(t, err)
	e, ok := m.Entries[".gemini/config/skills/deploy/SKILL.md"]
	if !ok || e.Source != ".claude/commands/deploy.md" || e.Mode != "0644" || e.Generator != "agentport test" || e.SourceHash == "" {
		t.Errorf("manifest entry = %+v, %v", e, ok)
	}
	if _, ok := m.Entries[".codex/agents/reviewer.toml"]; ok {
		t.Error("the Antigravity manifest records a Codex file")
	}
	if p.Lossy() != true {
		t.Error("dropping allowed-tools is not reported as a loss")
	}
}

func TestSecondSyncChangesNothing(t *testing.T) {
	root := hubRoot(t)
	sync(t, options(root, nil))
	manifests := map[string][]byte{}
	mtimes := map[string]time.Time{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(p, ".claude") {
			info, _ := d.Info()
			mtimes[p] = info.ModTime()
			if strings.HasSuffix(p, manifest.File) {
				manifests[p], _ = os.ReadFile(p)
			}
		}
		return nil
	})
	time.Sleep(10 * time.Millisecond)

	p := sync(t, options(root, nil))
	if p.Changes() {
		t.Errorf("second sync has changes: %v", states(p))
	}
	for path, mtime := range mtimes {
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().Equal(mtime) {
			t.Errorf("%s was rewritten by the second sync", path)
		}
	}
	for path, before := range manifests {
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Errorf("%s changed on the second sync", path)
		}
	}
	if len(manifests) != 2 {
		t.Errorf("manifests = %d; want one per target", len(manifests))
	}
}

func TestUnmanagedFilesAreNeverTouched(t *testing.T) {
	root := hubRoot(t)
	unmanaged := map[string]string{
		// A hand-ported skill where a command would go.
		".gemini/config/skills/deploy/SKILL.md": "---\nname: deploy\ndescription: hand-ported\n---\nmine\n",
		".gemini/config/skills/deploy/notes.md": "notes\n",
		// Another file named like the hub agent.
		".gemini/config/agents/my-reviewer.md": "---\nname: reviewer\ndescription: mine\ntools: [view_file]\n---\nmine\n",
		".gemini/config/skills/other/SKILL.md": "---\nname: other\ndescription: other\n---\nother\n",
		".gemini/config/AGENTS.md":             "instructions\n",
		".codex/agents/custom.toml":            "name = \"custom\"\n",
	}
	for rel, content := range unmanaged {
		put(t, root, rel, content, 0o600)
	}
	p := sync(t, options(root, nil))
	for rel, content := range unmanaged {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || read(t, root, rel) != content || info.Mode().Perm() != 0o600 {
			t.Errorf("%s was changed", rel)
		}
	}
	got := states(p)
	if got[".gemini/config/skills/deploy"] != Conflict || got[".gemini/config/agents/reviewer.md"] != Conflict {
		t.Errorf("states = %v; want the hand-ported skill and the same-named agent as conflicts", got)
	}
	ag := p.Targets[0]
	for _, want := range []string{".gemini/config/skills/other", ".gemini/config/agents/my-reviewer.md"} {
		if !slices.Contains(ag.Unmanaged, want) {
			t.Errorf("unmanaged = %v; want %s", ag.Unmanaged, want)
		}
	}
	if exists(root, ".gemini/config/agents/reviewer.md") {
		t.Error("an agent was written beside an unmanaged one of the same name")
	}
	m, _ := manifest.Load(filepath.Join(root, ".gemini/config"))
	for key := range m.Entries {
		if strings.HasPrefix(key, ".gemini/config/skills/deploy/") {
			t.Errorf("the manifest claims %s", key)
		}
	}
	if !p.Changes() {
		t.Error("conflicts do not count as out of sync")
	}
}

func TestIdenticalUnmanagedFileIsAdopted(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	first := plan(t, opts)
	var skill []byte
	for _, u := range first.Targets[0].Units {
		if u.Path == ".gemini/config/skills/alpha" {
			for _, f := range u.Files {
				if strings.HasSuffix(f.Path, "SKILL.md") {
					skill = f.data
				}
			}
		}
	}
	put(t, root, ".gemini/config/skills/alpha/SKILL.md", string(skill), 0o644)
	p := sync(t, opts)
	if got := states(p)[".gemini/config/skills/alpha/SKILL.md"]; got != string(manifest.StateUnchanged) {
		t.Errorf("identical unmanaged SKILL.md is %s; want unchanged", got)
	}
	m, _ := manifest.Load(filepath.Join(root, ".gemini/config"))
	if _, ok := m.Entries[".gemini/config/skills/alpha/SKILL.md"]; !ok {
		t.Error("an identical file was not recorded as managed")
	}
}

func TestDriftIsKeptUntilForced(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	rel := ".gemini/config/agents/reviewer.md"
	put(t, root, rel, "edited by hand\n", 0o644)

	p := sync(t, opts)
	if states(p)[rel] != string(manifest.StateDrift) || read(t, root, rel) != "edited by hand\n" {
		t.Fatalf("state %s, content %q; want drift, kept", states(p)[rel], read(t, root, rel))
	}
	opts.Force = true
	sync(t, opts)
	if read(t, root, rel) == "edited by hand\n" {
		t.Error("--force did not replace the edited file")
	}
	opts.Force = false
	if p := plan(t, opts); p.Changes() {
		t.Errorf("after --force: %v", states(p))
	}
}

func TestOrphans(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	must(t, os.Remove(filepath.Join(root, ".claude/commands/ops/check.md")))
	must(t, os.RemoveAll(filepath.Join(root, ".claude/skills/alpha")))
	put(t, root, ".gemini/config/skills/alpha/SKILL.md", "edited\n", 0o644)
	put(t, root, ".gemini/config/skills/alpha/extra.txt", "mine\n", 0o644)

	p := sync(t, opts)
	got := states(p)
	if got[".gemini/config/skills/ops-check/SKILL.md"] != string(manifest.StateOrphan) {
		t.Errorf("states = %v", got)
	}
	if exists(root, ".gemini/config/skills/ops-check") {
		t.Error("the orphaned skill's directory was not removed")
	}
	if got[".gemini/config/skills/alpha/SKILL.md"] != string(manifest.StateDrift) || !exists(root, ".gemini/config/skills/alpha/SKILL.md") {
		t.Error("an edited orphan was not kept as drift")
	}
	if exists(root, ".gemini/config/skills/alpha/scripts/run.sh") || !exists(root, ".gemini/config/skills/alpha/extra.txt") {
		t.Error("orphan deletion removed the wrong files")
	}

	opts.Force = true
	sync(t, opts)
	m, _ := manifest.Load(filepath.Join(root, ".gemini/config"))
	if _, ok := m.Entries[".gemini/config/skills/alpha/SKILL.md"]; ok || !exists(root, ".gemini/config/skills/alpha/SKILL.md") {
		t.Error("--force deleted an edited orphan, or kept tracking it")
	}
}

func TestSkipPatterns(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	opts.Config.Skip = []string{"commands/ops", "agents-ko/*"}
	p := sync(t, opts)
	got := states(p)
	if got[".gemini/config/skills/ops-check/SKILL.md"] != string(manifest.StateOrphan) || exists(root, ".gemini/config/skills/ops-check") {
		t.Errorf("a skipped command's output was not removed: %v", got)
	}
	if exists(root, ".gemini/config/agents-ko/reviewer.md") {
		t.Error("a skipped mirror agent's output was not removed")
	}
	if !exists(root, ".gemini/config/skills/deploy/SKILL.md") {
		t.Error("an unskipped command was removed")
	}
}

func TestCollisionsHoldEarlierOutput(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	put(t, root, ".claude/agents/sub/reviewer.md", reviewer, 0o644)
	p := sync(t, opts)
	got := states(p)
	if got[".claude/agents/reviewer.md"] != Skipped || got[".claude/agents/sub/reviewer.md"] != Skipped {
		t.Errorf("states = %v; want the duplicate agents skipped", got)
	}
	if !exists(root, ".gemini/config/agents/reviewer.md") {
		t.Error("the earlier output of a skipped agent was deleted")
	}
	put(t, root, ".claude/skills/beta/SKILL.md", "---\nname: alpha\ndescription: also alpha\n---\nb\n", 0o644)
	if got := states(plan(t, opts)); got[".claude/skills/alpha"] != Skipped || got[".claude/skills/beta"] != Skipped {
		t.Errorf("states = %v; want both skills named alpha skipped", got)
	}
}

func TestLinkedUnitIsNotWrittenThrough(t *testing.T) {
	root := hubRoot(t)
	elsewhere := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".gemini/config/skills"), 0o755))
	must(t, os.Symlink(elsewhere, filepath.Join(root, ".gemini/config/skills/alpha")))
	p := sync(t, options(root, nil, harness.Antigravity))
	if states(p)[".gemini/config/skills/alpha"] != Conflict {
		t.Errorf("states = %v", states(p))
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Error("files were written through a linked skill directory")
	}
}

func TestTargetErrors(t *testing.T) {
	root := hubRoot(t)
	must(t, os.RemoveAll(filepath.Join(root, ".codex")))
	must(t, os.Symlink(filepath.Join(root, ".claude/skills"), filepath.Join(root, ".gemini/config/skills")))
	p, err := New(options(root, nil))
	must(t, err)
	if p.Targets[0].Err == nil || !strings.Contains(p.Targets[0].Err.Error(), "hub") {
		t.Errorf("Antigravity error = %v; want the hub refusal", p.Targets[0].Err)
	}
	if p.Targets[1].Err == nil || !strings.Contains(p.Targets[1].Err.Error(), "not set up") {
		t.Errorf("Codex error = %v; want not set up", p.Targets[1].Err)
	}
	must(t, p.Apply())
	if exists(root, ".codex") {
		t.Error("a harness that was not set up got a directory")
	}

	if _, err := New(options(t.TempDir(), nil)); err == nil {
		t.Error("planning without a hub succeeded")
	}
	if _, err := New(options(root, nil, harness.Claude)); err == nil {
		t.Error("the hub was accepted as a target")
	}
}

func TestModeChangeIsAnUpdate(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	must(t, os.Chmod(filepath.Join(root, ".claude/skills/alpha/scripts/run.sh"), 0o644))
	p := sync(t, opts)
	if got := states(p)[".gemini/config/skills/alpha/scripts/run.sh"]; got != string(manifest.StateUpdate) {
		t.Errorf("state = %s; want update", got)
	}
	info, _ := os.Stat(filepath.Join(root, ".gemini/config/skills/alpha/scripts/run.sh"))
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v; want 0644", info.Mode().Perm())
	}
}

func TestRemovedPairIsCleanedUp(t *testing.T) {
	root := hubRoot(t)
	sync(t, options(root, nil, harness.Antigravity))
	p := sync(t, options(root, config.Default(), harness.Antigravity))
	if exists(root, ".gemini/config/skills-ko/deploy") || exists(root, ".gemini/config/agents-ko/reviewer.md") {
		t.Errorf("mirror output survived removing the pair: %v", states(p))
	}
}

func TestSuspiciousManifestEntryIsLeftAlone(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".gemini/config/AGENTS.md", "keep\n", 0o644)
	m := manifest.New()
	m.Entries[".gemini/config/AGENTS.md"] = manifest.Entry{Source: ".claude/gone.md", OutputHash: manifest.Hash([]byte("keep\n")), Mode: "0644"}
	must(t, m.Save(filepath.Join(root, ".gemini/config")))
	p := sync(t, options(root, nil, harness.Antigravity))
	if !exists(root, ".gemini/config/AGENTS.md") {
		t.Fatal("a manifest entry outside the target's directories deleted a file")
	}
	if len(p.Targets[0].Warnings) == 0 {
		t.Error("the suspicious entry was not reported")
	}
}

func TestStrayFilesInManagedUnits(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	put(t, root, ".gemini/config/skills/alpha/mine.txt", "mine\n", 0o644)
	p := plan(t, opts)
	if !slices.Contains(p.Targets[0].Unmanaged, ".gemini/config/skills/alpha/mine.txt") {
		t.Errorf("unmanaged = %v", p.Targets[0].Unmanaged)
	}
}

func TestReadFailuresAreHeld(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	put(t, root, ".claude/agents/reviewer.md", "---\nname: [broken\n---\n", 0o644)
	p := sync(t, opts)
	if got := states(p)[".claude/agents/reviewer.md"]; got != Failed {
		t.Errorf("states = %v; want the unreadable agent failed", states(p))
	}
	if !exists(root, ".gemini/config/agents/reviewer.md") {
		t.Error("the earlier output of an unreadable agent was deleted")
	}
}

func TestNamesDifferingInCaseCollide(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".claude/skills/upper/SKILL.md", "---\nname: Alpha\ndescription: upper\n---\nu\n", 0o644)
	p := sync(t, options(root, nil, harness.Antigravity))
	var reason string
	for _, u := range p.Targets[0].Units {
		if u.Path == ".gemini/config/skills/Alpha" && u.Status == Conflict {
			reason = u.Reason
		}
	}
	if !strings.Contains(reason, "letter case") {
		t.Errorf("states = %v; want the case-only variant reported", states(p))
	}
}

func TestConversionFailureIsHeld(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".claude/commands/empty.md", "", 0o644)
	p := sync(t, options(root, nil, harness.Codex))
	if got := states(p)[".claude/commands/empty.md"]; got != Failed {
		t.Errorf("states = %v; want the command Codex cannot load failed", states(p))
	}
	if p := plan(t, options(root, nil, harness.Codex)); !p.Lossy() || p.Changes() {
		t.Error("a failed unit hides its neighbours' losses, or counts as a change")
	}
}

func TestBrokenManifestStopsTheTarget(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".gemini/config/.agentport/manifest.json", "{", 0o644)
	p, err := New(options(root, nil))
	must(t, err)
	if p.Targets[0].Err == nil || p.Targets[1].Err != nil {
		t.Errorf("errors = %v, %v; want only Antigravity's", p.Targets[0].Err, p.Targets[1].Err)
	}
}

func TestUnreadableSkillsDirectoryHoldsCommands(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	skills := filepath.Join(root, ".claude/skills")
	must(t, os.Chmod(skills, 0o000))
	t.Cleanup(func() { _ = os.Chmod(skills, 0o755) })
	p := sync(t, opts)
	if got := states(p)[".claude/commands/deploy.md"]; got != Skipped {
		t.Errorf("states = %v; want commands skipped while skill names cannot be read", states(p))
	}
	if len(p.Warnings) == 0 {
		t.Error("the unreadable skills directory was not reported")
	}
	must(t, os.Chmod(skills, 0o755))
	if !exists(root, ".gemini/config/skills/deploy/SKILL.md") || !exists(root, ".gemini/config/skills/alpha/SKILL.md") {
		t.Error("output was deleted while its source could not be read")
	}
}

func TestFailedWriteRecordsWhatWasWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	root := hubRoot(t)
	agents := filepath.Join(root, ".gemini/config/agents")
	must(t, os.MkdirAll(agents, 0o555))
	t.Cleanup(func() { _ = os.Chmod(agents, 0o755) })
	p := plan(t, options(root, nil, harness.Antigravity))
	if err := p.Apply(); err == nil {
		t.Fatal("writing into a read-only directory succeeded")
	}
	m, err := manifest.Load(filepath.Join(root, ".gemini/config"))
	must(t, err)
	for key := range m.Entries {
		if !exists(root, key) {
			t.Errorf("the manifest records %s, which was not written", key)
		}
	}
	if len(m.Entries) == 0 {
		t.Error("nothing written before the failure was recorded")
	}
}

func TestHubDirectoryLinkedIntoATarget(t *testing.T) {
	root := hubRoot(t)
	// Codex sees Claude's skills because the hub's skills directory is its skills directory.
	must(t, os.MkdirAll(filepath.Join(root, ".agents"), 0o755))
	must(t, os.Rename(filepath.Join(root, ".claude/skills"), filepath.Join(root, ".agents/skills")))
	must(t, os.Symlink(filepath.Join(root, ".agents/skills"), filepath.Join(root, ".claude/skills")))
	p, err := New(options(root, nil, harness.Codex))
	must(t, err)
	if p.Targets[0].Err == nil || !strings.Contains(p.Targets[0].Err.Error(), "resolve to the same place") {
		t.Fatalf("Codex error = %v; want the hub's own directory refused", p.Targets[0].Err)
	}
	must(t, p.Apply())
	if exists(root, ".agents/skills/deploy") {
		t.Error("a converted command was written into the hub")
	}
}

func TestHubSkillLinkedFromATarget(t *testing.T) {
	root := hubRoot(t)
	target := filepath.Join(root, ".gemini/config/skills/alpha")
	must(t, os.MkdirAll(filepath.Dir(target), 0o755))
	must(t, os.Rename(filepath.Join(root, ".claude/skills/alpha"), target))
	must(t, os.Symlink(target, filepath.Join(root, ".claude/skills/alpha")))
	before := read(t, root, ".claude/skills/alpha/SKILL.md")
	p := sync(t, options(root, nil, harness.Antigravity))
	if got := states(p)[".gemini/config/skills/alpha"]; got != Skipped {
		t.Errorf("states = %v; want the skill the target reads through a link skipped", states(p))
	}
	if read(t, root, ".claude/skills/alpha/SKILL.md") != before {
		t.Error("the hub skill was overwritten through its link")
	}
}

func TestCaseCollisionKeepsEarlierOutput(t *testing.T) {
	root := hubRoot(t)
	must(t, os.Rename(filepath.Join(root, ".claude/skills/alpha"), filepath.Join(root, ".claude/skills/upper")))
	put(t, root, ".claude/skills/upper/SKILL.md", "---\nname: Alpha\ndescription: upper\n---\nu\n", 0o644)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	written := read(t, root, ".gemini/config/skills/Alpha/SKILL.md")
	put(t, root, ".claude/skills/alpha/SKILL.md", alphaSkill, 0o644)
	p := sync(t, opts)
	conflicts := 0
	for _, u := range p.Targets[0].Units {
		if strings.EqualFold(u.Path, ".gemini/config/skills/alpha") && u.Status == Conflict {
			conflicts++
		}
	}
	if conflicts != 2 {
		t.Errorf("units = %v; want both case variants a conflict", states(p))
	}
	if !exists(root, ".gemini/config/skills/Alpha/SKILL.md") || read(t, root, ".gemini/config/skills/Alpha/SKILL.md") != written {
		t.Error("the earlier output of a case-colliding skill was changed or deleted")
	}
}

func TestUncheckableHubSkillIsHeld(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	skill := filepath.Join(root, ".claude/skills/alpha")
	must(t, os.Chmod(skill, 0o000))
	t.Cleanup(func() { _ = os.Chmod(skill, 0o755) })
	p := sync(t, opts)
	must(t, os.Chmod(skill, 0o755))
	if !exists(root, ".gemini/config/skills/alpha/SKILL.md") || len(p.Warnings) == 0 {
		t.Errorf("the output of a skill that could not be checked was deleted, or nothing was reported: %v", p.Warnings)
	}
}

func TestDanglingHubDirectoryIsHeld(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	agents := filepath.Join(root, ".claude/agents")
	must(t, os.Rename(agents, filepath.Join(root, "moved-agents")))
	must(t, os.Symlink(filepath.Join(root, "unmounted"), agents))
	p := sync(t, opts)
	if !exists(root, ".gemini/config/agents/reviewer.md") || len(p.Warnings) == 0 {
		t.Error("output under a hub directory linked to nothing was deleted, or nothing was reported")
	}
}

func TestTargetsResolvingToOneDirectory(t *testing.T) {
	root := hubRoot(t)
	must(t, os.MkdirAll(filepath.Join(root, ".agents/skills"), 0o755))
	must(t, os.Symlink(filepath.Join(root, ".agents/skills"), filepath.Join(root, ".gemini/config/skills")))
	p, err := New(options(root, nil))
	must(t, err)
	if p.Targets[0].Err != nil || p.Targets[1].Err == nil || !strings.Contains(p.Targets[1].Err.Error(), "Antigravity") {
		t.Errorf("errors = %v, %v; want the later target refused", p.Targets[0].Err, p.Targets[1].Err)
	}
}

func TestFileWhereAUnitGoesIsAConflict(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".gemini/config/skills/deploy", "a file, not a directory\n", 0o644)
	p := sync(t, options(root, nil, harness.Antigravity))
	if got := states(p)[".gemini/config/skills/deploy"]; got != Conflict {
		t.Errorf("states = %v; want the skill blocked by a file a conflict", states(p))
	}
	if !exists(root, ".gemini/config/agents/reviewer.md") {
		t.Error("one blocked unit stopped the rest of the target")
	}
}

func TestManifestEntryFromAnotherSourceIsLeftAlone(t *testing.T) {
	root := hubRoot(t)
	put(t, root, ".gemini/config/skillsets/x/SKILL.md", "keep\n", 0o644)
	m := manifest.New()
	m.Entries[".gemini/config/skillsets/x/SKILL.md"] = manifest.Entry{Source: "elsewhere/x", OutputHash: manifest.Hash([]byte("keep\n")), Mode: "0644"}
	must(t, m.Save(filepath.Join(root, ".gemini/config")))
	p := sync(t, options(root, nil, harness.Antigravity))
	if !exists(root, ".gemini/config/skillsets/x/SKILL.md") || len(p.Targets[0].Warnings) != 1 {
		t.Errorf("an entry whose source is not a hub pair was acted on: %v", p.Targets[0].Warnings)
	}
}

func TestSharedStoreKeepsTheHubsOnlyCopy(t *testing.T) {
	root := hubRoot(t)
	// The skill is synced from a directory named old, then the hub moves to a shared store: its
	// skill becomes a link to the target's copy, which the manifest still credits to old.
	must(t, os.Rename(filepath.Join(root, ".claude/skills/alpha"), filepath.Join(root, ".claude/skills/old")))
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	must(t, os.RemoveAll(filepath.Join(root, ".claude/skills/old")))
	must(t, os.Symlink(filepath.Join(root, ".gemini/config/skills/alpha"), filepath.Join(root, ".claude/skills/alpha")))
	p := sync(t, opts)
	if !exists(root, ".gemini/config/skills/alpha/scripts/run.sh") || !exists(root, ".gemini/config/skills/alpha/SKILL.md") {
		t.Fatalf("the hub's only copy was deleted: %v", states(p))
	}
	if p.Changes() {
		t.Errorf("a skill the target already reads through a link keeps the target out of sync: %v", states(p))
	}
}

func TestUnreadableSkillHoldsCommandsBesideIt(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	// A skill linked to an unmounted volume may be named like a command; until it is back, no
	// command beside it can be checked.
	must(t, os.Symlink(filepath.Join(root, "unmounted"), filepath.Join(root, ".claude/skills/deploy")))
	p := sync(t, opts)
	if got := states(p)[".claude/commands/deploy.md"]; got != Skipped {
		t.Errorf("states = %v; want commands skipped while a skill beside them cannot be read", states(p))
	}
	if !exists(root, ".gemini/config/skills/deploy/SKILL.md") || !exists(root, ".gemini/config/skills/ops-check/SKILL.md") {
		t.Error("earlier command output was deleted")
	}
}

func TestDanglingSkillFileIsHeld(t *testing.T) {
	root := hubRoot(t)
	opts := options(root, nil, harness.Antigravity)
	sync(t, opts)
	entry := filepath.Join(root, ".claude/skills/alpha/SKILL.md")
	must(t, os.Remove(entry))
	must(t, os.Symlink(filepath.Join(root, "moved", "SKILL.md"), entry))
	p := sync(t, opts)
	if !exists(root, ".gemini/config/skills/alpha/SKILL.md") || len(p.Warnings) == 0 {
		t.Error("a skill whose SKILL.md links to nothing lost its output, or nothing was reported")
	}
}

func TestDanglingTargetDirectoryIsRefused(t *testing.T) {
	root := hubRoot(t)
	must(t, os.Symlink(filepath.Join(root, ".agents/skills"), filepath.Join(root, ".gemini/config/skills")))
	p, err := New(options(root, nil))
	must(t, err)
	if p.Targets[0].Err == nil || !strings.Contains(p.Targets[0].Err.Error(), "links to a missing directory") {
		t.Errorf("Antigravity error = %v", p.Targets[0].Err)
	}
}

func TestApplyNeverChangesTheHub(t *testing.T) {
	root := hubRoot(t)
	r := &roots{open: map[string]*os.Root{}, hubDirs: []string{filepath.Join(root, ".claude")}}
	defer r.close()
	base := filepath.Join(root, ".claude/skills")
	if err := r.write(base, &File{Path: ".claude/skills/alpha/SKILL.md", data: []byte("x"), mode: 0o644}, root); err == nil {
		t.Error("a write into the hub was not refused")
	}
	if err := r.remove(base, &File{Path: ".claude/skills/alpha/SKILL.md"}, root); err == nil || !exists(root, ".claude/skills/alpha/SKILL.md") {
		t.Error("a deletion in the hub was not refused")
	}
}
