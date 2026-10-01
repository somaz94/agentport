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
	for _, body := range []string{"no markers\n", Begin + "\nbut no end\n", quoted} {
		if got, ok := Strip(body); ok || got != body {
			t.Errorf("Strip(%q) = %q, %v; want it unchanged", body, got, ok)
		}
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
