package convert

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/toolnote"
)

// checkBody reports what item's body says that the target cannot honour as written: Claude Code
// tool names, and agents it refers to that the target does not have.
func checkBody(item *ir.Item, to harness.ID, opts Options, r *loss.Report) {
	if item.Source.Harness == harness.Claude && to != harness.Claude {
		for _, name := range toolnote.Mentioned(item.Body) {
			if c := toolnote.Counterpart(name, to); c != "" {
				r.Addf("body", loss.Transformed, "mentions Claude Code's %s; a marked note says %s calls it %s", name, to.Title(), c)
			} else {
				r.Addf("body", loss.Warn, "mentions Claude Code's %s, which %s does not have; a marked note says so", name, to.Title())
			}
		}
	}
	if a := opts.Agents; a != nil {
		var missing []string
		for _, name := range a.Known {
			if name != item.Name && !a.Present[name] && !slices.Contains(missing, name) && toolnote.Mentions(item.Body, name) {
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

// withNote returns item as harness to should receive it: any tools note an earlier conversion left
// is removed, and a Claude Code body bound elsewhere gets a fresh one. The caller's item is not
// changed, so reports and adopt still see the source body.
func withNote(item *ir.Item, to harness.ID, r *loss.Report) *ir.Item {
	body, stripped := toolnote.Strip(item.Body)
	if stripped && to == harness.Claude {
		r.Add("body", loss.Transformed, "removed the agentport tools note")
	}
	if item.Source.Harness == harness.Claude && to != harness.Claude {
		body = toolnote.Prepend(body, to)
	}
	if body == item.Body {
		return item
	}
	cp := *item
	cp.Body = body
	return &cp
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
