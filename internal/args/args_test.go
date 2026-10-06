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
	if got := Named("Deploy $env to $region, not $regional.", []string{"env", "region", "zone"}); !reflect.DeepEqual(got, []string{"env", "region"}) {
		t.Errorf("Named = %v", got)
	}
	if got := Named("$x", []string{"\xff"}); got != nil {
		t.Errorf("Named with an invalid UTF-8 name = %v; want nil", got)
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
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, ok := Strip(c.in); !ok || got != c.want {
				t.Errorf("Strip(%q) = %q, %v; want %q, true", c.in, got, ok, c.want)
			}
		})
	}
}

func TestHint(t *testing.T) {
	body := Prepend("Run $ARGUMENTS\n", "/demo [path | --all]", false, nil)
	if got := Hint(body, "/", "demo"); got != "[path | --all]" {
		t.Errorf("Hint = %q", got)
	}
	if got := Hint(Prepend("x", "$demo", false, nil), "$", "demo"); got != "" {
		t.Errorf("Hint without a hint = %q", got)
	}
	if got := Hint(body, "$", "demo"); got != "" {
		t.Errorf("Hint with the wrong prefix = %q", got)
	}
	if got := Hint("plain body", "/", "demo"); got != "" {
		t.Errorf("Hint without a preamble = %q", got)
	}
	if got := Hint(Begin+"\n"+invokedAs+"/demo x", "/", "demo"); got != "" {
		t.Errorf("Hint of a truncated preamble = %q", got)
	}
}

func FuzzPrependStrip(f *testing.F) {
	f.Add("# T\n\n$ARGUMENTS\n", "/x [a]")
	f.Add("", "$x")
	f.Fuzz(func(t *testing.T, body, invocation string) {
		if strings.HasPrefix(body, Begin) || strings.Contains(invocation, "\n") || strings.Contains(invocation, End) {
			t.Skip()
		}
		if back, ok := Strip(Prepend(body, invocation, false, nil)); !ok || back != body {
			t.Errorf("round trip changed %q into %q", body, back)
		}
	})
}
