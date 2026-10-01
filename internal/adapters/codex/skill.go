// Package codex reads and writes Codex customizations. Format facts and their evidence are in
// docs/spec/codex.md.
package codex

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/args"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/skilldir"
)

var target = common.Target{Harness: harness.Codex, Prefix: "$"}

// Codex limits: a longer name does not load; a longer description is cut in the skill catalog.
const (
	maxName        = 64
	maxDescription = 1024
)

// ReadSkill reads a Codex skill directory, including its agents/openai.yaml policy. Codex ignores
// a sidecar it cannot parse, so such a sidecar becomes a note and implicit invocation stays on.
func ReadSkill(dir string) (*ir.Item, error) {
	item, err := common.ReadSkill(dir, harness.Codex, func(*ir.Item, string, *yaml.Node) (bool, error) {
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	for _, r := range item.Resources {
		if r.Path != common.CodexSidecar {
			continue
		}
		implicit, err := implicitInvocation(r.Data)
		if err != nil {
			item.Notes = append(item.Notes, ir.Note{Field: common.CodexSidecar,
				Detail: fmt.Sprintf("does not parse (%v); Codex ignores it, so implicit invocation stays on", err)})
			continue
		}
		item.Invocation.ModelInvocable = implicit
	}
	item.Invocation.ArgumentHint = args.Hint(item.Body, target.Prefix, item.Name)
	return item, nil
}

// WriteSkill renders item as a Codex skill directory.
func WriteSkill(item *ir.Item) ([]ir.Resource, loss.Report, error) {
	var r loss.Report
	if len(item.Name) > maxName {
		return nil, r, fmt.Errorf("skill name %q is longer than Codex's %d-character limit", item.Name, maxName)
	}
	doc := common.ForeignSkill(item, target, &r)
	desc, _ := doc.Scalar("description")
	if desc == "" {
		return nil, r, fmt.Errorf("skill %q has no description and an empty body; Codex does not load a skill without one", item.Name)
	}
	if utf8.RuneCountInString(desc) > maxDescription {
		r.Addf("description", loss.Warn, "longer than %d characters; Codex truncates it in the skill catalog", maxDescription)
	}
	if m, ok := doc.Get("metadata"); ok && m.Kind != yaml.MappingNode {
		doc.Delete("metadata")
		replace(&r, "metadata", loss.Dropped, "Codex requires metadata to be a mapping and would not load the skill")
	}
	if !item.Invocation.UserInvocable {
		r.Add("user-invocable", loss.Dropped, "Codex cannot hide a skill from the $ menu")
	}

	res := common.Resources(item, target, &r)
	implicit := item.Invocation.ModelInvocable
	if item.Source.Harness != harness.Codex && implicit && sidecarDisallows(res) {
		implicit = false
		r.Add("policy.allow_implicit_invocation", loss.Warn,
			"the bundled agents/openai.yaml turns implicit invocation off; kept it although the source allows it")
	}
	res, err := withPolicy(res, implicit)
	if err != nil {
		return nil, r, fmt.Errorf("skill %q: %w", item.Name, err)
	}
	switch {
	case implicit:
	case item.Source.Harness == harness.Codex:
		r.Add("policy.allow_implicit_invocation", loss.Mapped, "")
	case !item.Invocation.ModelInvocable:
		r.Add("disable-model-invocation", loss.Transformed, "became policy.allow_implicit_invocation: false in "+common.CodexSidecar)
	}
	files, err := skilldir.Layout(doc, res)
	return files, r, err
}

// replace swaps every entry for field with a single one.
func replace(r *loss.Report, field string, s loss.Status, detail string) {
	kept := r.Entries[:0]
	for _, e := range r.Entries {
		if e.Field != field {
			kept = append(kept, e)
		}
	}
	r.Entries = kept
	r.Add(field, s, detail)
}

func sidecarDisallows(res []ir.Resource) bool {
	for _, r := range res {
		if r.Path == common.CodexSidecar {
			implicit, err := implicitInvocation(r.Data)
			return err == nil && !implicit
		}
	}
	return false
}

// implicitInvocation reads policy.allow_implicit_invocation, which defaults to true. Only YAML 1.2
// booleans count, as in Codex.
func implicitInvocation(data []byte) (bool, error) {
	root, err := sidecarRoot(data)
	if err != nil {
		return false, err
	}
	policy, ok := lookup(root, "policy")
	if !ok {
		return true, nil
	}
	if policy.Kind != yaml.MappingNode {
		return false, errors.New("policy must be a mapping")
	}
	v, ok := lookup(policy, "allow_implicit_invocation")
	if !ok {
		return true, nil
	}
	return common.Bool("policy.allow_implicit_invocation", v)
}

// sidecarRoot returns the top-level mapping of a sidecar; an empty or null document is an empty
// mapping.
func sidecarRoot(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	top := doc.Content[0]
	switch {
	case top.Kind == yaml.ScalarNode && top.Tag == "!!null":
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	case top.Kind != yaml.MappingNode:
		return nil, errors.New("top level must be a mapping")
	}
	return top, nil
}

// withPolicy sets policy.allow_implicit_invocation in the sidecar, creating the sidecar only when
// implicit invocation must be turned off. An existing sidecar keeps its other fields.
func withPolicy(res []ir.Resource, implicit bool) ([]ir.Resource, error) {
	for i, r := range res {
		if r.Path != common.CodexSidecar {
			continue
		}
		current, err := implicitInvocation(r.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", common.CodexSidecar, err)
		}
		if current == implicit {
			return res, nil
		}
		root, err := sidecarRoot(r.Data)
		if err != nil {
			return nil, err
		}
		policy, ok := lookup(root, "policy")
		if !ok {
			policy = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			set(root, "policy", policy)
		}
		set(policy, "allow_implicit_invocation", common.BoolNode(implicit))
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(root); err != nil {
			return nil, err
		}
		out := append([]ir.Resource(nil), res...)
		out[i].Data = buf.Bytes()
		return out, nil
	}
	if implicit {
		return res, nil
	}
	return append(res, ir.Resource{Path: common.CodexSidecar, Mode: 0o644, Data: []byte(common.GeneratedSidecar)}), nil
}

func lookup(m *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind == yaml.AliasNode && v.Alias != nil {
				v = v.Alias
			}
			return v, true
		}
	}
	return nil, false
}

func set(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
