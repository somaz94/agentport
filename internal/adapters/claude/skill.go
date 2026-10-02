// Package claude reads and writes Claude Code customizations, the hub format.
package claude

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/args"
	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/skilldir"
)

var target = common.Target{Harness: harness.Claude, Prefix: "/"}

// skillKeys are the SKILL.md keys Claude Code acts on (docs/spec/claude-code.md). A skill directory
// shared verbatim with another harness can carry them, and they are kept on the way back.
var skillKeys = map[string]bool{
	"when_to_use": true, "arguments": true, "allowed-tools": true, "disallowed-tools": true,
	"disallowedTools": true, "model": true, "effort": true, "context": true, "agent": true,
	"background": true, "hooks": true, "paths": true, "shell": true, "version": true,
	"argument-hint": true, "user-invocable": true, "disable-model-invocation": true,
}

// ReadSkill reads a Claude Code skill directory.
func ReadSkill(dir string) (*ir.Item, error) {
	return common.ReadSkill(dir, harness.Claude, claim)
}

// ReadCommand reads the Claude Code command file path. rel, its path below the commands directory,
// names it through ir.CommandSkillName; an empty rel uses the file name.
func ReadCommand(path, rel string) (*ir.Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if rel == "" {
		rel = filepath.Base(path)
	}
	rel = filepath.ToSlash(rel)
	item := &ir.Item{
		Kind:       ir.KindCommand,
		Name:       ir.CommandSkillName(rel),
		Body:       doc.Body,
		Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true},
		Source:     ir.Source{Harness: harness.Claude, Path: path, Rel: rel},
	}
	err = common.Fields(item, doc, func(item *ir.Item, key string, n *yaml.Node) (bool, error) {
		// Carrying either into a skill would give it an effect it never had as a command.
		if key == "name" || key == "paths" {
			item.Notes = append(item.Notes, ir.Note{Field: key, Detail: "Claude Code ignores it in a command; not carried over"})
			return true, nil
		}
		return claim(item, key, n)
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return item, nil
}

// claim handles the invocation keys skills and commands share.
func claim(item *ir.Item, key string, n *yaml.Node) (bool, error) {
	switch key {
	case "argument-hint":
		item.Invocation.ArgumentHint = common.Text(n)
	case "disable-model-invocation":
		b, err := common.Bool(key, n)
		item.Invocation.ModelInvocable = !b
		return true, err
	case "user-invocable":
		b, err := common.Bool(key, n)
		item.Invocation.UserInvocable = b
		return true, err
	default:
		return false, nil
	}
	return true, nil
}

// WriteSkill renders item as a Claude Code skill directory; a command becomes a skill with the
// same behavior, so the options, which only concern other targets, do not apply.
func WriteSkill(item *ir.Item, _ common.Options) ([]ir.Resource, loss.Report, error) {
	var r loss.Report
	native := item.Source.Harness == harness.Claude
	doc := &frontmatter.Document{}
	doc.SetString("name", item.Name)
	if !common.ReportRename(item, &r) {
		r.Add("name", loss.Mapped, "")
	}
	if item.Description != "" {
		doc.SetString("description", item.Description)
		r.Add("description", loss.Mapped, "")
	}
	if item.Invocation.ArgumentHint != "" {
		doc.SetString("argument-hint", item.Invocation.ArgumentHint)
		r.Add("argument-hint", loss.Mapped, "")
	}
	if !item.Invocation.ModelInvocable {
		doc.Set("disable-model-invocation", common.BoolNode(true))
		r.Add("disable-model-invocation", loss.Mapped, "")
	}
	if !item.Invocation.UserInvocable {
		doc.Set("user-invocable", common.BoolNode(false))
		if native {
			r.Add("user-invocable", loss.Mapped, "")
		} else {
			r.Add("user-invocable", loss.Transformed, "the source hid the skill from its command menu")
		}
	}
	for _, f := range item.Extensions {
		if native || skillKeys[f.Key] || common.PortableKeys[f.Key] {
			doc.Set(f.Key, f.Value)
			r.Add(f.Key, loss.Mapped, "")
		} else {
			r.Addf(f.Key, loss.Dropped, "%s-only field", item.Source.Harness.Title())
		}
	}

	body, stripped := args.Strip(item.Body)
	if stripped {
		r.Add("body", loss.Transformed, "removed the agentport arguments preamble; Claude Code substitutes $ARGUMENTS itself")
	}
	doc.Body = body
	common.Notes(item, &r)

	files, err := skilldir.Layout(doc, common.Resources(item, target, &r))
	return files, r, err
}
