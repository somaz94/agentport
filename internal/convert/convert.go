// Package convert routes an item to the reader and writer of its harness.
package convert

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/adapters/antigravity"
	"github.com/somaz94/agentport/internal/adapters/claude"
	"github.com/somaz94/agentport/internal/adapters/codex"
	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/paths"
)

// ErrAmbiguous means the path alone does not say which harness owns the item.
var ErrAmbiguous = errors.New("cannot tell the source harness from the path")

// Options are the conversion choices a user can change.
type Options = common.Options

type skillCodec struct {
	read  func(string) (*ir.Item, error)
	write func(*ir.Item, common.Options) ([]ir.Resource, loss.Report, error)
}

var skills = map[harness.ID]skillCodec{
	harness.Claude:      {claude.ReadSkill, claude.WriteSkill},
	harness.Codex:       {codex.ReadSkill, codex.WriteSkill},
	harness.Antigravity: {antigravity.ReadSkill, antigravity.WriteSkill},
}

// ReadSkill reads the skill directory dir as harness h.
func ReadSkill(h harness.ID, dir string) (*ir.Item, error) {
	c, ok := skills[h]
	if !ok {
		return nil, fmt.Errorf("no skill reader for %q", h)
	}
	return c.read(dir)
}

// Result is one converted item: the files to place in its target directory and the loss report.
type Result struct {
	Item   *ir.Item
	Files  []ir.Resource
	Report loss.Report
}

// Skill converts item, a skill or a command, to a skill of harness to. The item name becomes a
// directory name, so a name that could leave its directory is refused before anything is converted.
func Skill(item *ir.Item, to harness.ID, opts Options) (Result, error) {
	c, ok := skills[to]
	if !ok {
		return Result{}, fmt.Errorf("no skill writer for %q", to)
	}
	if err := paths.ValidName(item.Name); err != nil {
		return Result{}, fmt.Errorf("skill name: %w", err)
	}
	files, report, err := c.write(item, opts)
	if err != nil {
		return Result{}, err
	}
	report.Source = fmt.Sprintf("%s %s %s", item.Source.Harness.Title(), item.Kind, item.Name)
	report.Target = fmt.Sprintf("%s skill %s", to.Title(), item.Name)
	return Result{Item: item, Files: files, Report: report}, nil
}

// DetectSkill names the harness that owns the skill directory dir, from the location table in
// internal/paths. The directory is resolved through symlinks first, so a linked skill counts as
// the harness that holds the original. A project `.agents/skills` is read by both Codex and
// Antigravity, so it is ambiguous unless the Codex sidecar is present.
func DetectSkill(dir, home string) (harness.ID, error) {
	resolved := resolve(dir)
	parent := filepath.Dir(resolved)
	if home != "" {
		home = resolve(home)
		for _, h := range harness.All {
			if d, err := mustLayout(h).Dir(paths.ScopeUser, ir.KindSkill); err == nil && parent == filepath.Join(home, d) {
				return h, nil
			}
		}
	}
	var owners []harness.ID
	for _, h := range harness.All {
		if d, err := mustLayout(h).Dir(paths.ScopeProject, ir.KindSkill); err == nil && strings.HasSuffix(parent, string(filepath.Separator)+d) {
			owners = append(owners, h)
		}
	}
	if len(owners) == 1 {
		return owners[0], nil
	}
	if len(owners) > 1 {
		if hasSidecar(resolved) {
			return harness.Codex, nil
		}
		return "", fmt.Errorf("%s: %w (read by %s); pass --from", dir, ErrAmbiguous, joinTitles(owners))
	}
	return "", fmt.Errorf("%s: %w (no harness keeps skills in %s); pass --from", dir, ErrAmbiguous, parent)
}

// Owner picks the reader for a skill directory several harnesses load: Codex when its sidecar is
// present, otherwise the first non-Codex owner, where DetectSkill would ask for --from.
func Owner(dir string, owners []harness.ID) harness.ID {
	if slices.Contains(owners, harness.Codex) && hasSidecar(resolve(dir)) {
		return harness.Codex
	}
	for _, h := range owners {
		if h != harness.Codex {
			return h
		}
	}
	return owners[0]
}

// WithTargetSidecar returns item with the Codex sidecar already present at targetDir added to its
// resources, so rewriting an existing Codex skill updates that sidecar instead of replacing or
// orphaning it. Items that bring their own sidecar are returned unchanged. A sidecar exactly as an
// earlier conversion generated it holds nothing but that run's policy, so this run's flags decide.
func WithTargetSidecar(item *ir.Item, targetDir string) (*ir.Item, error) {
	for _, r := range item.Resources {
		if r.Path == common.CodexSidecar {
			return item, nil
		}
	}
	data, err := os.ReadFile(filepath.Join(targetDir, filepath.FromSlash(common.CodexSidecar)))
	if errors.Is(err, fs.ErrNotExist) {
		return item, nil
	}
	if err != nil {
		return nil, err
	}
	if string(data) == common.GeneratedSidecar {
		data = []byte(common.DefaultSidecar)
	}
	cp := *item
	cp.Resources = append(slices.Clone(item.Resources), ir.Resource{Path: common.CodexSidecar, Mode: 0o644, Data: data})
	return &cp, nil
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func hasSidecar(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(common.CodexSidecar)))
	return err == nil
}

func mustLayout(h harness.ID) paths.Layout {
	l, err := paths.For(h)
	if err != nil {
		panic(err)
	}
	return l
}

func joinTitles(hs []harness.ID) string {
	names := make([]string, len(hs))
	for i, h := range hs {
		names[i] = h.Title()
	}
	return strings.Join(names, " and ")
}
