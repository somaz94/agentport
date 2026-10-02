// Package antigravity reads and writes Antigravity customizations. Format facts and their evidence
// are in docs/spec/antigravity.md.
package antigravity

import (
	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/args"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/skilldir"
)

var target = common.Target{Harness: harness.Antigravity, Prefix: "/"}

// ReadSkill reads an Antigravity skill directory. An argument hint is recovered from an agentport
// preamble, the only place Antigravity output keeps it.
func ReadSkill(dir string) (*ir.Item, error) {
	item, err := common.ReadSkill(dir, harness.Antigravity, func(item *ir.Item, key string, n *yaml.Node) (bool, error) {
		switch key {
		case "disable-model-invocation":
			b, err := common.Bool(key, n)
			item.Invocation.ModelInvocable = !b
			return true, err
		case "disable-slash-command":
			b, err := common.Bool(key, n)
			item.Invocation.UserInvocable = !b
			return true, err
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	item.Invocation.ArgumentHint = args.Hint(item.Body, target.Prefix, item.Name)
	return item, nil
}

// WriteSkill renders item as an Antigravity skill directory; a command becomes a skill.
func WriteSkill(item *ir.Item, opts common.Options) ([]ir.Resource, loss.Report, error) {
	var r loss.Report
	native := item.Source.Harness == harness.Antigravity
	doc := common.ForeignSkill(item, target, &r)
	switch {
	case !item.Invocation.ModelInvocable:
		doc.Set("disable-model-invocation", common.BoolNode(true))
		r.Add("disable-model-invocation", loss.Mapped, "")
	case common.UserOnly(item, opts):
		doc.Set("disable-model-invocation", common.BoolNode(true))
		r.Add("disable-model-invocation", loss.Transformed, common.UserOnlyDetail)
	}
	if !item.Invocation.UserInvocable {
		doc.Set("disable-slash-command", common.BoolNode(true))
		if native {
			r.Add("disable-slash-command", loss.Mapped, "")
		} else {
			r.Add("user-invocable", loss.Transformed, "became disable-slash-command: true")
		}
	}
	files, err := skilldir.Layout(doc, common.Resources(item, target, &r))
	return files, r, err
}
