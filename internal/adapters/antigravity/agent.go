package antigravity

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

// capTools are the tools each capability becomes, from the runtime-verified table in
// docs/spec/antigravity.md: an unknown name fails the subagent at spawn, so no other name is written.
var capTools = map[ir.Capability][]string{
	ir.CapRead:      {"view_file"},
	ir.CapSearch:    {"grep_search"},
	ir.CapGlob:      {"find_by_name", "list_dir"},
	ir.CapShell:     {"run_command", "manage_task"},
	ir.CapEdit:      {"replace_file_content"},
	ir.CapWrite:     {"write_to_file"},
	ir.CapWebFetch:  {"read_url_content"},
	ir.CapWebSearch: {"search_web"},
	ir.CapAskUser:   {"ask_question"},
	ir.CapDelegate:  {"invoke_subagent"},
}

var toolCaps = map[string]ir.Capability{
	"view_file": ir.CapRead, "grep_search": ir.CapSearch, "find_by_name": ir.CapGlob, "list_dir": ir.CapGlob,
	"run_command": ir.CapShell, "replace_file_content": ir.CapEdit, "multi_replace_file_content": ir.CapEdit,
	"write_to_file": ir.CapWrite, "read_url_content": ir.CapWebFetch, "search_web": ir.CapWebSearch,
	"ask_question": ir.CapAskUser, "invoke_subagent": ir.CapDelegate,
}

// impliedTools grant nothing on their own: send_message is always added, and manage_task only
// manages what run_command started.
var impliedTools = map[string]bool{"send_message": true, "manage_task": true}

// defaultTools are what an agent without a `tools` key gets.
var defaultTools = []string{"send_message", "view_file", "read_url_content", "search_web", "schedule", "generate_image"}

// registered are tools the spec saw offered to a subagent or the main agent without a capability of
// their own; a name outside this set and toolCaps may fail the subagent at spawn.
var registered = map[string]bool{
	"send_message": true, "manage_task": true, "schedule": true, "generate_image": true,
	"define_subagent": true, "manage_subagents": true,
}

var models = map[string]bool{"inherit": true, "flash_lite": true, "flash": true, "pro": true}

// ReadAgent reads the Antigravity agent file path. Each shape docs/spec/antigravity.md records as
// silently dropping the agent is an error here.
func ReadAgent(path string) (*ir.Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := frontmatter.Parse(data)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", path, err)
	case doc.Repaired:
		return nil, fmt.Errorf("%s: frontmatter is not strict YAML, so Antigravity does not load it", path)
	}
	item := &ir.Item{Kind: ir.KindAgent, Body: doc.Body, Source: ir.Source{Harness: harness.Antigravity, Path: path}}
	for _, key := range []string{"name", "description"} {
		if n, ok := doc.Get(key); ok && n.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("%s: %s is not text, so Antigravity does not load the agent", path, key)
		}
	}
	err = common.Fields(item, doc, func(item *ir.Item, key string, n *yaml.Node) (bool, error) {
		switch key {
		case "name":
			item.Name = common.Text(n)
		case "tools":
			if n.Kind != yaml.SequenceNode || slices.ContainsFunc(n.Content, notScalar) {
				return true, errors.New("tools is not a list of names, so Antigravity does not load the agent")
			}
			item.Tools = readTools(n)
		case "model":
			if m := common.Text(n); !models[strings.ToLower(m)] {
				return true, fmt.Errorf("model %q is not inherit, flash_lite, flash or pro, so Antigravity does not load the agent", m)
			}
			item.Model = common.Text(n)
		case "preloadSkills":
			if n.Kind != yaml.SequenceNode {
				return true, errors.New("preloadSkills is not a list, so Antigravity does not load the agent")
			}
			for _, c := range n.Content {
				item.Preload = append(item.Preload, common.Text(c))
			}
		case "mainAgent":
			if !common.IsBool(n) {
				return true, errors.New("mainAgent is not a boolean, so Antigravity does not load the agent")
			}
			return false, nil
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
	if item.Tools == nil {
		def := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, t := range defaultTools {
			def.Content = append(def.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t})
		}
		item.Tools = readTools(def)
		item.Tools.Raw = nil
	}
	return item, nil
}

func notScalar(n *yaml.Node) bool { return n.Kind != yaml.ScalarNode }

func checkAgent(item *ir.Item) error {
	switch {
	case item.Name == "":
		return errors.New("no name; Antigravity does not load it")
	case item.Description == "":
		return fmt.Errorf("agent %s has no description; Antigravity does not load it", item.Name)
	}
	return nil
}

func readTools(n *yaml.Node) *ir.ToolSet {
	t := &ir.ToolSet{Raw: n}
	for _, c := range n.Content {
		name := common.Text(c)
		if granted, ok := toolCaps[name]; ok {
			t.Add(granted)
		} else if !impliedTools[name] {
			t.Unknown = append(t.Unknown, name)
		}
	}
	return t
}

// WriteAgent renders item as an Antigravity agent file. Antigravity drops a broken agent without an
// error, so a converted agent gets only tool names from the verified table and no model; an
// Antigravity agent keeps its own fields.
func WriteAgent(item *ir.Item, _ common.Options) ([]ir.Resource, loss.Report, error) {
	var r loss.Report
	if err := checkAgent(item); err != nil {
		return nil, r, err
	}
	native := item.Source.Harness == harness.Antigravity
	doc := &frontmatter.Document{}
	doc.SetString("name", item.Name)
	r.Add("name", loss.Mapped, "")
	doc.SetString("description", item.Description)
	r.Add("description", loss.Mapped, "")
	writeTools(doc, item, native, &r)

	switch {
	case native && item.Model != "":
		doc.SetString("model", item.Model)
		r.Add("model", loss.Mapped, "")
	case item.Model == "" || strings.EqualFold(item.Model, "inherit"):
	default:
		r.Addf("model", loss.Approximated, "Antigravity has no %s model; the agent inherits the conversation's model", item.Model)
	}
	if item.Effort != "" {
		r.Add("effort", loss.Dropped, "Antigravity has no reasoning effort setting")
	}
	if len(item.Preload) > 0 {
		doc.SetList("preloadSkills", item.Preload)
		r.Add("preloadSkills", loss.Mapped, "")
	}
	for _, f := range item.Extensions {
		if native {
			doc.Set(f.Key, f.Value)
			r.Add(f.Key, loss.Mapped, "")
		} else {
			common.ForeignAgentField(item, f, &r)
		}
	}
	if !native {
		doc.Set("mainAgent", common.BoolNode(false))
		r.Add("mainAgent", loss.Mapped, "false: a subagent stays out of the agent picker, as in the source")
	}
	doc.Body = item.Body
	common.Notes(item, &r)
	data, err := doc.Marshal()
	if err != nil {
		return nil, r, err
	}
	return []ir.Resource{{Path: item.Name + ".md", Mode: 0o644, Data: data}}, r, nil
}

func writeTools(doc *frontmatter.Document, item *ir.Item, native bool, r *loss.Report) {
	t := item.Tools
	if t == nil {
		t = &ir.ToolSet{All: true}
	}
	if native {
		// No Raw means the source had no tools key, which grants Antigravity's default set.
		if t.Raw == nil {
			r.Add("tools", loss.Mapped, "")
			return
		}
		doc.Set("tools", t.Raw)
		r.Add("tools", loss.Mapped, "")
		var odd []string
		for _, name := range t.Unknown {
			if !registered[name] {
				odd = append(odd, name)
			}
		}
		if len(odd) > 0 {
			r.Addf("tools", loss.Warn, "not a known Antigravity tool, so the subagent may fail at spawn: %s", strings.Join(odd, ", "))
		}
		return
	}
	var names []string
	var unmapped []string
	for _, c := range t.Granted() {
		tools, ok := capTools[c]
		if !ok {
			unmapped = append(unmapped, string(c))
			continue
		}
		for _, name := range tools {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	doc.SetList("tools", names)
	if t.All {
		r.Add("tools", loss.Transformed, "every tool, listed by name: without a list Antigravity grants no shell, search or edit tool")
	} else {
		r.Addf("tools", loss.Transformed, "%s tool names written as Antigravity's", item.Source.Harness.Title())
	}
	if len(t.Narrowed) > 0 {
		r.Addf("tools", loss.Approximated, "Antigravity cannot narrow a tool, so these are granted whole: %s", strings.Join(t.Narrowed, ", "))
	}
	if len(t.Unknown) > 0 {
		r.Addf("tools", loss.Dropped, "no Antigravity counterpart: %s", strings.Join(t.Unknown, ", "))
	}
	if len(unmapped) > 0 && !t.All {
		r.Addf("tools", loss.Dropped, "no Antigravity tool for: %s", strings.Join(unmapped, ", "))
	}
}
