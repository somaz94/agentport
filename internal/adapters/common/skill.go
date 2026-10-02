// Package common holds the skill conversion rules every target shares.
package common

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/args"
	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/skilldir"
)

// CodexSidecar is the per-skill policy and UI file only Codex reads.
const CodexSidecar = "agents/openai.yaml"

// GeneratedSidecar is the sidecar the Codex writer creates when only the policy needs saying.
// Its content is carried by the IR's invocation flags, so other targets do not need the file.
const GeneratedSidecar = "policy:\n  allow_implicit_invocation: false\n"

// DefaultSidecar states the policy a missing sidecar implies.
const DefaultSidecar = "policy:\n  allow_implicit_invocation: true\n"

// PortableKeys are Agent Skills fields every harness accepts in SKILL.md without acting on them
// differently, so they travel as-is.
var PortableKeys = map[string]bool{"license": true, "compatibility": true, "metadata": true}

// Target describes how a harness invokes a skill.
type Target struct {
	Harness harness.ID
	// Prefix starts a skill invocation in the target's composer: "/" or "$".
	Prefix string
}

// Options are the conversion choices a user can change.
type Options struct {
	// ModelInvocableCommands lets the model start converted commands, as Claude Code does. Off by
	// default, so a mutating workflow never starts from a description match alone.
	ModelInvocableCommands bool
	// Agents, when set, checks the agents a body refers to against those the target has.
	Agents *AgentNames
}

// AgentNames are the agents on either side of a conversion.
type AgentNames struct {
	// Known are the source harness's agent names: the names a body can refer to.
	Known []string
	// Present are the agent names the target harness already has.
	Present map[string]bool
}

// UserOnlyDetail explains, in a loss report, why a converted command lost model invocation. It
// names no switch, since convert and sync turn it back on differently.
const UserOnlyDetail = "converted commands start only when invoked by name, so a workflow never starts from a description match; Claude Code also lets the model start them"

// UserOnly reports whether a command converted with opts must start only when the user invokes it.
// A command the user cannot invoke keeps model invocation, or nothing could start it.
func UserOnly(item *ir.Item, opts Options) bool {
	return item.Kind == ir.KindCommand && !opts.ModelInvocableCommands &&
		item.Invocation.ModelInvocable && item.Invocation.UserInvocable
}

// ReportRename records a command whose skill name differs from its file path, because the path
// nests or has capitals, and reports whether it did.
func ReportRename(item *ir.Item, r *loss.Report) bool {
	if item.Kind != ir.KindCommand || item.Source.Rel == "" || strings.TrimSuffix(item.Source.Rel, ".md") == item.Name {
		return false
	}
	r.Addf("name", loss.Transformed, "derived from the command path %s; a skill name is lowercase and cannot nest", item.Source.Rel)
	return true
}

// ForeignSkill builds the frontmatter and body a non-Claude target starts from — name,
// description, carried-over extensions and the arguments preamble — and records what happened to
// each field. Invocation flags are left to the caller, which knows the target's keys.
func ForeignSkill(item *ir.Item, t Target, r *loss.Report) *frontmatter.Document {
	doc := &frontmatter.Document{}
	body, _ := args.Strip(item.Body)

	doc.SetString("name", item.Name)
	renamed := ReportRename(item, r)
	if err := ir.ValidateSkillName(item.Name); err != nil {
		r.Addf("name", loss.Warn, "%v; %s accepts it, other harnesses may not", err, t.Harness.Title())
	} else if !renamed {
		r.Add("name", loss.Mapped, "")
	}

	desc := item.Description
	switch {
	case desc != "":
		r.Add("description", loss.Mapped, "")
	case skilldir.FirstLine(body) != "":
		desc = skilldir.FirstLine(body)
		r.Add("description", loss.Transformed, "missing; used the first body line, as Claude Code does")
	default:
		r.Add("description", loss.Warn, "missing and the body is empty; the model has nothing to match on")
	}
	if when, ok := item.Extension("when_to_use"); ok {
		if text := Text(when); text != "" {
			desc = strings.TrimSpace(desc + " " + text)
			r.Add("when_to_use", loss.Transformed, "appended to description, as Claude Code does in its listing")
		} else {
			r.Add("when_to_use", loss.Dropped, "empty")
		}
	}
	if desc != "" {
		doc.SetString("description", desc)
	}

	for _, f := range item.Extensions {
		switch {
		case f.Key == "when_to_use":
		case item.Source.Harness == t.Harness || PortableKeys[f.Key]:
			doc.Set(f.Key, f.Value)
			r.Add(f.Key, loss.Mapped, "")
		default:
			r.Addf(f.Key, loss.Dropped, "%s has no counterpart", t.Harness.Title())
		}
	}

	doc.Body = foreignBody(item, body, t, r)
	Notes(item, r)
	return doc
}

// Notes reports what the reader could not carry, as warnings.
func Notes(item *ir.Item, r *loss.Report) {
	for _, n := range item.Notes {
		r.Add(n.Field, loss.Warn, n.Detail)
	}
}

func foreignBody(item *ir.Item, body string, t Target, r *loss.Report) string {
	var names []string
	if n, ok := item.Extension("arguments"); ok {
		names = strings.Fields(strings.NewReplacer("[", " ", "]", " ", ",", " ").Replace(Text(n)))
	}
	named := args.Named(body, names)
	positional := args.Indexed(body)

	switch {
	case args.Uses(body) || len(named) > 0:
		invocation := t.Prefix + item.Name
		if item.Invocation.ArgumentHint != "" {
			invocation += " " + item.Invocation.ArgumentHint
		}
		body = args.Prepend(body, invocation, positional, named)
		r.Addf("body", loss.Transformed, "%s does not substitute $ARGUMENTS; a marked preamble explains it", t.Harness.Title())
		if positional || len(named) > 0 {
			r.Add("body", loss.Approximated, "positional and named arguments are explained, not split, by the target")
		}
		if item.Invocation.ArgumentHint != "" {
			r.Add("argument-hint", loss.Transformed, "moved into the arguments preamble")
		}
	case item.Invocation.ArgumentHint != "":
		r.Addf("argument-hint", loss.Dropped, "%s has no argument hint", t.Harness.Title())
	}
	if args.InjectsShell(body) {
		r.Addf("body", loss.Approximated, "%s does not run !`command` injection; the text stays as written", t.Harness.Title())
	}
	if vars := args.ClaudeVariables(body); len(vars) > 0 {
		r.Addf("body", loss.Approximated, "%s does not expand %s; the text stays as written", t.Harness.Title(), strings.Join(vars, ", "))
	}
	if args.AttachesFiles(body) {
		r.Addf("body", loss.Approximated, "%s does not attach @path references; the text stays as written", t.Harness.Title())
	}
	return body
}

// Resources returns the bundled files for t, byte for byte with their modes. A Codex sidecar that
// only says what the invocation flags already say is not copied to other targets; any other
// sidecar is kept, since a project `.agents/skills` is read by Codex too.
func Resources(item *ir.Item, t Target, r *loss.Report) []ir.Resource {
	var out []ir.Resource
	n := 0
	for _, res := range item.Resources {
		if res.Path == CodexSidecar && t.Harness != harness.Codex {
			if s := string(res.Data); s == GeneratedSidecar && !item.Invocation.ModelInvocable || s == DefaultSidecar && item.Invocation.ModelInvocable {
				r.Add(CodexSidecar, loss.Mapped, "its policy is carried by the invocation flags; the file is not copied")
				continue
			}
			r.Addf(CodexSidecar, loss.Mapped, "kept as a bundled file for Codex; %s ignores it", t.Harness.Title())
		} else {
			n++
		}
		out = append(out, res)
	}
	if n > 0 {
		r.Addf("resources", loss.Mapped, "%d bundled file(s) copied byte for byte with their modes", n)
	}
	return out
}

// Bool reads a YAML 1.2 boolean. yaml.v3 would also accept yes/no/on/off, which Codex rejects.
func Bool(key string, n *yaml.Node) (bool, error) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!bool" {
		switch strings.ToLower(n.Value) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return false, fmt.Errorf("%s must be true or false, got %q", key, n.Value)
}

// BoolNode returns a YAML boolean scalar.
func BoolNode(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(b)}
}

// Text renders a frontmatter value as the text its author wrote. An unquoted `[a | b]` or `{path}`
// parses as a collection, and Claude Code's repair keeps such values as text, so they are turned
// back into the bracketed form instead of being lost.
func Text(n *yaml.Node) string {
	switch n.Kind {
	case yaml.SequenceNode:
		parts := make([]string, len(n.Content))
		for i, c := range n.Content {
			parts[i] = Text(c)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case yaml.MappingNode:
		var parts []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			part := Text(n.Content[i])
			if v := Text(n.Content[i+1]); v != "" {
				part += ": " + v
			}
			parts = append(parts, part)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case yaml.AliasNode:
		if n.Alias != nil {
			return Text(n.Alias)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return ""
		}
		return n.Value
	}
	return ""
}
