package convert

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
)

// claudeOnlyTools are Claude Code tool names distinct enough to find in prose, with what each
// target calls the same tool (docs/spec/); no entry means the target has none. Names that are also
// plain words, such as Read or Edit, are left out.
var claudeOnlyTools = map[string]map[harness.ID]string{
	"AskUserQuestion": {harness.Antigravity: "ask_question", harness.Codex: "request_user_input (root thread and Plan mode only)"},
	"TodoWrite":       {harness.Codex: "update_plan, off by default"},
	"TaskCreate":      {harness.Codex: "update_plan, off by default"},
	"TaskUpdate":      {harness.Codex: "update_plan, off by default"},
	"WebFetch":        {harness.Antigravity: "read_url_content"},
	"WebSearch":       {harness.Antigravity: "search_web", harness.Codex: "web_search, set for the whole session"},
	"NotebookEdit":    {},
}

// checkBody reports what item's body says that the target cannot honour as written: Claude Code
// tool names, and agents it refers to that the target does not have.
func checkBody(item *ir.Item, to harness.ID, opts Options, r *loss.Report) {
	if item.Source.Harness == harness.Claude && to != harness.Claude {
		names := make([]string, 0, len(claudeOnlyTools))
		for name := range claudeOnlyTools {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if !mentions(item.Body, name) {
				continue
			}
			if c := claudeOnlyTools[name][to]; c != "" {
				r.Addf("body", loss.Warn, "mentions Claude Code's %s; %s calls it %s", name, to.Title(), c)
			} else {
				r.Addf("body", loss.Warn, "mentions Claude Code's %s, which %s does not have", name, to.Title())
			}
		}
	}
	if a := opts.Agents; a != nil {
		var missing []string
		for _, name := range a.Known {
			if name != item.Name && !a.Present[name] && !slices.Contains(missing, name) && mentions(item.Body, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			r.Addf("body", loss.Warn, "refers to %s, which %s does not have; convert %s too",
				strings.Join(missing, ", "), to.Title(), plural(len(missing), "it", "them"))
		}
	}
}

// mentions reports whether name occurs in body as a whole word: not inside a longer name made of
// letters, digits, `_` or `-`.
func mentions(body, name string) bool {
	for i := 0; name != ""; {
		j := strings.Index(body[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		if (start == 0 || !nameByte(body[start-1])) && (end == len(body) || !nameByte(body[end])) {
			return true
		}
		i = start + 1
	}
	return false
}

func nameByte(b byte) bool {
	return b == '_' || b == '-' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ReadAgentNames reads the names of harness h's agents below dir, skipping files that do not load.
func ReadAgentNames(h harness.ID, dir string) []string {
	rels, _, err := AgentFiles(h, dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, rel := range rels {
		if item, err := ReadAgent(h, filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			names = append(names, item.Name)
		}
	}
	return names
}
