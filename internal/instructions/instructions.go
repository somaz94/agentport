// Package instructions keeps a target's hand-condensed instruction file (AGENTS.md) honest against
// the hub's (CLAUDE.md). Instructions are never generated, so nothing would otherwise notice a hub
// section changing: agentport records the version of every hub section the target file was last
// reviewed against, and reports the sections that changed since.
package instructions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/safefs"
)

// File is the review record's name, kept beside the manifest under a target root.
const File = "instructions.json"

// SchemaVersion is bumped when the on-disk format changes incompatibly.
const SchemaVersion = 1

// Preamble names the text before the first `## ` heading.
const Preamble = "(before the first heading)"

// Section is one `## ` section of an instruction file and a hash of its text.
type Section struct {
	Heading string
	Hash    string
}

// Sections splits a Markdown file at its `## ` headings outside fenced code. Blank lines, trailing
// spaces and `<br/>` spacer lines are left out of each hash, so respacing a section is not a change.
// A repeated heading gets a `(2)`, `(3)` … suffix so every section keeps its own entry.
func Sections(data []byte) []Section {
	type part struct {
		heading string
		body    bytes.Buffer
	}
	parts := []*part{{heading: Preamble}}
	var fence string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), len(data)+1)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if marker := fenceMarker(line); marker != "" {
			switch {
			case fence == "":
				fence = marker
			case strings.HasPrefix(marker, fence):
				fence = ""
			}
		}
		if fence == "" && strings.HasPrefix(line, "## ") {
			parts = append(parts, &part{heading: strings.TrimSpace(line[3:])})
			continue
		}
		if line == "" || line == "<br/>" {
			continue
		}
		cur := parts[len(parts)-1]
		cur.body.WriteString(line)
		cur.body.WriteByte('\n')
	}
	seen := map[string]int{}
	var out []Section
	for _, p := range parts {
		if p.heading == Preamble && p.body.Len() == 0 {
			continue
		}
		seen[p.heading]++
		heading := p.heading
		if n := seen[p.heading]; n > 1 {
			heading = fmt.Sprintf("%s (%d)", p.heading, n)
		}
		out = append(out, Section{Heading: heading, Hash: manifest.Hash(p.body.Bytes())})
	}
	return out
}

// fenceMarker returns the backtick or tilde run that opens or closes a fenced code block on line.
func fenceMarker(line string) string {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 {
		return ""
	}
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(s) && s[n] == c {
			n++
		}
		if n >= 3 {
			return s[:n]
		}
	}
	return ""
}

// Record is what a target file was last reviewed against: the hub file and its section hashes.
type Record struct {
	Version int `json:"version"`
	// Hub is the slash-separated path of the hub file, relative to the home directory.
	Hub      string            `json:"hub"`
	Sections map[string]string `json:"sections"`
}

// Path returns the review record's path for root, a target harness's configuration directory.
func Path(root string) string {
	return filepath.Join(root, manifest.Dir, File)
}

// Load reads the review record under root; it returns nil when none has been written.
func Load(root string) (*Record, error) {
	data, err := os.ReadFile(Path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read review record: %w", err)
	}
	r := &Record{}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(root), err)
	}
	if r.Version != SchemaVersion {
		return nil, fmt.Errorf("%s has schema version %d, this build reads %d", Path(root), r.Version, SchemaVersion)
	}
	return r, nil
}

// Save writes the review record under root without following a link out of it.
func (r *Record) Save(root string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode review record: %w", err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := safefs.WriteFile(dir, filepath.Join(manifest.Dir, File), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write review record: %w", err)
	}
	return nil
}

// Diff lists hub sections by what happened to them since the review record was written.
type Diff struct {
	Added   []string `json:"added,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// Empty reports whether no section changed.
func (d Diff) Empty() bool {
	return len(d.Added)+len(d.Changed)+len(d.Removed) == 0
}

// Len counts the sections that changed.
func (d Diff) Len() int {
	return len(d.Added) + len(d.Changed) + len(d.Removed)
}

func (d Diff) String() string {
	var parts []string
	for _, g := range []struct {
		verb  string
		names []string
	}{{"added", d.Added}, {"changed", d.Changed}, {"removed", d.Removed}} {
		if len(g.names) == 0 {
			continue
		}
		quoted := make([]string, len(g.names))
		for i, n := range g.names {
			quoted[i] = fmt.Sprintf("%q", n)
		}
		parts = append(parts, g.verb+" "+strings.Join(quoted, ", "))
	}
	return strings.Join(parts, "; ")
}

// Compare lists how current differs from what r recorded: added and changed sections in document
// order, removed ones sorted.
func Compare(r *Record, current []Section) Diff {
	var d Diff
	now := map[string]bool{}
	for _, s := range current {
		now[s.Heading] = true
		switch old, ok := r.Sections[s.Heading]; {
		case !ok:
			d.Added = append(d.Added, s.Heading)
		case old != s.Hash:
			d.Changed = append(d.Changed, s.Heading)
		}
	}
	for h := range r.Sections {
		if !now[h] {
			d.Removed = append(d.Removed, h)
		}
	}
	sort.Strings(d.Removed)
	return d
}

// Pair is a target harness's instruction file and the hub file it condenses, at the user scope.
// Paths are relative to the home directory.
type Pair struct {
	Target harness.ID
	Hub    string
	File   string
	// Root is the target's configuration directory, where the review record is kept.
	Root string
}

// Pairs returns a pair for each of targets, or for every harness but hub when targets is empty.
// A harness with no user-level instruction file is left out.
func Pairs(hub harness.ID, targets []harness.ID) ([]Pair, error) {
	hl, err := paths.For(hub)
	if err != nil {
		return nil, err
	}
	hubFile, err := hl.Path(paths.ScopeUser, ir.KindInstructions, "")
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		for _, h := range harness.All {
			if h != hub {
				targets = append(targets, h)
			}
		}
	}
	var out []Pair
	for _, h := range targets {
		l, err := paths.For(h)
		if err != nil {
			return nil, err
		}
		if !l.Supports(paths.ScopeUser, ir.KindInstructions) {
			continue
		}
		file, err := l.Path(paths.ScopeUser, ir.KindInstructions, "")
		if err != nil {
			return nil, err
		}
		root, err := l.Root(paths.ScopeUser)
		if err != nil {
			return nil, err
		}
		out = append(out, Pair{Target: h, Hub: hubFile, File: file, Root: root})
	}
	return out, nil
}

// State says how a target's instruction file stands against the hub's.
type State string

// States.
const (
	// NoFile: the target has no instruction file, so there is nothing to review.
	NoFile State = "no-file"
	// NoHub: the hub has no instruction file to compare against.
	NoHub State = "no-hub"
	// Unrecorded: the target file has never been marked reviewed.
	Unrecorded State = "unrecorded"
	// Current: every hub section is as it was when the target file was last marked reviewed.
	Current State = "current"
	// Changed: hub sections changed since the target file was last marked reviewed.
	Changed State = "changed"
)

// Report is the outcome of checking one pair.
type Report struct {
	Pair
	State State
	Diff  Diff
	// Sections counts the hub file's sections.
	Sections int
}

// Check compares the hub's instruction file below home with what p's target file was last
// reviewed against.
func Check(home string, p Pair) (Report, error) {
	rep := Report{Pair: p}
	if _, err := os.Stat(filepath.Join(home, p.File)); errors.Is(err, fs.ErrNotExist) {
		rep.State = NoFile
		return rep, nil
	} else if err != nil {
		return rep, err
	}
	data, err := os.ReadFile(filepath.Join(home, p.Hub))
	if errors.Is(err, fs.ErrNotExist) {
		rep.State = NoHub
		return rep, nil
	}
	if err != nil {
		return rep, err
	}
	current := Sections(data)
	rep.Sections = len(current)
	r, err := Load(filepath.Join(home, p.Root))
	if err != nil {
		return rep, err
	}
	if r == nil {
		rep.State = Unrecorded
		return rep, nil
	}
	rep.Diff = Compare(r, current)
	rep.State = Current
	if !rep.Diff.Empty() {
		rep.State = Changed
	}
	return rep, nil
}

// Mark records that p's target file below home now reflects every section of the hub file, and
// returns the number of sections recorded.
func Mark(home string, p Pair) (int, error) {
	if _, err := os.Stat(filepath.Join(home, p.File)); err != nil {
		return 0, fmt.Errorf("%s: %w", p.File, err)
	}
	data, err := os.ReadFile(filepath.Join(home, p.Hub))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p.Hub, err)
	}
	r := &Record{Version: SchemaVersion, Hub: filepath.ToSlash(p.Hub), Sections: map[string]string{}}
	for _, s := range Sections(data) {
		r.Sections[s.Heading] = s.Hash
	}
	if err := r.Save(filepath.Join(home, p.Root)); err != nil {
		return 0, err
	}
	return len(r.Sections), nil
}
