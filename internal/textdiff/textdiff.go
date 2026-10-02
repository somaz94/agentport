// Package textdiff renders the line differences between two texts as a unified diff, for previews.
package textdiff

import (
	"bytes"
	"fmt"
	"strings"
)

// contextLines is how many unchanged lines surround each change.
const contextLines = 3

// maxCells bounds the comparison table; beyond it the middle is shown as replaced wholesale.
const maxCells = 4_000_000

type op struct {
	kind byte // ' ', '-' or '+'
	line string
}

// Unified returns a unified diff from a to b headed by the two names, or "" when they are equal.
func Unified(oldName, newName string, a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}
	ops := diff(lines(a), lines(b))
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hunks(ops) {
		out.WriteString(h)
	}
	return out.String()
}

func lines(data []byte) []string {
	l := strings.SplitAfter(string(data), "\n")
	if l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}

func diff(x, y []string) []op {
	p := 0
	for p < len(x) && p < len(y) && x[p] == y[p] {
		p++
	}
	s := 0
	for s < len(x)-p && s < len(y)-p && x[len(x)-1-s] == y[len(y)-1-s] {
		s++
	}
	var ops []op
	for _, l := range x[:p] {
		ops = append(ops, op{' ', l})
	}
	ops = append(ops, middle(x[p:len(x)-s], y[p:len(y)-s])...)
	for _, l := range x[len(x)-s:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}

// middle compares what lies between the common prefix and suffix by longest common subsequence.
func middle(x, y []string) []op {
	var ops []op
	if len(x)*len(y) > maxCells {
		for _, l := range x {
			ops = append(ops, op{'-', l})
		}
		for _, l := range y {
			ops = append(ops, op{'+', l})
		}
		return ops
	}
	// lcs[i][j] is the common subsequence length of x[i:] and y[j:].
	lcs := make([][]int32, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			ops = append(ops, op{' ', x[i]})
			i, j = i+1, j+1
		case i < len(x) && (j == len(y) || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', x[i]})
			i++
		default:
			ops = append(ops, op{'+', y[j]})
			j++
		}
	}
	return ops
}

// hunks groups changes with their surrounding context, merging hunks whose context overlaps.
func hunks(ops []op) []string {
	var out []string
	for start := 0; start < len(ops); {
		first := start
		for first < len(ops) && ops[first].kind == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		from := max(first-contextLines, 0)
		end := first
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*contextLines {
				end = min(end+contextLines, len(ops))
				break
			}
			end = run
		}
		out = append(out, render(ops, from, end))
		start = end
	}
	return out
}

func render(ops []op, from, to int) string {
	oldStart, newStart := 1, 1
	for _, o := range ops[:from] {
		if o.kind != '+' {
			oldStart++
		}
		if o.kind != '-' {
			newStart++
		}
	}
	var body strings.Builder
	oldLen, newLen := 0, 0
	for _, o := range ops[from:to] {
		if o.kind != '+' {
			oldLen++
		}
		if o.kind != '-' {
			newLen++
		}
		body.WriteByte(o.kind)
		body.WriteString(o.line)
		if !strings.HasSuffix(o.line, "\n") {
			body.WriteString("\n\\ No newline at end of file\n")
		}
	}
	if oldLen == 0 {
		oldStart--
	}
	if newLen == 0 {
		newStart--
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@\n%s", oldStart, oldLen, newStart, newLen, body.String())
}
