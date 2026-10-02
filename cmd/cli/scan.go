package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/somaz94/agentport/internal/convert"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/paths"
)

var scanKinds = []ir.Kind{ir.KindSkill, ir.KindCommand, ir.KindAgent}

type scanOptions struct {
	tools []string
	scope string
	root  string
}

// Target summarizes converting one item to one harness.
type Target struct {
	Lossy  bool                `json:"lossy"`
	Counts map[loss.Status]int `json:"counts,omitempty"`
	Error  string              `json:"error,omitempty"`
}

// Entry is one customization found by scan.
type Entry struct {
	Harnesses []harness.ID          `json:"harnesses"`
	Kind      ir.Kind               `json:"kind"`
	Name      string                `json:"name"`
	Path      string                `json:"path"`
	Targets   map[harness.ID]Target `json:"targets,omitempty"`
	// Skipped says why a command or agent would not be converted at all.
	Skipped string `json:"skipped,omitempty"`
}

func newScanCmd(opts *options) *cobra.Command {
	s := &scanOptions{}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "List the customizations at each harness location and how portable each is",
		Long: "List skills, commands and agents at every harness location for a scope. Convert each\n" +
			"in memory to the other harnesses and summarize what would be lost; a command whose skill\n" +
			"name is taken is listed as skipped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, warnings, err := scan(s)
			if err != nil {
				return err
			}
			for _, w := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
			return writeScan(cmd.OutOrStdout(), opts.output, entries)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&s.tools, "tool", nil, "harnesses to scan (repeatable; default all): "+strings.Join(harness.Names(), ", "))
	f.StringVar(&s.scope, "scope", string(paths.ScopeUser), "user or project")
	f.StringVar(&s.root, "root", "", "scope root: the home directory for user scope, the repository for project scope")
	return cmd
}

// Unreadable locations are warnings, not failures: one bad directory must not hide the others.
func scan(s *scanOptions) ([]Entry, []string, error) {
	scope, err := paths.ParseScope(s.scope)
	if err != nil {
		return nil, nil, err
	}
	root, err := scopeRoot(scope, s.root)
	if err != nil {
		return nil, nil, err
	}
	tools := harness.All
	if len(s.tools) > 0 {
		tools = nil
		for _, t := range s.tools {
			h, err := harness.Parse(t)
			if err != nil {
				return nil, nil, err
			}
			if !slices.Contains(tools, h) {
				tools = append(tools, h)
			}
		}
	}

	byPath := map[string]*Entry{}
	commandDirs := map[harness.ID]convert.CommandDirs{}
	agentDirs := map[harness.ID]string{}
	var order, warnings []string
	for _, h := range tools {
		layout, err := paths.For(h)
		if err != nil {
			return nil, nil, err
		}
		for _, kind := range scanKinds {
			rel, err := layout.Dir(scope, kind)
			if err != nil {
				continue
			}
			if kind == ir.KindAgent {
				agentDirs[h] = filepath.Join(root, rel)
			}
			if kind == ir.KindCommand {
				if skills, err := layout.Dir(scope, ir.KindSkill); err == nil {
					commandDirs[h] = convert.CommandDirs{Harness: h, Commands: filepath.Join(root, rel), Skills: filepath.Join(root, skills)}
				}
			}
			found, warn := listItems(h, kind, filepath.Join(root, rel))
			warnings = append(warnings, warn...)
			for _, item := range found {
				// Codex and Antigravity share a project .agents/skills; list such a skill once.
				if e, ok := byPath[item.path]; ok {
					e.Harnesses = append(e.Harnesses, h)
					continue
				}
				relPath, _ := filepath.Rel(root, item.path)
				byPath[item.path] = &Entry{Harnesses: []harness.ID{h}, Kind: kind, Name: item.name, Path: filepath.ToSlash(relPath)}
				order = append(order, item.path)
			}
		}
	}

	shadowed := map[harness.ID]map[string]string{}
	unchecked := map[harness.ID]string{}
	for h, d := range commandDirs {
		m, err := convert.Shadowed(d)
		if err != nil {
			// listItems has already warned about the directory; convert refuses these commands too.
			unchecked[h] = "name collisions not checked: " + err.Error()
			continue
		}
		shadowed[h] = m
	}

	duplicates := map[harness.ID]map[string]string{}
	for h, dir := range agentDirs {
		// An unreadable directory was already reported while listing.
		duplicates[h], _ = convert.AgentDuplicates(h, dir)
	}

	entries := make([]Entry, 0, len(order))
	for _, p := range order {
		e := byPath[p]
		switch e.Kind {
		case ir.KindSkill:
			item, err := convert.ReadSkill(convert.Owner(p, e.Harnesses), p)
			e.Name = loadedName(item, err, e.Name)
			e.Targets = convertTargets(item, err, e.Harnesses)
		case ir.KindCommand:
			h := e.Harnesses[0]
			rel, _ := filepath.Rel(commandDirs[h].Commands, p)
			rel = filepath.ToSlash(rel)
			if why := cmp.Or(shadowed[h][rel], unchecked[h]); why != "" {
				e.Skipped = why
				break
			}
			item, err := convert.ReadCommand(h, p, rel)
			e.Targets = convertTargets(item, err, e.Harnesses)
		case ir.KindAgent:
			h := e.Harnesses[0]
			rel, _ := filepath.Rel(agentDirs[h], p)
			if why := duplicates[h][filepath.ToSlash(rel)]; why != "" {
				e.Skipped = why
				break
			}
			item, err := convert.ReadAgent(h, p)
			e.Name = loadedName(item, err, e.Name)
			e.Targets = convertTargets(item, err, e.Harnesses)
		}
		entries = append(entries, *e)
	}
	return entries, warnings, nil
}

// loadedName is the name a skill or agent loads under, which its frontmatter sets; the file or
// directory name stands in when the item cannot be read.
func loadedName(item *ir.Item, err error, fallback string) string {
	if err != nil {
		return fallback
	}
	return item.Name
}

func scopeRoot(scope paths.Scope, root string) (string, error) {
	if root != "" {
		return filepath.Abs(root)
	}
	if scope == paths.ScopeUser {
		return os.UserHomeDir()
	}
	return os.Getwd()
}

type found struct{ name, path string }

func listItems(h harness.ID, kind ir.Kind, dir string) ([]found, []string) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, []string{err.Error()}
	}
	var out []found
	var warnings []string
	switch kind {
	case ir.KindSkill:
		dirs, err := convert.SkillDirs(h, dir)
		if err != nil {
			return nil, []string{err.Error()}
		}
		for _, p := range dirs {
			out = append(out, found{filepath.Base(p), p})
		}
	case ir.KindCommand:
		rels, warn, err := convert.CommandFiles(dir)
		if err != nil {
			return nil, []string{err.Error()}
		}
		warnings = warn
		for _, rel := range rels {
			out = append(out, found{strings.TrimSuffix(rel, ".md"), filepath.Join(dir, filepath.FromSlash(rel))})
		}
	case ir.KindAgent:
		rels, warn, err := convert.AgentFiles(h, dir)
		if err != nil {
			return nil, []string{err.Error()}
		}
		warnings = warn
		for _, rel := range rels {
			out = append(out, found{strings.TrimSuffix(rel, path.Ext(rel)), filepath.Join(dir, filepath.FromSlash(rel))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, warnings
}

// convertTargets converts item, which reading returned with err, to every harness that does not
// already load it.
func convertTargets(item *ir.Item, err error, owners []harness.ID) map[harness.ID]Target {
	targets := map[harness.ID]Target{}
	for _, to := range harness.All {
		if slices.Contains(owners, to) {
			continue
		}
		if err != nil {
			targets[to] = Target{Error: err.Error()}
			continue
		}
		res, cerr := convert.Item(item, to, convert.Options{})
		if cerr != nil {
			targets[to] = Target{Error: cerr.Error()}
			continue
		}
		t := Target{Lossy: res.Report.Lossy(), Counts: map[loss.Status]int{}}
		for _, e := range res.Report.Entries {
			if e.Status == loss.Approximated || e.Status == loss.Dropped || e.Status == loss.Warn {
				t.Counts[e.Status]++
			}
		}
		targets[to] = t
	}
	return targets
}

func writeScan(w io.Writer, format string, entries []Entry) error {
	if format == outputJSON {
		if entries == nil {
			entries = []Entry{}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		_, err := fmt.Fprintln(w, "no skills, commands or agents found")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HARNESS\tKIND\tNAME\tPORTABILITY")
	for _, e := range entries {
		names := make([]string, len(e.Harnesses))
		for i, h := range e.Harnesses {
			names[i] = string(h)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", strings.Join(names, ","), e.Kind, e.Name, portability(e))
	}
	return tw.Flush()
}

func portability(e Entry) string {
	if e.Skipped != "" {
		return "skipped (" + e.Skipped + ")"
	}
	if e.Targets == nil {
		return "-"
	}
	var parts []string
	for _, h := range harness.All {
		t, ok := e.Targets[h]
		if !ok {
			continue
		}
		parts = append(parts, string(h)+": "+summary(t))
	}
	return strings.Join(parts, "; ")
}

func summary(t Target) string {
	if t.Error != "" {
		return "error (" + t.Error + ")"
	}
	if !t.Lossy {
		return "lossless"
	}
	var parts []string
	for _, s := range []loss.Status{loss.Dropped, loss.Approximated, loss.Warn} {
		if n := t.Counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	return strings.Join(parts, ", ")
}
