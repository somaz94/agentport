package toolnote

import (
	"reflect"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/args"
	"github.com/somaz94/agentport/internal/harness"
)

func TestMentioned(t *testing.T) {
	body := "Ask with AskUserQuestion, track with TodoWrite. Not MyWebFetch or WebSearch-like, but WebSearch."
	if got, want := Mentioned(body), []string{"AskUserQuestion", "TodoWrite", "WebSearch"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Mentioned = %v; want %v", got, want)
	}
	if Mentioned("Read and Edit are plain words.") != nil {
		t.Error("plain words counted as tools")
	}
	if Counterpart("AskUserQuestion", harness.Antigravity) != "ask_question" || Counterpart("TodoWrite", harness.Antigravity) != "" {
		t.Error("Counterpart does not follow the table")
	}
}

func TestPrepend(t *testing.T) {
	body := "Ask with AskUserQuestion. Then NotebookEdit.\n"
	got := Prepend(body, harness.Antigravity)
	want := Begin + "\n> **Tools**: this file names Claude Code tools. In Antigravity, `AskUserQuestion` is ask_question." +
		" Antigravity has no `NotebookEdit`; skip what needs it.\n" + End + "\n\n" + body
	if got != want {
		t.Errorf("Prepend =\n%s\nwant\n%s", got, want)
	}
	if again := Prepend(got, harness.Antigravity); again != got {
		t.Errorf("a second Prepend added another note:\n%s", again)
	}
	if back, ok := Strip(got); !ok || back != body {
		t.Errorf("Strip = %q, %v; want the body back", back, ok)
	}

	if plain := "Nothing to map.\n"; Prepend(plain, harness.Codex) != plain {
		t.Error("a body naming no tool got a note")
	}
	if got := Prepend("Use WebSearch.", harness.Codex); !strings.Contains(got, " In Codex, `WebSearch` is web_search, set for the whole session.\n") ||
		strings.Contains(got, "has no") {
		t.Errorf("Codex note = %q", got)
	}
	// A note for another target is replaced, not stacked.
	if got := Prepend(Prepend(body, harness.Codex), harness.Antigravity); got != want {
		t.Errorf("re-targeted note =\n%s", got)
	}
}

func TestPrependKeepsArgumentsFirst(t *testing.T) {
	withArgs := args.Prepend("Deploy $ARGUMENTS, asking with AskUserQuestion.\n", "/deploy <svc>", false, nil)
	got := Prepend(withArgs, harness.Antigravity)
	if !strings.HasPrefix(got, args.Begin+"\n") {
		t.Fatalf("the arguments preamble is no longer first:\n%s", got)
	}
	if args.Hint(got, "/", "deploy") != "<svc>" {
		t.Error("the argument hint no longer reads back")
	}
	rest, _ := args.Strip(got)
	if !strings.HasPrefix(rest, Begin+"\n") {
		t.Errorf("the note does not follow the arguments preamble:\n%s", got)
	}
	if back, ok := Strip(got); !ok || back != withArgs {
		t.Errorf("Strip = %q, %v; want the arguments preamble and body back", back, ok)
	}
}

func TestStripRefuses(t *testing.T) {
	note := Prepend("AskUserQuestion here.\n", harness.Antigravity)
	for name, body := range map[string]string{
		"no note":         "AskUserQuestion here.\n",
		"quoted later":    "Intro.\n\n" + note,
		"end deleted":     strings.Replace(note, End, "", 1),
		"split by blank":  strings.Replace(note, "\n"+End, "\n\n> added by hand\n"+End, 1),
		"begin not alone": strings.Replace(note, Begin+"\n", Begin+" ", 1),
	} {
		if got, ok := Strip(body); ok || got != body {
			t.Errorf("%s: Strip = %q, %v; want the body unchanged", name, got, ok)
		}
	}
	// Blank lines at the note's edges are formatting, not a deleted End.
	padded := strings.Replace(note, Begin+"\n", Begin+"\n\n", 1)
	if got, ok := Strip(padded); !ok || got != "AskUserQuestion here.\n" {
		t.Errorf("padded: Strip = %q, %v", got, ok)
	}
}

func TestMentions(t *testing.T) {
	for _, c := range []struct {
		body string
		want bool
	}{
		{"AskUserQuestion", true},
		{"use `AskUserQuestion`.", true},
		{"AskUserQuestions", false},
		{"my-AskUserQuestion", false},
		{"AskUserQuestion_x and then AskUserQuestion", true},
		{"", false},
	} {
		if got := Mentions(c.body, "AskUserQuestion"); got != c.want {
			t.Errorf("Mentions(%q) = %v; want %v", c.body, got, c.want)
		}
	}
	if Mentions("anything", "") {
		t.Error("an empty name matched")
	}
}
