package convert

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/golden"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/skilldir"
)

// commandCases are fixtures under testdata/commands/<case>/commands/, one command file each.
var commandCases = []string{"basic", "nested", "flags", "hidden"}

func readCommandCase(t *testing.T, name string) *ir.Item {
	t.Helper()
	dir := filepath.Join("testdata", "commands", name, "commands")
	rels, _, err := CommandFiles(dir)
	if err != nil || len(rels) != 1 {
		t.Fatalf("%s: want one command file, got %v, %v", dir, rels, err)
	}
	item, err := ReadCommand(harness.Claude, filepath.Join(dir, filepath.FromSlash(rels[0])), rels[0])
	if err != nil {
		t.Fatal(err)
	}
	item.Source.Path = ""
	return item
}

func TestCommandGolden(t *testing.T) {
	for _, name := range commandCases {
		for _, to := range harness.All {
			t.Run(name+"/"+string(to), func(t *testing.T) {
				res, err := Skill(readCommandCase(t, name), to, Options{})
				if err != nil {
					t.Fatal(err)
				}
				golden.Assert(t, filepath.Join("testdata", "commands", name, string(to)+".golden"), bundle(t, res))
			})
		}
	}
}

func TestCommandModelInvocation(t *testing.T) {
	item := readCommandCase(t, "basic")
	on := Options{ModelInvocableCommands: true}
	res, err := Skill(item, harness.Antigravity, on)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Files[0].Data), "disable-model-invocation") {
		t.Errorf("--model-invocable still turned model invocation off:\n%s", res.Files[0].Data)
	}
	if res, err := Skill(item, harness.Codex, on); err != nil || sidecar(res) != "" {
		t.Errorf("--model-invocable still wrote a Codex policy: %q, %v", sidecar(res), err)
	}
	// A command only the model may start keeps model invocation; otherwise nothing could start it.
	hidden := readCommandCase(t, "hidden")
	for _, to := range []harness.ID{harness.Antigravity, harness.Codex} {
		res, err := Skill(hidden, to, Options{})
		if err != nil || strings.Contains(string(res.Files[0].Data), "disable-model-invocation") || sidecar(res) != "" {
			t.Errorf("%s: a command hidden from the user lost model invocation: %v", to, err)
		}
	}
}

// TestCommandRoundTrip converts a command to each target, reads it back as that target's skill and
// converts it to Claude Code: the body must come back exactly, and with model invocation allowed,
// so must the invocation.
func TestCommandRoundTrip(t *testing.T) {
	src := readCommandCase(t, "basic")
	for _, via := range []harness.ID{harness.Codex, harness.Antigravity} {
		for _, opts := range []Options{{}, {ModelInvocableCommands: true}} {
			out, err := Skill(src, via, opts)
			if err != nil {
				t.Fatal(err)
			}
			mid, err := ReadSkill(via, writeTemp(t, out))
			if err != nil {
				t.Fatal(err)
			}
			back, err := Skill(mid, harness.Claude, Options{})
			if err != nil {
				t.Fatal(err)
			}
			final, err := ReadSkill(harness.Claude, writeTemp(t, back))
			if err != nil {
				t.Fatal(err)
			}
			want := src.Invocation
			want.ModelInvocable = opts.ModelInvocableCommands
			if final.Body != src.Body || final.Invocation != want {
				t.Errorf("via %s %+v: body %q, invocation %+v; want %+v", via, opts, final.Body, final.Invocation, want)
			}
		}
	}
}

func TestReadCommand(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Without a path below a commands directory, the file name alone names the command.
	plain := write("Plain.md", "body\n")
	item, err := ReadCommand(harness.Claude, plain, "")
	if err != nil || item.Name != "plain" || item.Source.Rel != "Plain.md" || item.Kind != ir.KindCommand {
		t.Fatalf("ReadCommand without rel = %+v, %v", item, err)
	}
	res, err := Skill(item, harness.Antigravity, Options{})
	if err != nil || !hasEntry(res.Report, "name", loss.Transformed) || hasEntry(res.Report, "name", loss.Mapped) {
		t.Errorf("a renamed command is not reported as transformed: %+v, %v", res.Report.Entries, err)
	}
	if res.Report.Source != "Claude Code command plain" {
		t.Errorf("report source = %q", res.Report.Source)
	}

	for name, body := range map[string]string{
		"bad-bool.md":    "---\ndisable-model-invocation: maybe\n---\n",
		"unclosed.md":    "---\ndescription: d\n",
		"user-hidden.md": "---\nuser-invocable: [x]\n---\n",
	} {
		if _, err := ReadCommand(harness.Claude, write(name, body), ""); err == nil {
			t.Errorf("%s: ReadCommand succeeded; want an error", name)
		}
	}
	if _, err := ReadCommand(harness.Claude, filepath.Join(dir, "missing.md"), ""); err == nil {
		t.Error("a missing file was read")
	}
	if _, err := ReadCommand(harness.Codex, plain, ""); err == nil || !strings.Contains(err.Error(), "no commands") {
		t.Errorf("Codex command reader: %v", err)
	}
}

// mkfile writes body at the slash-separated rel under root, creating directories.
func mkfile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestShadowed(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		".claude/skills/scan/SKILL.md":         "---\nname: scan\n---\n",
		".claude/skills/dir-name/SKILL.md":     "---\nname: doc\n---\n",
		".claude/skills/no-name/SKILL.md":      "body\n",
		".claude/skills/broken/SKILL.md":       "---\nname: [unclosed\n",
		".claude/skills/synced/sync/SKILL.md":  "---\nname: sync\n---\n",
		".claude/skills/notes/README.md":       "not a skill\n",
		".claude/commands/scan.md":             "",
		".claude/commands/doc.md":              "",
		".claude/commands/no-name.md":          "",
		".claude/commands/broken.md":           "",
		".claude/commands/sync.md":             "",
		".claude/commands/A/b.md":              "",
		".claude/commands/a-b.md":              "",
		".claude/commands/frontend/widget.md":  "",
		".claude/commands/notes.txt":           "",
		".claude/commands/backup.md~":          "",
		".claude/commands/deep/er/one.md":      "",
		".claude/commands/deep-er-one-more.md": "",
	} {
		mkfile(t, root, rel, body)
	}
	d := CommandDirs{Harness: harness.Claude, Commands: filepath.Join(root, ".claude", "commands"), Skills: filepath.Join(root, ".claude", "skills")}
	got, err := Shadowed(d)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"scan.md":    "a skill named scan exists and keeps the name",
		"doc.md":     "a skill named doc exists and keeps the name",
		"no-name.md": "a skill named no-name exists and keeps the name",
		"broken.md":  "a skill named broken exists and keeps the name",
		"A/b.md":     "its skill name a-b is also derived from a-b.md",
		"a-b.md":     "its skill name a-b is also derived from A/b.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Shadowed =\n%v\nwant\n%v", got, want)
	}

	if got, err := Shadowed(CommandDirs{Harness: harness.Claude, Commands: filepath.Join(root, "none"), Skills: filepath.Join(root, "none")}); err != nil || len(got) != 0 {
		t.Errorf("missing directories: %v, %v", got, err)
	}
	notDir := mkfile(t, root, "file", "")
	if _, err := Shadowed(CommandDirs{Harness: harness.Claude, Commands: d.Commands, Skills: notDir}); err == nil {
		t.Error("a skills path that is a file was read as an empty directory")
	}
	if _, err := Shadowed(CommandDirs{Harness: harness.Claude, Commands: filepath.Join(notDir, "sub"), Skills: d.Skills}); err == nil {
		t.Error("an unreadable commands path was read as an empty directory")
	}
}

func TestCommandFiles(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	cmds := filepath.Join(root, "commands")
	mkfile(t, cmds, "top.md", "")
	mkfile(t, cmds, "notes.txt", "")
	mkfile(t, elsewhere, "team/x.md", "")
	mkfile(t, elsewhere, "target.md", "")
	for name, target := range map[string]string{
		"team":      filepath.Join(elsewhere, "team"),
		"alias.md":  filepath.Join(elsewhere, "target.md"),
		"no-ext":    filepath.Join(elsewhere, "target.md"),
		"loop":      cmds,
		"broken.md": filepath.Join(elsewhere, "missing.md"),
	} {
		if err := os.Symlink(target, filepath.Join(cmds, name)); err != nil {
			t.Fatal(err)
		}
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(cmds, linked); err != nil {
		t.Fatal(err)
	}
	// Links are followed and named after themselves; the loop back to the root is not entered again.
	rels, warnings, err := CommandFiles(linked)
	if want := []string{"alias.md", "team/x.md", "top.md"}; err != nil || !reflect.DeepEqual(rels, want) {
		t.Errorf("CommandFiles = %v, %v; want %v", rels, err, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "broken.md") {
		t.Errorf("warnings = %v; want one, for the broken link", warnings)
	}

	if rels, warnings, err := CommandFiles(filepath.Join(root, "none")); err != nil || rels != nil || warnings != nil {
		t.Errorf("a missing directory: %v, %v, %v", rels, warnings, err)
	}
	if _, _, err := CommandFiles(filepath.Join(cmds, "top.md", "sub")); err == nil {
		t.Error("a path below a file was read as an empty directory")
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	locked := filepath.Dir(mkfile(t, cmds, "locked/hidden.md", ""))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if rels, warnings, err := CommandFiles(cmds); err != nil || slices.Contains(rels, "locked/hidden.md") || len(warnings) != 2 {
		t.Errorf("an unreadable subdirectory: %v, %v, %v", rels, warnings, err)
	}
}

// TestShadowedSameFile reaches one file by three paths. Claude Code loads it under the first path its
// walk reaches, which agentport cannot predict, so every path is refused.
func TestShadowedSameFile(t *testing.T) {
	root := t.TempDir()
	cmds := filepath.Join(root, "commands")
	deploy := mkfile(t, cmds, "deploy.md", "")
	mkfile(t, cmds, "other.md", "")
	if err := os.Symlink(deploy, filepath.Join(cmds, "ship.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(deploy, filepath.Join(cmds, "hard.md")); err != nil {
		t.Fatal(err)
	}
	d := CommandDirs{Harness: harness.Claude, Commands: cmds, Skills: filepath.Join(root, "skills")}
	got, err := Shadowed(d)
	const once = "; Claude Code loads it under only one of these names"
	want := map[string]string{
		"deploy.md": "the same file is also listed as hard.md, ship.md" + once,
		"hard.md":   "the same file is also listed as deploy.md, ship.md" + once,
		"ship.md":   "the same file is also listed as deploy.md, hard.md" + once,
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Shadowed = %v, %v; want %v", got, err, want)
	}
	if why, err := CommandBlocked(d, "ship.md"); err != nil || why != want["ship.md"] {
		t.Errorf("CommandBlocked(ship.md) = %q, %v", why, err)
	}
}

func TestCommandBlocked(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "skills/taken/SKILL.md", "---\nname: taken\n---\n")
	mkfile(t, root, "commands/free.md", "")
	mkfile(t, root, "commands/taken.md", "")
	d := CommandDirs{Harness: harness.Claude, Commands: filepath.Join(root, "commands"), Skills: filepath.Join(root, "skills")}
	if why, err := CommandBlocked(d, "free.md"); err != nil || why != "" {
		t.Errorf("free.md: %q, %v", why, err)
	}
	if why, err := CommandBlocked(d, "taken.md"); err != nil || !strings.Contains(why, "a skill named taken") {
		t.Errorf("taken.md: %q, %v", why, err)
	}
	if why, err := CommandBlocked(d, "never/listed.md"); err != nil || !strings.Contains(why, "never reaches it") {
		t.Errorf("a command the listing does not reach: %q, %v", why, err)
	}
	if _, err := CommandBlocked(CommandDirs{Harness: harness.Claude, Commands: d.Commands, Skills: filepath.Join(d.Commands, "free.md")}, "free.md"); err == nil {
		t.Error("an unreadable skills directory was read as an empty one")
	}
	if _, err := CommandBlocked(CommandDirs{Harness: harness.Claude, Commands: filepath.Join(d.Commands, "free.md", "sub"), Skills: d.Skills}, "free.md"); err == nil {
		t.Error("an unreadable commands directory was read as an empty one")
	}
}

// TestCodexRerunFollowsFlags converts a command into the same Codex directory three times: a sidecar
// exactly as agentport generated it follows each run's flags, in both directions.
func TestCodexRerunFollowsFlags(t *testing.T) {
	item := readCommandCase(t, "basic")
	dir := filepath.Join(t.TempDir(), item.Name)
	for _, opts := range []Options{{}, {ModelInvocableCommands: true}, {}} {
		in, err := WithTargetSidecar(item, dir)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Skill(in, harness.Codex, opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := skilldir.Write(dir, res.Files); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(common.CodexSidecar)))
		want := common.GeneratedSidecar
		if opts.ModelInvocableCommands {
			want = common.DefaultSidecar
		}
		if string(got) != want || hasEntry(res.Report, "policy.allow_implicit_invocation", loss.Warn) {
			t.Errorf("%+v: sidecar %q; report %+v", opts, got, res.Report.Entries)
		}
	}
	// The default policy written out says only what the flags say, so no other target copies it.
	codex := &ir.Item{Name: "s", Description: "d", Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true},
		Resources: []ir.Resource{{Path: common.CodexSidecar, Data: []byte(common.DefaultSidecar)}}, Source: ir.Source{Harness: harness.Codex}}
	if res, err := Skill(codex, harness.Claude, Options{}); err != nil || sidecar(res) != "" {
		t.Errorf("the default sidecar reached Claude: %q, %v", sidecar(res), err)
	}
}

func TestDetectCommand(t *testing.T) {
	home := t.TempDir()
	user := mkfile(t, home, ".claude/commands/a/b.md", "")
	d, rel, err := DetectCommand(user, home)
	if err != nil || d.Harness != harness.Claude || rel != "a/b.md" ||
		d.Commands != filepath.Join(home, ".claude", "commands") || d.Skills != filepath.Join(home, ".claude", "skills") {
		t.Errorf("user command: %+v, %q, %v", d, rel, err)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	project := mkfile(t, repo, ".claude/commands/c.md", "")
	d, rel, err = DetectCommand(project, home)
	if err != nil || rel != "c.md" || d.Skills != filepath.Join(repo, ".claude", "skills") {
		t.Errorf("project command: %+v, %q, %v", d, rel, err)
	}
	// Without a home, only the project rule applies, and it matches the user directory too.
	if d, rel, err := DetectCommand(user, ""); err != nil || rel != "a/b.md" || d.Skills != filepath.Join(home, ".claude", "skills") {
		t.Errorf("user command without a home: %+v, %q, %v", d, rel, err)
	}

	// A directory linked into place resolves; the file keeps the name of its link.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(home, ".claude", "commands", "a"), link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "elsewhere.md"), filepath.Join(home, ".claude", "commands", "a", "linked.md")); err != nil {
		t.Fatal(err)
	}
	if _, rel, err := DetectCommand(filepath.Join(link, "linked.md"), home); err != nil || rel != "a/linked.md" {
		t.Errorf("through a linked directory: %q, %v", rel, err)
	}

	outside := mkfile(t, t.TempDir(), "notes/x.md", "")
	if _, _, err := DetectCommand(outside, home); !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), "--from") {
		t.Errorf("outside every commands directory: %v", err)
	}
}
