package claude

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
)

// toolAliases maps old tool names to current ones (docs/spec/claude-code.md). MultiEdit and LS no
// longer exist; agentport reads them as Edit and Glob.
var toolAliases = map[string]string{
	"Task": "Agent", "KillShell": "TaskStop", "KillBash": "TaskStop", "MultiEdit": "Edit", "LS": "Glob",
	"ListMcpResources": "ListMcpResourcesTool", "ReadMcpResource": "ReadMcpResourceTool",
}

var toolCaps = map[string]ir.Capability{
	"Read": ir.CapRead, "Grep": ir.CapSearch, "Glob": ir.CapGlob, "Bash": ir.CapShell, "PowerShell": ir.CapShell,
	"Edit": ir.CapEdit, "Write": ir.CapWrite, "WebFetch": ir.CapWebFetch, "WebSearch": ir.CapWebSearch,
	"Agent": ir.CapDelegate, "AskUserQuestion": ir.CapAskUser, "TodoWrite": ir.CapPlan,
	"TaskCreate": ir.CapPlan, "TaskGet": ir.CapPlan, "TaskList": ir.CapPlan, "TaskUpdate": ir.CapPlan,
}

// capTools is the Claude Code tool each capability is written as.
var capTools = map[ir.Capability]string{
	ir.CapRead: "Read", ir.CapSearch: "Grep", ir.CapGlob: "Glob", ir.CapShell: "Bash", ir.CapEdit: "Edit",
	ir.CapWrite: "Write", ir.CapWebFetch: "WebFetch", ir.CapWebSearch: "WebSearch", ir.CapDelegate: "Agent",
	ir.CapAskUser: "AskUserQuestion", ir.CapPlan: "TodoWrite",
}

// ReadAgent reads the Claude Code agent file path. Claude Code skips an agent its frontmatter rules
// reject (docs/spec/claude-code.md), so such a file is an error here.
func ReadAgent(path string) (*ir.Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, key := range []string{"name", "description"} {
		if n, ok := doc.Get(key); ok && !isString(n) {
			return nil, fmt.Errorf("%s: %s is not a string, so Claude Code does not load the agent", path, key)
		}
	}
	item := &ir.Item{Kind: ir.KindAgent, Body: doc.Body, Source: ir.Source{Harness: harness.Claude, Path: path}}
	var tools, denied *yaml.Node
	err = common.Fields(item, doc, func(item *ir.Item, key string, n *yaml.Node) (bool, error) {
		switch key {
		case "name":
			item.Name = n.Value
		case "tools":
			tools = n
		case "disallowedTools":
			// Kept as an extension too, for other harnesses to report as folded into the tool list.
			denied = n
			return false, nil
		case "model":
			item.Model = strings.TrimSpace(common.Text(n))
		case "effort":
			item.Effort = common.Text(n)
		case "skills":
			item.Preload = entries(n)
		default:
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkAgent(item); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Claude Code turns the two characters \n in a description into a line break.
	item.Description = strings.ReplaceAll(item.Description, `\n`, "\n")
	item.Tools = toolSet(tools, denied)
	for _, key := range doc.Keys() {
		n, _ := doc.Get(key)
		item.Native = append(item.Native, ir.Field{Key: key, Value: n})
	}
	return item, nil
}

func isString(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!str"
}

func checkAgent(item *ir.Item) error {
	switch {
	case item.Name == "":
		return errors.New("no name; Claude Code does not load it")
	// Claude Code tests the NFKC form, which turns these compatibility characters into `:`.
	case strings.HasPrefix(item.Name, "-"), strings.ContainsAny(item.Name, ":：﹕︓⩴"):
		return fmt.Errorf("agent name %q starts with - or contains :, so Claude Code does not load it", item.Name)
	case item.Description == "":
		return fmt.Errorf("agent %s has no description; Claude Code does not load it", item.Name)
	}
	return nil
}

// everyTool is what an agent granted every tool (no `tools` key, or `*`) starts from when
// disallowedTools takes some away: each tool with a capability, PowerShell left out as Windows-only.
var everyTool = []string{"Read", "Grep", "Glob", "Bash", "Edit", "Write", "WebFetch", "WebSearch", "Agent",
	"AskUserQuestion", "TodoWrite", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate"}

// toolSet computes what an agent may call, as Claude Code does: every tool without a `tools` key or
// with `*` in it, then each tool named in disallowedTools removed, a specifier there still removing
// the whole tool.
func toolSet(tools, denied *yaml.Node) *ir.ToolSet {
	var listed []string
	if tools != nil {
		listed = entries(tools)
	}
	all := tools == nil || slices.Contains(listed, "*")
	deny := map[string]bool{}
	var denies []string
	if denied != nil {
		denies = entries(denied)
	}
	// Claude Code ignores a disallowedTools list that holds `*`.
	if !slices.Contains(denies, "*") {
		for _, e := range denies {
			name, _ := splitEntry(e)
			deny[canonical(name)] = true
		}
	}
	if all && len(deny) == 0 {
		return &ir.ToolSet{All: true, Raw: tools}
	}
	if all {
		listed = everyTool
	}
	t := &ir.ToolSet{Raw: tools}
	for _, e := range listed {
		name, narrowed := splitEntry(e)
		if deny[canonical(name)] {
			continue
		}
		c, ok := toolCaps[canonical(name)]
		switch {
		case !ok:
			t.Unknown = append(t.Unknown, e)
		case narrowed:
			t.Narrowed = append(t.Narrowed, e)
			t.Add(c)
		default:
			t.Add(c)
		}
	}
	// A denial that removes no capability, such as one MCP tool or TodoWrite alone, leaves All.
	if all && len(t.Caps) == len(ir.Capabilities) {
		return &ir.ToolSet{All: true, Raw: tools}
	}
	return t
}

// entries reads a string or list value into entries the way Claude Code does: list items that are
// not strings are ignored, and so is a value that is neither.
func entries(n *yaml.Node) []string {
	var out []string
	switch {
	case n.Kind == yaml.SequenceNode:
		for _, c := range n.Content {
			if isString(c) {
				out = append(out, split(c.Value)...)
			}
		}
	case isString(n):
		out = split(n.Value)
	}
	return out
}

// split separates entries at a comma or a space outside parentheses, which hold a specifier such
// as `Bash(git push *)`. Claude Code splits on nothing else: a tab stays inside an entry.
func split(s string) []string {
	var out []string
	var cur strings.Builder
	inParens := false
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, t)
		}
		cur.Reset()
	}
	for _, r := range s {
		switch {
		case r == '(':
			inParens = true
		case r == ')':
			inParens = false
		case !inParens && (r == ',' || r == ' '):
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// splitEntry returns an entry's tool name and whether a specifier narrows it.
func splitEntry(e string) (string, bool) {
	if i := strings.IndexByte(e, '('); i > 0 && strings.HasSuffix(e, ")") {
		return e[:i], true
	}
	return e, false
}

func canonical(name string) string {
	if a, ok := toolAliases[name]; ok {
		return a
	}
	return name
}

// WriteAgent renders item as a Claude Code agent file. An agent read from Claude Code is written
// back as it was; one from another harness gets Claude Code's names for what carries over.
func WriteAgent(item *ir.Item, _ common.Options) ([]ir.Resource, loss.Report, error) {
	var r loss.Report
	if err := checkAgent(item); err != nil {
		return nil, r, err
	}
	doc := &frontmatter.Document{Body: item.Body}
	if item.Source.Harness == harness.Claude && item.Native != nil {
		for _, f := range item.Native {
			doc.Set(f.Key, f.Value)
			r.Add(f.Key, loss.Mapped, "")
		}
	} else {
		foreignAgent(doc, item, &r)
	}
	common.Notes(item, &r)
	data, err := doc.Marshal()
	if err != nil {
		return nil, r, err
	}
	return []ir.Resource{{Path: item.Name + ".md", Mode: 0o644, Data: data}}, r, nil
}

func foreignAgent(doc *frontmatter.Document, item *ir.Item, r *loss.Report) {
	doc.SetString("name", item.Name)
	r.Add("name", loss.Mapped, "")
	doc.SetString("description", item.Description)
	r.Add("description", loss.Mapped, "")
	writeTools(doc, item, r)
	switch {
	case item.Model == "":
	case strings.EqualFold(item.Model, "inherit"):
		// Written out: without a model Claude Code tries CLAUDE_CODE_SUBAGENT_MODEL first.
		doc.SetString("model", "inherit")
		r.Add("model", loss.Mapped, "")
	default:
		r.Addf("model", loss.Approximated, "Claude Code has no %s model; the agent gets Claude Code's default subagent model", item.Model)
	}
	switch e := strings.ToLower(item.Effort); e {
	case "":
	case "low", "medium", "high", "xhigh", "max":
		doc.SetString("effort", e)
		r.Add("effort", loss.Mapped, "")
	default:
		r.Addf("effort", loss.Dropped, "Claude Code has no %s effort", item.Effort)
	}
	if len(item.Preload) > 0 {
		doc.SetList("skills", item.Preload)
		r.Add("skills", loss.Mapped, "")
	}
	for _, f := range item.Extensions {
		common.ForeignAgentField(item, f, r)
	}
}

func writeTools(doc *frontmatter.Document, item *ir.Item, r *loss.Report) {
	t := item.Tools
	if t == nil || t.All {
		r.Add("tools", loss.Mapped, "every tool")
		return
	}
	names := make([]string, 0, len(t.Caps))
	for _, c := range t.Caps {
		names = append(names, capTools[c])
	}
	doc.SetString("tools", strings.Join(names, ", "))
	r.Addf("tools", loss.Transformed, "%s tool names written as Claude Code's", item.Source.Harness.Title())
	if len(t.Unknown) > 0 {
		r.Addf("tools", loss.Dropped, "no Claude Code counterpart: %s", strings.Join(t.Unknown, ", "))
	}
}
