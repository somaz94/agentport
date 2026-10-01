package harness

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]ID{
		"claude":      Claude,
		"Claude-Code": Claude,
		" codex ":     Codex,
		"antigravity": Antigravity,
		"AGY":         Antigravity,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Parse("cursor"); err == nil || !strings.Contains(err.Error(), "claude, codex, antigravity") {
		t.Errorf("Parse(cursor) error = %v; want the list of valid names", err)
	}
}

func TestTitle(t *testing.T) {
	cases := map[ID]string{Claude: "Claude Code", Codex: "Codex", Antigravity: "Antigravity", "other": "other"}
	for id, want := range cases {
		if got := id.Title(); got != want {
			t.Errorf("%q.Title() = %q; want %q", id, got, want)
		}
	}
}

func TestNames(t *testing.T) {
	if got := strings.Join(Names(), ","); got != "claude,codex,antigravity" {
		t.Errorf("Names() = %s", got)
	}
}
