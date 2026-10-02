package convert

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/golden"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/skilldir"
)

// agentCases are fixtures under testdata/agents/<case>/, one agent file each.
var agentCases = map[string]harness.ID{
	"claude-reviewer":    harness.Claude,
	"claude-runner":      harness.Claude,
	"claude-all":         harness.Claude,
	"claude-narrowed":    harness.Claude,
	"claude-web":         harness.Claude,
	"claude-denied":      harness.Claude,
	"claude-native":      harness.Claude,
	"antigravity-helper": harness.Antigravity,
	"codex-writer":       harness.Codex,
}

func readAgentCase(t *testing.T, name string) *ir.Item {
	t.Helper()
	h := agentCases[name]
	item, err := ReadAgent(h, filepath.Join("testdata", "agents", name, "agent"+AgentExt(h)))
	if err != nil {
		t.Fatal(err)
	}
	item.Source.Path = ""
	return item
}

func TestAgentGolden(t *testing.T) {
	for name := range agentCases {
		for _, to := range harness.All {
			t.Run(name+"/"+string(to), func(t *testing.T) {
				res, err := Agent(readAgentCase(t, name), to, Options{})
				if err != nil {
					t.Fatal(err)
				}
				golden.Assert(t, filepath.Join("testdata", "agents", name, string(to)+".golden"), bundle(t, res))
			})
		}
	}
}

// TestAgentRoundTrip writes each Claude agent to Antigravity, reads it back and converts it to
// Claude Code again: the prompt, the description and every capability Antigravity has must survive.
func TestAgentRoundTrip(t *testing.T) {
	for name, from := range agentCases {
		if from != harness.Claude {
			continue
		}
		t.Run(name, func(t *testing.T) {
			src := readAgentCase(t, name)
			mid := writeAgentTemp(t, src, harness.Antigravity)
			back := writeAgentTemp(t, mid, harness.Claude)
			want := slices.DeleteFunc(slices.Clone(src.Tools.Granted()), func(c ir.Capability) bool { return c == ir.CapPlan })
			if back.Body != src.Body || back.Description != src.Description || !sameCaps(back.Tools.Granted(), want) {
				t.Errorf("came back as %q / %q / %v; want %q / %q / %v", back.Description, back.Body, back.Tools.Granted(), src.Description, src.Body, want)
			}
			if !reflect.DeepEqual(back.Preload, src.Preload) {
				t.Errorf("preloaded skills %v; want %v", back.Preload, src.Preload)
			}
		})
	}
	// Through Codex the prompt survives; a role grants every file tool, so the list does not.
	src := readAgentCase(t, "claude-reviewer")
	if back := writeAgentTemp(t, writeAgentTemp(t, src, harness.Codex), harness.Claude); back.Body != src.Body {
		t.Errorf("Codex round trip changed the body to %q", back.Body)
	}
}

// writeAgentTemp converts item to harness to, writes it and reads it back.
func writeAgentTemp(t *testing.T, item *ir.Item, to harness.ID) *ir.Item {
	t.Helper()
	res, err := Agent(item, to, Options{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := skilldir.Write(dir, res.Files); err != nil {
		t.Fatal(err)
	}
	back, err := ReadAgent(to, filepath.Join(dir, res.Files[0].Path))
	if err != nil {
		t.Fatalf("%s did not read back: %v\n%s", to, err, res.Files[0].Data)
	}
	return back
}

func sameCaps(a, b []ir.Capability) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func TestClaudeAgentTools(t *testing.T) {
	cases := map[string]struct {
		tools      string
		all        bool
		caps       []ir.Capability
		unknown    []string
		narrowedTo int
	}{
		"omitted":           {tools: "", all: true},
		"star":              {tools: "tools: '*'", all: true},
		"star in a list":    {tools: "tools: [Read, '*']", all: true},
		"empty":             {tools: "tools: ''"},
		"null":              {tools: "tools:"},
		"list":              {tools: "tools: [Read, Grep]", caps: []ir.Capability{ir.CapRead, ir.CapSearch}},
		"spaces":            {tools: "tools: Bash  Read", caps: []ir.Capability{ir.CapShell, ir.CapRead}},
		"tab":               {tools: "tools: \"Read\\tGrep\"", unknown: []string{"Read\tGrep"}},
		"block":             {tools: "tools: |\n  Read\n  Grep", unknown: []string{"Read\nGrep"}},
		"nested list":       {tools: "tools: [[Read, Grep]]"},
		"number":            {tools: "tools: [Read, 1]", caps: []ir.Capability{ir.CapRead}},
		"not a string":      {tools: "tools: 3"},
		"paren flag":        {tools: "tools: 'Bash(a (b) c), Read'", caps: []ir.Capability{ir.CapShell, ir.CapRead}, unknown: []string{"c)"}, narrowedTo: 1},
		"aliases":           {tools: "tools: LS, MultiEdit, Task, KillShell", caps: []ir.Capability{ir.CapGlob, ir.CapEdit, ir.CapDelegate}, unknown: []string{"KillShell"}},
		"specifier":         {tools: "tools: 'Agent(a, b), Read'", caps: []ir.Capability{ir.CapDelegate, ir.CapRead}, narrowedTo: 1},
		"denied":            {tools: "tools: Read, Grep\ndisallowedTools: Grep(x), Bash", caps: []ir.Capability{ir.CapRead}},
		"denied powershell": {tools: "tools: Bash, PowerShell\ndisallowedTools: PowerShell", caps: []ir.Capability{ir.CapShell}},
		"denied todo":       {tools: "disallowedTools: TodoWrite", all: true},
		"denied from all": {tools: "disallowedTools: [Bash, Agent]",
			caps: []ir.Capability{ir.CapRead, ir.CapSearch, ir.CapGlob, ir.CapEdit, ir.CapWrite, ir.CapWebFetch, ir.CapWebSearch, ir.CapAskUser, ir.CapPlan}},
		"denied mcp only": {tools: "disallowedTools: mcp__x", all: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "a.md")
			if err := os.WriteFile(p, []byte("---\nname: a\ndescription: d\n"+c.tools+"\n---\nbody\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			item, err := ReadAgent(harness.Claude, p)
			if err != nil {
				t.Fatal(err)
			}
			got := item.Tools
			if got.All != c.all || !slices.Equal(got.Caps, c.caps) || !slices.Equal(got.Unknown, c.unknown) || len(got.Narrowed) != c.narrowedTo {
				t.Errorf("tools = %+v; want all=%v caps=%v unknown=%v narrowed=%d", got, c.all, c.caps, c.unknown, c.narrowedTo)
			}
		})
	}
}

func TestAgentReadErrors(t *testing.T) {
	cases := map[string]struct {
		h    harness.ID
		body string
		want string
	}{
		"claude no name":          {harness.Claude, "---\ndescription: d\n---\n", "no name"},
		"claude colon":            {harness.Claude, "---\nname: a:b\ndescription: d\n---\n", "contains :"},
		"claude fullwidth colon":  {harness.Claude, "---\nname: a\uFF1Ab\ndescription: d\n---\n", "contains :"},
		"claude dash":             {harness.Claude, "---\nname: -a\ndescription: d\n---\n", "starts with -"},
		"claude no description":   {harness.Claude, "---\nname: a\n---\n", "no description"},
		"claude bad frontmatter":  {harness.Claude, "---\nname: a\n", "closing"},
		"claude numeric name":     {harness.Claude, "---\nname: 123\ndescription: d\n---\n", "name is not a string"},
		"claude list description": {harness.Claude, "---\nname: a\ndescription: [a, b]\n---\n", "description is not a string"},
		"claude math colon":       {harness.Claude, "---\nname: a\u2a74b\ndescription: d\n---\n", "contains :"},
		"ag list description":     {harness.Antigravity, "---\nname: a\ndescription: [a, b]\n---\n", "description is not text"},
		"ag nested tools":         {harness.Antigravity, "---\nname: a\ndescription: d\ntools: [[view_file]]\n---\n", "not a list of names"},
		"ag scalar preload":       {harness.Antigravity, "---\nname: a\ndescription: d\npreloadSkills: doc-style\n---\n", "preloadSkills"},
		"ag quoted mainAgent":     {harness.Antigravity, "---\nname: a\ndescription: d\nmainAgent: 'false'\n---\n", "mainAgent"},
		"ag not strict":           {harness.Antigravity, "---\nname: a\ndescription: Use when: x\n---\n", "strict YAML"},
		"ag tools string":         {harness.Antigravity, "---\nname: a\ndescription: d\ntools: view_file, grep_search\n---\n", "not a list"},
		"ag unknown model":        {harness.Antigravity, "---\nname: a\ndescription: d\nmodel: opus\n---\n", "model"},
		"ag no description":       {harness.Antigravity, "---\nname: a\n---\n", "no description"},
		"ag no name":              {harness.Antigravity, "---\ndescription: d\n---\n", "no name"},
		"ag bad frontmatter":      {harness.Antigravity, "---\nname: a\n", "closing"},
		"codex no instructions":   {harness.Codex, "name = \"a\"\ndescription = \"d\"\n", "developer_instructions"},
		"codex blank name":        {harness.Codex, "name = \" \"\ndescription = \"d\"\ndeveloper_instructions = \"x\"\n", "no name"},
		"codex no description":    {harness.Codex, "name = \"a\"\ndeveloper_instructions = \"x\"\n", "no description"},
		"codex name not a string": {harness.Codex, "name = 3\ndescription = \"d\"\ndeveloper_instructions = \"x\"\n", "not a string"},
		"codex not toml":          {harness.Codex, "name = \n", "toml"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "a"+AgentExt(c.h))
			if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadAgent(c.h, p); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("ReadAgent = %v; want an error containing %q", err, c.want)
			}
		})
	}
	for _, h := range harness.All {
		if _, err := ReadAgent(h, filepath.Join(t.TempDir(), "missing"+AgentExt(h))); err == nil {
			t.Errorf("%s read a missing file", h)
		}
	}
	if _, err := ReadAgent("cursor", "x.md"); err == nil {
		t.Error("an unknown harness read an agent")
	}
}

func TestAgentWriteErrors(t *testing.T) {
	good := ir.Item{Kind: ir.KindAgent, Name: "a", Description: "d", Body: "b", Source: ir.Source{Harness: harness.Claude}}
	for name, mutate := range map[string]func(*ir.Item){
		"no name":        func(i *ir.Item) { i.Name = "" },
		"no description": func(i *ir.Item) { i.Description = "" },
		"unsafe name":    func(i *ir.Item) { i.Name = "../a" },
	} {
		for _, to := range harness.All {
			item := good
			mutate(&item)
			if _, err := Agent(&item, to, Options{}); err == nil {
				t.Errorf("%s to %s: converted", name, to)
			}
		}
	}
	empty := good
	empty.Body = "  \n"
	if _, err := Agent(&empty, harness.Codex, Options{}); err == nil || !strings.Contains(err.Error(), "developer_instructions") {
		t.Errorf("an empty prompt became a Codex role: %v", err)
	}
	if _, err := Agent(&good, "cursor", Options{}); err == nil {
		t.Error("an unknown target was accepted")
	}
	// Claude Code would not load this name, so the Claude writer refuses it.
	colon := good
	colon.Name = "a:b"
	if _, err := Agent(&colon, harness.Claude, Options{}); err == nil {
		t.Error("a name with a colon became a Claude Code agent")
	}
}

// TestCodexRoleStrings writes prompts that need every kind of TOML string and reads them back.
func TestCodexRoleStrings(t *testing.T) {
	for name, body := range map[string]string{
		"plain":                "Do the task.\n",
		"leading newline":      "\nStarts blank.\n",
		"backslashes":          `match \d+ and C:\path` + "\n",
		"triple quotes":        "say '''hi''' and \"\"\"there\"\"\"\n",
		"trailing quote":       "ends with a quote'",
		"control characters":   "bell \a and carriage\rreturn\n",
		"no trailing newline":  "one line",
		"unicode":              "é 한글 \u2028 sep",
		"double quotes ending": `ends "quoted"`,
	} {
		t.Run(name, func(t *testing.T) {
			item := &ir.Item{Kind: ir.KindAgent, Name: "r", Description: "d \"q\" \\ \n x", Body: body, Source: ir.Source{Harness: harness.Claude}}
			back := writeAgentTemp(t, item, harness.Codex)
			if back.Body != body || back.Description != item.Description {
				t.Errorf("came back as %q / %q", back.Body, back.Description)
			}
		})
	}
}

func TestCodexKeepsItsOwnFields(t *testing.T) {
	src := readAgentCase(t, "codex-writer")
	back := writeAgentTemp(t, src, harness.Codex)
	if back.Model != src.Model || back.Effort != src.Effort || !reflect.DeepEqual(back.Extensions, src.Extensions) || !sameCaps(back.Tools.Caps, src.Tools.Caps) {
		t.Errorf("Codex to Codex changed the role:\n%+v\nwant\n%+v", back, src)
	}
}

func TestAgentEffort(t *testing.T) {
	for effort, want := range map[string]string{"max": "xhigh", "MED": "medium", "low": "low", "7": ""} {
		item := &ir.Item{Kind: ir.KindAgent, Name: "a", Description: "d", Body: "b", Effort: effort, Source: ir.Source{Harness: harness.Claude}}
		if got := writeAgentTemp(t, item, harness.Codex).Effort; got != want {
			t.Errorf("Claude effort %s became %q in Codex; want %q", effort, got, want)
		}
	}
	for effort, want := range map[string]string{"xhigh": "xhigh", "max": "max", "minimal": ""} {
		item := &ir.Item{Kind: ir.KindAgent, Name: "a", Description: "d", Body: "b", Effort: effort, Source: ir.Source{Harness: harness.Codex}}
		if got := writeAgentTemp(t, item, harness.Claude).Effort; got != want {
			t.Errorf("Codex effort %s became %q in Claude Code; want %q", effort, got, want)
		}
	}
}

func TestAntigravityAgentTools(t *testing.T) {
	// Without a tools key Antigravity grants its default set, which reads files but has no shell.
	p := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(p, []byte("---\nname: a\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := ReadAgent(harness.Antigravity, p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(item.Tools.Caps, []ir.Capability{ir.CapRead, ir.CapWebFetch, ir.CapWebSearch}) || !slices.Equal(item.Tools.Unknown, []string{"schedule", "generate_image"}) {
		t.Errorf("default tools = %+v", item.Tools)
	}
	res, err := Agent(item, harness.Antigravity, Options{})
	if err != nil || strings.Contains(string(res.Files[0].Data), "tools:") {
		t.Errorf("an omitted tools key was written back: %v\n%s", err, res.Files[0].Data)
	}
	// A native list with a name outside the verified table is kept, with a warning.
	odd := filepath.Join(t.TempDir(), "b.md")
	if err := os.WriteFile(odd, []byte("---\nname: b\ndescription: d\ntools: [view_file, command_status]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := ReadAgent(harness.Antigravity, odd)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := Agent(b, harness.Antigravity, Options{}); err != nil || !hasEntry(res.Report, "tools", loss.Warn) {
		t.Errorf("an unknown Antigravity tool kept without a warning: %+v, %v", res.Report.Entries, err)
	}
}

func TestDetectAgent(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	cases := map[string]harness.ID{
		filepath.Join(home, ".claude", "agents", "team", "a.md"):        harness.Claude,
		filepath.Join(home, ".gemini", "config", "agents", "a.md"):      harness.Antigravity,
		filepath.Join(home, ".codex", "agents", "sub", "a.toml"):        harness.Codex,
		filepath.Join(repo, ".claude", "agents", "a.md"):                harness.Claude,
		filepath.Join(repo, ".agents", "agents", "a.md"):                harness.Antigravity,
		filepath.Join(repo, ".codex", "agents", "a.toml"):               harness.Codex,
		filepath.Join(home, ".gemini", "config", "agents", "x", "a.md"): "",
		filepath.Join(home, ".claude", "agents", "a.toml"):              "",
		filepath.Join(home, "notes", "a.md"):                            "",
	}
	for p, want := range cases {
		got, dir, rel, err := DetectAgent(p, home)
		if got != want || (want == "") != (err != nil) || want != "" && filepath.Join(dir, filepath.FromSlash(rel)) != p {
			t.Errorf("DetectAgent(%s) = %q, %q, %q, %v; want %q", p, got, dir, rel, err, want)
		}
	}
	// A directory linked into an agents directory resolves.
	if err := os.MkdirAll(filepath.Join(home, ".claude", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(home, ".claude", "agents"), link); err != nil {
		t.Fatal(err)
	}
	if got, _, rel, err := DetectAgent(filepath.Join(link, "a.md"), home); got != harness.Claude || rel != "a.md" {
		t.Errorf("through a linked directory: %q, %q, %v", got, rel, err)
	}
}

func TestAgentFiles(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "claude/team/a.md", "")
	mkfile(t, root, "claude/b.md", "")
	mkfile(t, root, "ag/a.md", "")
	mkfile(t, root, "ag/nested/b.md", "")
	mkfile(t, root, "ag/notes.txt", "")
	mkfile(t, root, "codex/sub/a.toml", "")
	mkfile(t, root, "codex/b.md", "")
	for h, want := range map[harness.ID][]string{
		harness.Claude:      {"b.md", "team/a.md"},
		harness.Antigravity: {"a.md"},
		harness.Codex:       {"sub/a.toml"},
	} {
		dir := map[harness.ID]string{harness.Claude: "claude", harness.Antigravity: "ag", harness.Codex: "codex"}[h]
		got, _, err := AgentFiles(h, filepath.Join(root, dir))
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("AgentFiles(%s) = %v, %v; want %v", h, got, err, want)
		}
		if got, _, err := AgentFiles(h, filepath.Join(root, "none")); err != nil || got != nil {
			t.Errorf("AgentFiles(%s) of a missing directory = %v, %v", h, got, err)
		}
		if _, _, err := AgentFiles(h, filepath.Join(root, "claude", "b.md", "sub")); err == nil {
			t.Errorf("AgentFiles(%s) read a path below a file", h)
		}
	}
}

func TestCheckBody(t *testing.T) {
	item := &ir.Item{Kind: ir.KindAgent, Name: "planner", Description: "d", Source: ir.Source{Harness: harness.Claude},
		Body: "Ask with AskUserQuestion, then hand off to `code-reviewer` and to release-runner.\nNot code-reviewers, not my-planner.\n"}
	agents := &AgentNames{Known: []string{"planner", "code-reviewer", "release-runner", "code-reviewer", "unused"}, Present: map[string]bool{"release-runner": true}}

	var r loss.Report
	checkBody(item, harness.Antigravity, Options{Agents: agents}, &r)
	want := []loss.Entry{
		{Field: "body", Status: loss.Warn, Detail: "mentions Claude Code's AskUserQuestion; Antigravity calls it ask_question"},
		{Field: "body", Status: loss.Warn, Detail: "refers to code-reviewer, which Antigravity does not have; convert it too"},
	}
	if !reflect.DeepEqual(r.Entries, want) {
		t.Errorf("entries =\n%+v\nwant\n%+v", r.Entries, want)
	}

	r = loss.Report{}
	checkBody(item, harness.Codex, Options{Agents: &AgentNames{Known: []string{"code-reviewer", "release-runner"}}}, &r)
	if len(r.Entries) != 2 || !strings.Contains(r.Entries[1].Detail, "code-reviewer, release-runner, which Codex does not have; convert them too") {
		t.Errorf("Codex entries = %+v", r.Entries)
	}

	// Tool names are Claude Code's, so only a Claude source going elsewhere is checked; without agent
	// lists there is nothing to check references against.
	r = loss.Report{}
	checkBody(item, harness.Claude, Options{}, &r)
	native := *item
	native.Source.Harness = harness.Antigravity
	checkBody(&native, harness.Codex, Options{}, &r)
	if len(r.Entries) != 0 {
		t.Errorf("unexpected entries %+v", r.Entries)
	}
	r = loss.Report{}
	none := *item
	none.Body = "NotebookEdit only."
	checkBody(&none, harness.Antigravity, Options{}, &r)
	if len(r.Entries) != 1 || r.Entries[0].Detail != "mentions Claude Code's NotebookEdit, which Antigravity does not have" {
		t.Errorf("entries = %+v", r.Entries)
	}
}

func TestReadAgentNames(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "a.md", "---\nname: alpha\ndescription: d\n---\n")
	mkfile(t, dir, "team/b.md", "---\nname: beta\ndescription: d\n---\n")
	mkfile(t, dir, "broken.md", "---\nname: [\n")
	if got := ReadAgentNames(harness.Claude, dir); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("ReadAgentNames = %v", got)
	}
	if got := ReadAgentNames(harness.Claude, filepath.Join(dir, "a.md", "sub")); got != nil {
		t.Errorf("ReadAgentNames below a file = %v", got)
	}
}

// TestAntigravityReadsWhatItLoads covers shapes Antigravity loads although they look wrong.
func TestAntigravityReadsWhatItLoads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.md")
	body := "---\nname: 123\ndescription: d\nmodel: Flash\nmainAgent: yes\ntools: [view_file, 1]\npreloadSkills: [1]\n---\nbody\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := ReadAgent(harness.Antigravity, p)
	if err != nil || item.Name != "123" || item.Model != "Flash" || !slices.Equal(item.Preload, []string{"1"}) || !slices.Equal(item.Tools.Unknown, []string{"1"}) {
		t.Fatalf("ReadAgent = %+v, %v", item, err)
	}
	res, err := Agent(item, harness.Antigravity, Options{})
	if err != nil || !strings.Contains(string(res.Files[0].Data), "mainAgent: yes") || !strings.Contains(string(res.Files[0].Data), "model: Flash") {
		t.Errorf("written back as %q, %v", res.Files[0].Data, err)
	}
}

func TestCodexRoleTablesAndKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.toml")
	role := "name = \"r\"\ndescription = \"d\"\ndeveloper_instructions = \"x\"\n\"odd key\" = 1\nratios = [1.5, 2.5]\n\n[skills]\ninclude_instructions = false\n\n[features]\napps = false\n"
	if err := os.WriteFile(p, []byte(role), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := ReadAgent(harness.Codex, p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Agent(src, harness.Codex, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Files[0].Data)
	if !strings.Contains(out, "\"odd key\" = 1") || !strings.Contains(out, "[skills]") || !strings.Contains(out, "[features]") || !hasEntry(res.Report, "features", loss.Mapped) {
		t.Errorf("role written as\n%s\n%+v", out, res.Report.Entries)
	}
	back := writeAgentTemp(t, src, harness.Codex)
	if !reflect.DeepEqual(back.Extensions, src.Extensions) {
		t.Errorf("extensions changed:\n%+v\nwant\n%+v", back.Extensions, src.Extensions)
	}
	// features without shell_tool shaped nothing, so another target drops it.
	if res, err := Agent(src, harness.Claude, Options{}); err != nil || !hasEntry(res.Report, "features", loss.Dropped) {
		t.Errorf("features to Claude Code: %+v, %v", res.Report.Entries, err)
	}
}

func TestCodexSymlinkedRole(t *testing.T) {
	dir := t.TempDir()
	real := mkfile(t, dir, "real.toml", "name = \"r\"\ndescription = \"d\"\ndeveloper_instructions = \"x\"\n")
	link := filepath.Join(dir, "link.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	item, err := ReadAgent(harness.Codex, link)
	if err != nil || len(item.Notes) != 1 || !strings.Contains(item.Notes[0].Detail, "symlink") {
		t.Errorf("a symlinked role read as %+v, %v", item, err)
	}
}

func TestAgentDuplicates(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "a.md", "---\nname: dup\ndescription: d\n---\n")
	mkfile(t, dir, "team/b.md", "---\nname: dup\ndescription: d\n---\n")
	mkfile(t, dir, "c.md", "---\nname: solo\ndescription: d\n---\n")
	mkfile(t, dir, "broken.md", "---\nname: [\n")
	if err := os.Symlink(filepath.Join(dir, "c.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	got, err := AgentDuplicates(harness.Claude, dir)
	want := map[string]string{
		"a.md":      "team/b.md is also named dup, and Claude Code loads only one of them",
		"team/b.md": "a.md is also named dup, and Claude Code loads only one of them",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("AgentDuplicates =\n%v, %v\nwant\n%v", got, err, want)
	}
	if _, err := AgentDuplicates(harness.Claude, filepath.Join(dir, "a.md", "sub")); err == nil {
		t.Error("a path below a file was read as a directory")
	}
}

func TestAntigravityMainAgentSpellings(t *testing.T) {
	for value, ok := range map[string]bool{"yes": true, "No": true, "ON": true, "off": true, "y": true, "N": true, "True": true,
		"'no'": true, "": true, "'false'": false, "1": false, "maybe": false, "tRuE": false, "oFF": false} {
		p := filepath.Join(t.TempDir(), "a.md")
		if err := os.WriteFile(p, []byte("---\nname: a\ndescription: d\nmainAgent: "+value+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadAgent(harness.Antigravity, p); (err == nil) != ok {
			t.Errorf("mainAgent: %s read with %v; want accepted=%v", value, err, ok)
		}
	}
}

func TestAntigravityAgentFilesWarnsOnBrokenLinks(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "a.md", "")
	if err := os.Symlink(filepath.Join(dir, "missing.md"), filepath.Join(dir, "b.md")); err != nil {
		t.Fatal(err)
	}
	// Antigravity reads only Markdown, so a broken link to anything else is not worth a warning.
	if err := os.Symlink(filepath.Join(dir, "missing.txt"), filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	rels, warnings, err := AgentFiles(harness.Antigravity, dir)
	if err != nil || !slices.Equal(rels, []string{"a.md"}) || len(warnings) != 1 {
		t.Errorf("AgentFiles = %v, %v, %v", rels, warnings, err)
	}
}

func TestClaudeAgentDetails(t *testing.T) {
	read := func(front string) *ir.Item {
		t.Helper()
		p := filepath.Join(t.TempDir(), "a.md")
		if err := os.WriteFile(p, []byte("---\nname: a\n"+front+"\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		item, err := ReadAgent(harness.Claude, p)
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	// A star anywhere in disallowedTools makes Claude Code ignore the list.
	if item := read("description: d\ndisallowedTools: '*, Bash'"); !item.Tools.All {
		t.Errorf("disallowedTools with * removed tools: %+v", item.Tools)
	}
	if item := read(`description: 'line one\nline two'`); item.Description != "line one\nline two" {
		t.Errorf("description = %q", item.Description)
	}
	// From another harness, inherit is written out: without it Claude Code tries its subagent default.
	ag := &ir.Item{Kind: ir.KindAgent, Name: "a", Description: "d", Body: "b", Model: "inherit", Source: ir.Source{Harness: harness.Antigravity}}
	if res, err := Agent(ag, harness.Claude, Options{}); err != nil || !strings.Contains(string(res.Files[0].Data), "model: inherit") {
		t.Errorf("inherit from Antigravity: %v\n%s", err, res.Files[0].Data)
	}
}

// TestSubagentOnlyCarriesOver converts an Antigravity agent kept out of the picker: every Claude Code
// agent and Codex role already is, so nothing is lost.
func TestSubagentOnlyCarriesOver(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(p, []byte("---\nname: a\ndescription: d\nmainAgent: false\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := ReadAgent(harness.Antigravity, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []harness.ID{harness.Claude, harness.Codex} {
		if res, err := Agent(item, to, Options{}); err != nil || !hasEntry(res.Report, "mainAgent", loss.Mapped) || hasEntry(res.Report, "mainAgent", loss.Dropped) {
			t.Errorf("%s: %+v, %v", to, res.Report.Entries, err)
		}
	}
}
