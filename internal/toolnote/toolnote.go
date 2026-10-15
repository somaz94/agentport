// Package toolnote handles Claude Code tool names in bodies bound for other harnesses. The body is
// never rewritten, since a rewritten name could not be told apart from one the author wrote; a
// marked note says what each name is called in the target and is removed again, exactly, on the
// way back.
package toolnote

import (
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/args"
	"github.com/somaz94/agentport/internal/harness"
)

// Markers around the note. Matched verbatim when stripping, so they are part of the format.
const (
	Begin = "<!-- agentport:tools:begin -->"
	End   = "<!-- agentport:tools:end -->"
)

// counterparts are Claude Code tool names distinct enough to find in prose, with what each target
// calls the same tool (docs/spec/); no entry means the target has none. Names that are also plain
// words, such as Read or Edit, are left out.
var counterparts = map[string]map[harness.ID]string{
	"AskUserQuestion": {harness.Antigravity: "ask_question", harness.Codex: "request_user_input (root thread and Plan mode only)"},
	"TodoWrite":       {harness.Codex: "update_plan, off by default"},
	"TaskCreate":      {harness.Codex: "update_plan, off by default"},
	"TaskUpdate":      {harness.Codex: "update_plan, off by default"},
	"WebFetch":        {harness.Antigravity: "read_url_content"},
	"WebSearch":       {harness.Antigravity: "search_web", harness.Codex: "web_search, set for the whole session"},
	"NotebookEdit":    {},
}

// Mentioned returns, sorted, the Claude Code tool names body mentions as whole words.
func Mentioned(body string) []string {
	var out []string
	for name := range counterparts {
		if Mentions(body, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Counterpart returns what harness to calls the Claude Code tool name, or "" when it has none.
func Counterpart(name string, to harness.ID) string {
	return counterparts[name][to]
}

// Prepend returns body with a note for harness to on the tools it mentions, after the arguments
// preamble when there is one, since readers expect that preamble at the very start. A body already
// carrying a note gets a fresh one; a body that mentions no tool gets none.
func Prepend(body string, to harness.ID) string {
	body, _ = Strip(body)
	head, rest := splitArgs(body)
	names := Mentioned(rest)
	if len(names) == 0 {
		return body
	}
	var mapped, missing []string
	for _, n := range names {
		if c := Counterpart(n, to); c != "" {
			mapped = append(mapped, "`"+n+"` is "+c)
		} else {
			missing = append(missing, "`"+n+"`")
		}
	}
	var b strings.Builder
	b.WriteString(head + Begin + "\n> **Tools**: this file names Claude Code tools.")
	if len(mapped) > 0 {
		b.WriteString(" In " + to.Title() + ", " + strings.Join(mapped, "; ") + ".")
	}
	if len(missing) > 0 {
		b.WriteString(" " + to.Title() + " has no " + strings.Join(missing, ", ") + "; skip what needs it.")
	}
	b.WriteString("\n" + End + "\n\n" + rest)
	return b.String()
}

// Strip removes a note added by Prepend, with the blank line after it, and reports whether there
// was one. The note counts only at the start of the body or right after the arguments preamble,
// and, as with that preamble, not when a blank line splits it.
func Strip(body string) (string, bool) {
	head, rest := splitArgs(body)
	if !strings.HasPrefix(rest, Begin+"\n") {
		return body, false
	}
	before, after, ok := strings.Cut(rest, "\n"+End)
	if !ok || strings.Contains(strings.Trim(before[len(Begin):], "\n"), "\n\n") {
		return body, false
	}
	return head + strings.TrimPrefix(strings.TrimPrefix(after, "\n"), "\n"), true
}

// splitArgs splits body into its leading arguments preamble, with the blank line after it, and the
// rest.
func splitArgs(body string) (string, string) {
	rest, ok := args.Strip(body)
	if !ok {
		return "", body
	}
	return body[:len(body)-len(rest)], rest
}

// Mentions reports whether name occurs in body as a whole word: not inside a longer name made of
// letters, digits, `_` or `-`.
func Mentions(body, name string) bool {
	for i := 0; name != ""; {
		j := strings.Index(body[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		if (start == 0 || !nameByte(body[start-1])) && (end == len(body) || !nameByte(body[end])) {
			return true
		}
		i = start + 1
	}
	return false
}

func nameByte(b byte) bool {
	return b == '_' || b == '-' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}
