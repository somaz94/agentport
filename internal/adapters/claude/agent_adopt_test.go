package claude

import (
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/ir"
)

func adoptTools(t *testing.T, front string, removed, added []ir.Capability) (string, bool) {
	t.Helper()
	doc, err := frontmatter.Parse([]byte("---\n" + front + "---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	ok := AdoptTools(doc, removed, added)
	data, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return string(data), ok
}

func TestAdoptTools(t *testing.T) {
	cases := []struct {
		name, front     string
		removed, added  []ir.Capability
		want, wantAfter string
	}{
		{"string list keeps specifiers", "tools: Read, Bash(git *), Grep\n", []ir.Capability{ir.CapSearch}, []ir.Capability{ir.CapWrite},
			"tools: Read, Bash(git *), Write\n", ""},
		{"block list stays a list", "tools:\n  - Read\n  - Grep\n", []ir.Capability{ir.CapRead}, nil,
			"tools:\n  - Grep\n", ""},
		{"every tool denies the removed ones", "name: a\n", []ir.Capability{ir.CapShell}, nil,
			"disallowedTools: Bash, PowerShell\n", ""},
		{"star counts as every tool", "tools: '*'\ndisallowedTools: [Write]\n", []ir.Capability{ir.CapEdit}, nil,
			"disallowedTools:\n  - Write\n  - Edit\n", ""},
		{"granting again lifts a denial", "disallowedTools: Write, WebFetch\n", nil, []ir.Capability{ir.CapWrite},
			"disallowedTools: WebFetch\n", ""},
		{"last denial lifted", "tools: Read\ndisallowedTools: Write\n", nil, []ir.Capability{ir.CapWrite},
			"tools: Read, Write\n", "disallowedTools"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := adoptTools(t, c.front, c.removed, c.added)
			if !ok || !strings.Contains(got, c.want) || c.wantAfter != "" && strings.Contains(got, c.wantAfter) {
				t.Errorf("AdoptTools = %v:\n%s\nwant %q", ok, got, c.want)
			}
		})
	}
	if got, ok := adoptTools(t, "tools: Read\n", []ir.Capability{ir.CapRead}, nil); ok || !strings.Contains(got, "tools: Read\n") {
		t.Errorf("removing the last tool = %v:\n%s; want it refused and left alone", ok, got)
	}
	if got, ok := adoptTools(t, "name: a\n", ir.Capabilities, nil); ok || strings.Contains(got, "disallowedTools") {
		t.Errorf("denying every tool = %v:\n%s; want it refused and left alone", ok, got)
	}
	// What the agent already denies counts: removing the rest would leave nothing.
	front := "disallowedTools: TodoWrite, TaskCreate, TaskGet, TaskList, TaskUpdate\n"
	var rest []ir.Capability
	for _, c := range ir.Capabilities {
		if c != ir.CapPlan {
			rest = append(rest, c)
		}
	}
	if got, ok := adoptTools(t, front, rest, nil); ok || !strings.Contains(got, front) {
		t.Errorf("denying the rest = %v:\n%s; want it refused and left alone", ok, got)
	}
	if got, ok := adoptTools(t, "disallowedTools: [Write]\n", []ir.Capability{ir.CapPlan}, nil); !ok || !strings.Contains(got, "disallowedTools:\n  - Write\n  - TodoWrite") {
		t.Errorf("an unchanged denial keeps its form = %v:\n%s", ok, got)
	}
}
