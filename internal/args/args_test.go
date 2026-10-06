package args

import (
	"reflect"
	"strings"
	"testing"
)

func TestDetection(t *testing.T) {
	cases := []struct {
		body                     string
		uses, indexed, injection bool
	}{
		{"Run for $ARGUMENTS.", true, false, false},
		{"First is $ARGUMENTS[0], second $1.", true, true, false},
		{"$0 at line start", true, true, false},
		{`Escaped \$ARGUMENTS stays literal.`, false, false, false},
		{"Plain text.", false, false, false},
		{"Status: !`git status`", false, false, true},
		{"```!\ngit log\n```", false, false, true},
		{"  ~~~~!\ngit log\n~~~~", false, false, true},
		{"Not an injection: a!`b`", false, false, false},
		{"Not one line: !`a\nb`", false, false, false},
		{`\$0 and \$ARGUMENTS[1] stay literal.`, false, false, false},
		{`\$0 is escaped, $1 is not`, true, true, false},
		{"Second line:\n$1", true, true, false},
		{"$ARGUMENTS[x] is not an index", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			if Uses(c.body) != c.uses || Indexed(c.body) != c.indexed || InjectsShell(c.body) != c.injection {
				t.Errorf("Uses=%v Indexed=%v InjectsShell=%v; want %v %v %v",
					Uses(c.body), Indexed(c.body), InjectsShell(c.body), c.uses, c.indexed, c.injection)
			}
		})
	}
}

func TestClaudeOnlyReferences(t *testing.T) {
	body := "Run ${CLAUDE_SKILL_DIR}/scripts/x.sh in ${CLAUDE_PROJECT_DIR}; again ${CLAUDE_SKILL_DIR}. Not ${CLAUDE_OTHER}."
	if got := ClaudeVariables(body); !reflect.DeepEqual(got, []string{"${CLAUDE_SKILL_DIR}", "${CLAUDE_PROJECT_DIR}"}) {
		t.Errorf("ClaudeVariables = %v", got)
	}
	for body, want := range map[string]bool{
		"See @docs/guide.md for details": true,
		"@README.md":                     true,
		"@./notes":                       true,
		"@./notes/a":                     true,
		"mail user@example.com":          false,
		"@Override":                      false,
		"cc @someone please":             false,
	} {
		if got := AttachesFiles(body); got != want {
			t.Errorf("AttachesFiles(%q) = %v; want %v", body, got, want)
		}
	}
}

func TestNamed(t *testing.T) {
	for _, c := range []struct {
		body        string
		names, want []string
	}{
		{"Deploy $env to $region, not $regional.", []string{"env", "region", "zone"}, []string{"env", "region"}},
		// Declaration order, not body order: Prepend's "in that order" sentence relies on it.
		{"$region before $env", []string{"env", "region"}, []string{"env", "region"}},
		{`Escaped \$env, then $zone`, []string{"env", "zone"}, []string{"zone"}},
		// One alternation regexp would consume the v before $zone and miss it.
		{"$env$zone", []string{"env", "zone"}, []string{"env", "zone"}},
		{"$envoy", []string{"env"}, nil},
		{"$x", []string{"\xff"}, nil},
	} {
		t.Run(c.body, func(t *testing.T) {
			if got := Named(c.body, c.names); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Named(%q, %q) = %q; want %q", c.body, c.names, got, c.want)
			}
		})
	}
}

// A literal, not the package constants, so editing a constant cannot move both sides.
func TestPrependBytes(t *testing.T) {
	want := "<!-- agentport:args:begin -->\n" +
		"> **Arguments**: invoked as `/demo [path]`. The text typed after the skill name is the arguments; wherever this file says `$ARGUMENTS`, use that text." +
		" `$0`, `$1`, … and `$ARGUMENTS[N]` are those arguments split like shell words, counting from zero." +
		" `$env`, `$region` are those arguments in that order.\n" +
		"<!-- agentport:args:end -->\n\nbody\n"
	if got := Prepend("body\n", "/demo [path]", true, []string{"env", "region"}); got != want {
		t.Errorf("Prepend =\n%q\nwant\n%q", got, want)
	}
}

func TestPrependStripRoundTrip(t *testing.T) {
	for _, body := range []string{"# Title\n\nRun for $ARGUMENTS.\n", "\nleading blank\n", "", "x"} {
		got := Prepend(body, "/demo [path]", false, nil)
		if !strings.HasPrefix(got, Begin+"\n") || !strings.Contains(got, "`/demo [path]`") || strings.Contains(got, "`$0`") {
			t.Errorf("Prepend =\n%s", got)
		}
		back, ok := Strip(got)
		if !ok || back != body {
			t.Errorf("Strip(Prepend(%q)) = %q, %v; want the original body", body, back, ok)
		}
	}
	got := Prepend("x", "/demo", false, nil)
	if again := Prepend(got, "$demo", true, []string{"env"}); strings.Count(again, Begin) != 1 || !strings.Contains(again, "`$0`") || !strings.Contains(again, "`$env` are those arguments") {
		t.Errorf("re-prepending stacked preambles or lost the positional note:\n%s", again)
	}
	if got := Prepend("x", "/demo [a\n\n\nb]", false, nil); !strings.Contains(got, "`/demo [a\nb]`") {
		t.Errorf("Prepend kept a blank line from the invocation:\n%s", got)
	}
}

func TestStripOnlyAtStart(t *testing.T) {
	quoted := "The markers are `" + Begin + "` and later\n" + End + "\nkeep me\n"
	for _, body := range []string{"no markers\n", Begin + "\nbut no end\n", quoted, Begin + End + "\nbody"} {
		if got, ok := Strip(body); ok || got != body {
			t.Errorf("Strip(%q) = %q, %v; want it unchanged", body, got, ok)
		}
	}
}

func TestStripAcceptsEditedPreambles(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"emptied", Begin + "\n" + End + "\n\nbody", "body"},
		{"end at eof", Begin + "\n" + End, ""},
		{"one newline", Begin + "\nx\n" + End + "\nbody", "body"},
		{"third newline kept", Begin + "\nx\n" + End + "\n\n\nbody", "\nbody"},
		{"text after end kept", Begin + "\nx\n" + End + " note\nbody", " note\nbody"},
		{"wrapped", Begin + "\na\nb\n" + End + "\n\nbody", "body"},
		{"blank line before end", Begin + "\nx\n\n" + End + "\n\nbody", "body"},
		{"blank lines at both edges", Begin + "\n\n> x\n\n\n" + End + "\n\nbody", "body"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, ok := Strip(c.in); !ok || got != c.want {
				t.Errorf("Strip(%q) = %q, %v; want %q, true", c.in, got, ok, c.want)
			}
		})
	}
}

func TestStripKeepsTextWhenEndWasDeleted(t *testing.T) {
	for _, body := range []string{
		Begin + "\n> invoked as\n\n# Title\n\nimportant text\n" + End + "\nmore\n",
		Begin + "\nx\n\ny\n" + End + "\n\nbody",
	} {
		if got, ok := Strip(body); ok || got != body {
			t.Errorf("Strip(%q) = %q, %v; want it unchanged", body, got, ok)
		}
	}
}

func TestHint(t *testing.T) {
	withHint := Prepend("Run $ARGUMENTS\n", "/demo [path | --all]", false, nil)
	for _, c := range []struct{ name, body, prefix, want string }{
		{"hint", withHint, "/", "[path | --all]"},
		{"hint containing invocationEnd", Prepend("x $ARGUMENTS", "/demo [`a`. b]", false, nil), "/", "[`a`. b]"},
		{"reworded sentence", Begin + "\n" + invokedAs + "/demo [a]`. Reworded.\n" + End + "\n\nx", "/", "[a]"},
		{"reworded sentence, body quoting it", strings.Replace(Prepend("`/x`. "+argsSentence+" $ARGUMENTS", "/demo [a]", false, nil), argsSentence, "Reworded.", 1), "/", "[a]"},
		// The fallback cannot tell a hint's invocationEnd from the real one.
		{"reworded sentence, hint with invocationEnd", strings.Replace(Prepend("x $ARGUMENTS", "/demo [`a`. b]", false, nil), argsSentence, "Reworded.", 1), "/", "[`a"},
		{"no hint", Prepend("x", "$demo", false, nil), "$", ""},
		{"wrong prefix", withHint, "$", ""},
		{"no preamble", "plain body", "/", ""},
		{"truncated preamble", Begin + "\n" + invokedAs + "/demo x", "/", ""},
		{"emptied preamble", Begin + "\n" + End + "\n\nx", "/", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Hint(c.body, c.prefix, "demo"); got != c.want {
				t.Errorf("Hint = %q; want %q", got, c.want)
			}
		})
	}
}

func FuzzHint(f *testing.F) {
	f.Add("[path | --all]")
	f.Add("<a>\n<b>")
	f.Add("[`a`. b]")
	f.Fuzz(func(t *testing.T, hint string) {
		if strings.Contains(hint, invocationEnd+argsSentence) || strings.Contains(hint, "\n"+End) || strings.Contains(hint, "\n\n") {
			t.Skip() // Hint would end at the copied sentence or End; Prepend collapses blank lines.
		}
		if got := Hint(Prepend("x", "/demo "+hint, true, []string{"env"}), "/", "demo"); got != hint {
			t.Errorf("Hint(Prepend(%q)) = %q", hint, got)
		}
	})
}

func FuzzPrependStrip(f *testing.F) {
	f.Add("# T\n\n$ARGUMENTS\n", "/x [a]")
	f.Add("", "$x")
	f.Add("# T\n", "/x [a\n\n\nb]")
	f.Fuzz(func(t *testing.T, body, invocation string) {
		if strings.HasPrefix(body, Begin) || strings.Contains(invocation, End) {
			t.Skip()
		}
		if back, ok := Strip(Prepend(body, invocation, false, nil)); !ok || back != body {
			t.Errorf("round trip changed %q into %q", body, back)
		}
	})
}
