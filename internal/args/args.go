// Package args handles Claude Code argument placeholders in bodies bound for harnesses that do not
// substitute them. The body is never rewritten; a marked preamble explains the placeholders and is
// removed again, exactly, on the way back.
package args

import (
	"regexp"
	"strings"
)

// Markers around the preamble. Matched verbatim when stripping, so they are part of the format.
const (
	Begin = "<!-- agentport:args:begin -->"
	End   = "<!-- agentport:args:end -->"
)

// Hint parses the invocation back out from between these two.
const (
	invokedAs     = "> **Arguments**: invoked as `"
	invocationEnd = "`. "
)

// A $ not preceded by a backslash: Claude Code leaves `\$` literal.
const unescaped = `(^|[^\\])\$`

var (
	// An unescaped $ARGUMENTS, $ARGUMENTS[N] or $N, the forms Claude Code substitutes.
	placeholder = regexp.MustCompile(unescaped + `(ARGUMENTS(\[\d+\])?|\d+)`)
	indexed     = regexp.MustCompile(unescaped + `(ARGUMENTS\[\d+\]|\d+)`)
	// Inline !`cmd` at line start or after whitespace, or a fenced block opened with ```! or ~~~!.
	shellInjection = regexp.MustCompile("(?m)((^|\\s)!`[^`\\n]+`|^[ \\t]*(```+|~~~+)!)")
	// Variables Claude Code expands in skill and command bodies.
	claudeVar = regexp.MustCompile(`\$\{CLAUDE_(SKILL_DIR|PROJECT_DIR|SESSION_ID|EFFORT|PLUGIN_ROOT|PLUGIN_DATA)\}`)
	// An @ reference that looks like a path: it has a slash or a file extension.
	fileRef = regexp.MustCompile(`(^|\s)@(~?\.{0,2}/)?[\w.-]*(/[\w./-]+|\.[A-Za-z0-9]{1,5})(\s|$)`)
)

// Uses reports whether body contains a positional or whole-string argument placeholder.
func Uses(body string) bool {
	return placeholder.MatchString(body)
}

// Indexed reports whether body uses positional placeholders, which a target cannot split for it.
func Indexed(body string) bool {
	return indexed.MatchString(body)
}

// Named reports which of the names declared in a skill's `arguments:` appear as `$name` in body.
func Named(body string, names []string) []string {
	var used []string
	for _, n := range names {
		// Skip a name that cannot compile (invalid UTF-8) rather than panic.
		re, err := regexp.Compile(unescaped + regexp.QuoteMeta(n) + `\b`)
		if err == nil && re.MatchString(body) {
			used = append(used, n)
		}
	}
	return used
}

// InjectsShell reports whether body uses Claude Code's shell injection, which no other harness runs.
func InjectsShell(body string) bool {
	return shellInjection.MatchString(body)
}

// ClaudeVariables returns the ${CLAUDE_*} variables body uses, which only Claude Code expands.
func ClaudeVariables(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range claudeVar.FindAllString(body, -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// AttachesFiles reports whether body uses an @path reference, which Claude Code turns into a file
// attachment.
func AttachesFiles(body string) bool {
	return fileRef.MatchString(body)
}

// Prepend returns body with the preamble in front. invocation is how a user starts the item in the
// target, such as `/doc-tidy [path]`; named lists the `arguments:` names the body uses. A body
// already carrying a preamble gets a fresh one.
func Prepend(body, invocation string, positional bool, named []string) string {
	body, _ = Strip(body)
	var b strings.Builder
	b.WriteString(Begin + "\n")
	b.WriteString(invokedAs + invocation + invocationEnd + "The text typed after the skill name is the arguments; wherever this file says `$ARGUMENTS`, use that text.")
	if positional {
		b.WriteString(" `$0`, `$1`, … and `$ARGUMENTS[N]` are those arguments split like shell words, counting from zero.")
	}
	if len(named) > 0 {
		b.WriteString(" `$" + strings.Join(named, "`, `$") + "` are those arguments in that order.")
	}
	b.WriteString("\n" + End + "\n\n")
	b.WriteString(body)
	return b.String()
}

// Strip removes a preamble added by Prepend, with the blank line after it, and reports whether
// there was one. Only a preamble at the very start counts, so a body that quotes the markers keeps
// its text.
func Strip(body string) (string, bool) {
	if !strings.HasPrefix(body, Begin+"\n") {
		return body, false
	}
	// Cut the whole body, not the text after Begin, so an emptied preamble still strips.
	_, rest, ok := strings.Cut(body, "\n"+End)
	if !ok {
		return body, false
	}
	return strings.TrimPrefix(strings.TrimPrefix(rest, "\n"), "\n"), true
}

// Hint recovers the argument hint from a preamble: the invocation text after `prefix+name `.
func Hint(body, prefix, name string) string {
	rest, ok := strings.CutPrefix(body, Begin+"\n"+invokedAs)
	if !ok {
		return ""
	}
	invocation, _, ok := strings.Cut(rest, invocationEnd)
	if !ok {
		return ""
	}
	if hint, ok := strings.CutPrefix(invocation, prefix+name+" "); ok {
		return hint
	}
	return ""
}
