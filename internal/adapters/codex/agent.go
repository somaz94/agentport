package codex

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
)

// roleCaps are the capabilities every role has: Codex has no per-role tool list, reads and searches
// files through its shell, and edits with apply_patch (docs/spec/codex.md).
var roleCaps = []ir.Capability{ir.CapRead, ir.CapSearch, ir.CapGlob, ir.CapShell, ir.CapEdit, ir.CapWrite, ir.CapWebSearch, ir.CapDelegate}

// fileCaps all need a file or shell tool; an agent with none of them can lose the shell.
var fileCaps = []ir.Capability{ir.CapRead, ir.CapSearch, ir.CapGlob, ir.CapShell, ir.CapEdit, ir.CapWrite}

// ReadAgent reads the Codex role file path. Codex drops a role without a name, a description or
// developer instructions, so a file missing one is an error here.
func ReadAgent(path string) (*ir.Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	item := &ir.Item{Kind: ir.KindAgent, Tools: &ir.ToolSet{Caps: slices.Clone(roleCaps)}, Source: ir.Source{Harness: harness.Codex, Path: path}}
	for _, k := range md.Keys() {
		if len(k) != 1 {
			continue
		}
		key, v := k[0], raw[k[0]]
		var dst *string
		switch key {
		case "name":
			dst = &item.Name
		case "description":
			dst = &item.Description
		case "developer_instructions":
			dst = &item.Body
		case "model":
			dst = &item.Model
		case "model_reasoning_effort":
			dst = &item.Effort
		}
		if dst != nil {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("%s: %s is not a string", path, key)
			}
			*dst = s
			continue
		}
		if key == "features" {
			if f, ok := v.(map[string]any); ok && f["shell_tool"] == false {
				item.Tools = &ir.ToolSet{Caps: []ir.Capability{ir.CapEdit, ir.CapWrite, ir.CapWebSearch, ir.CapDelegate}}
			}
		}
		n := &yaml.Node{}
		if err := n.Encode(v); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, key, err)
		}
		item.Extensions = append(item.Extensions, ir.Field{Key: key, Value: n})
	}
	if err := checkRole(item); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		item.Notes = append(item.Notes, ir.Note{Field: "role", Detail: "the role file is a symlink, which Codex fails when the role starts"})
	}
	return item, nil
}

func checkRole(item *ir.Item) error {
	switch {
	case strings.TrimSpace(item.Name) == "":
		return errors.New("no name; Codex does not load the role")
	case strings.TrimSpace(item.Description) == "":
		return fmt.Errorf("agent %s has no description; Codex does not load the role", item.Name)
	case strings.TrimSpace(item.Body) == "":
		return fmt.Errorf("agent %s has no instructions; Codex does not load a role without developer_instructions", item.Name)
	}
	return nil
}

// WriteAgent renders item as a Codex role file. Codex drops a role with any key it does not know, so
// a converted role gets only keys docs/spec/codex.md lists as applied; a Codex role keeps its own.
// The result is parsed back before it is returned.
func WriteAgent(item *ir.Item, _ common.Options) ([]ir.Resource, loss.Report, error) {
	var r loss.Report
	if err := checkRole(item); err != nil {
		return nil, r, err
	}
	native := item.Source.Harness == harness.Codex
	var d tomlDoc
	d.str("name", item.Name)
	r.Add("name", loss.Mapped, "")
	d.str("description", item.Description)
	r.Add("description", loss.Mapped, "")

	switch {
	case native && item.Model != "":
		d.str("model", item.Model)
		r.Add("model", loss.Mapped, "")
	case item.Model == "" || strings.EqualFold(item.Model, "inherit"):
	default:
		r.Addf("model", loss.Approximated, "Codex has no %s model; the role uses the session's model", item.Model)
	}
	writeEffort(&d, item, native, &r)

	features := map[string]any{}
	for _, f := range item.Extensions {
		switch {
		case native:
			var v any
			if err := f.Value.Decode(&v); err != nil {
				return nil, r, fmt.Errorf("%s: %w", f.Key, err)
			}
			if f.Key == "features" {
				if m, ok := v.(map[string]any); ok {
					features = m
					r.Add(f.Key, loss.Mapped, "")
					continue
				}
			}
			if err := d.value(f.Key, v); err != nil {
				return nil, r, err
			}
			r.Add(f.Key, loss.Mapped, "")
		case f.Key == "disallowedTools":
			r.Add(f.Key, loss.Dropped, "Codex has no per-role tool list")
		case item.Source.Harness == harness.Antigravity && f.Key == "mainAgent" && common.SubagentOnly(f.Value):
			r.Add(f.Key, loss.Mapped, "false: a Codex role is only ever spawned as a subagent")
		default:
			r.Addf(f.Key, loss.Dropped, "%s-only field", item.Source.Harness.Title())
		}
	}
	d.text("developer_instructions", item.Body)
	r.Add("body", loss.Mapped, "became developer_instructions")
	if !native {
		roleTools(item, features, &r)
	}
	if len(features) > 0 {
		if err := d.value("features", features); err != nil {
			return nil, r, err
		}
	}
	if len(item.Preload) > 0 {
		r.Add("skills", loss.Dropped, "a Codex role cannot preload skills")
	}
	common.Notes(item, &r)

	data := d.bytes()
	if err := verifyRole(data, item); err != nil {
		return nil, r, err
	}
	return []ir.Resource{{Path: item.Name + ".toml", Mode: 0o644, Data: data}}, r, nil
}

// roleTools approximates the source's tool list with the one switch a role has: turning the shell
// off. Codex reads files through the shell, so that only fits an agent with no file tool at all.
func roleTools(item *ir.Item, features map[string]any, r *loss.Report) {
	t := item.Tools
	if t == nil || t.All {
		r.Add("tools", loss.Mapped, "every tool; a Codex role cannot be restricted anyway")
		return
	}
	has := func(c ir.Capability) bool { return t.Has(c) }
	r.Add("tools", loss.Dropped, "Codex has no per-role tool list")
	if !anyOf(has, fileCaps) {
		features["shell_tool"] = false
		r.Add("tools", loss.Transformed, "became [features] shell_tool = false: the agent had no file or shell tool")
		return
	}
	if !has(ir.CapEdit) && !has(ir.CapWrite) {
		r.Add("tools", loss.Warn, "read-only cannot be enforced: every Codex role can edit files with apply_patch")
	}
	if !has(ir.CapShell) {
		r.Add("tools", loss.Warn, "the role keeps a shell the agent did not have, since Codex reads files through it")
	}
}

func anyOf(has func(ir.Capability) bool, caps []ir.Capability) bool {
	for _, c := range caps {
		if has(c) {
			return true
		}
	}
	return false
}

// writeEffort carries the levels both harnesses share and maps `max` to `xhigh`, as Codex's own
// importer does (docs/spec/codex.md).
func writeEffort(d *tomlDoc, item *ir.Item, native bool, r *loss.Report) {
	switch e := strings.ToLower(item.Effort); {
	case e == "":
	case native:
		d.str("model_reasoning_effort", item.Effort)
		r.Add("model_reasoning_effort", loss.Mapped, "")
	case e == "max":
		d.str("model_reasoning_effort", "xhigh")
		r.Add("effort", loss.Transformed, "max became model_reasoning_effort = xhigh, as Codex's own importer maps it")
	case e == "low" || e == "medium" || e == "high" || e == "xhigh":
		d.str("model_reasoning_effort", e)
		r.Add("effort", loss.Mapped, "became model_reasoning_effort")
	case e == "med":
		d.str("model_reasoning_effort", "medium")
		r.Add("effort", loss.Mapped, "became model_reasoning_effort")
	default:
		r.Addf("effort", loss.Dropped, "Codex has no %s reasoning effort", item.Effort)
	}
}

// verifyRole parses the written role and checks that the strings came back exactly.
func verifyRole(data []byte, item *ir.Item) error {
	var got struct {
		Name         string `toml:"name"`
		Description  string `toml:"description"`
		Instructions string `toml:"developer_instructions"`
	}
	if _, err := toml.Decode(string(data), &got); err != nil {
		return fmt.Errorf("the written role does not parse: %w", err)
	}
	if got.Name != item.Name || got.Description != item.Description || got.Instructions != item.Body {
		return errors.New("the written role does not read back as written")
	}
	return nil
}
