package convert

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
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

type agentCodec struct {
	read  func(string) (*ir.Item, error)
	write func(*ir.Item, common.Options) ([]ir.Resource, loss.Report, error)
	ext   string
}

var agents = map[harness.ID]agentCodec{
	harness.Claude:      {claude.ReadAgent, claude.WriteAgent, ".md"},
	harness.Codex:       {codex.ReadAgent, codex.WriteAgent, ".toml"},
	harness.Antigravity: {antigravity.ReadAgent, antigravity.WriteAgent, ".md"},
}

// AgentExt is the file extension of harness h's agent files.
func AgentExt(h harness.ID) string {
	return agents[h].ext
}

// ReadAgent reads the agent file path as harness h.
func ReadAgent(h harness.ID, path string) (*ir.Item, error) {
	c, ok := agents[h]
	if !ok {
		return nil, fmt.Errorf("no agent reader for %q", h)
	}
	return c.read(path)
}

// Agent converts the agent item to an agent of harness to. The name becomes a file name, so a name
// that could leave its directory is refused first.
func Agent(item *ir.Item, to harness.ID, opts Options) (Result, error) {
	c, ok := agents[to]
	if !ok {
		return Result{}, fmt.Errorf("no agent writer for %q", to)
	}
	if err := paths.ValidName(item.Name); err != nil {
		return Result{}, fmt.Errorf("agent name: %w", err)
	}
	files, report, err := c.write(item, opts)
	if err != nil {
		return Result{}, err
	}
	checkBody(item, to, opts, &report)
	report.Source = fmt.Sprintf("%s agent %s", item.Source.Harness.Title(), item.Name)
	report.Target = fmt.Sprintf("%s agent %s", to.Title(), item.Name)
	return Result{Item: item, Files: files, Report: report}, nil
}

// Item converts item to harness to: an agent to an agent, a skill or a command to a skill.
func Item(item *ir.Item, to harness.ID, opts Options) (Result, error) {
	if item.Kind == ir.KindAgent {
		return Agent(item, to, opts)
	}
	return Skill(item, to, opts)
}

// DetectAgent finds, from the location table, the harness whose agents directory holds the file
// path, and returns it with that directory and the file's slash-separated path below it. Claude
// Code and Codex read their agents directories recursively, Antigravity only one level.
func DetectAgent(path, home string) (harness.ID, string, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", "", err
	}
	try := func(file, home string) (harness.ID, string, string, bool) {
		h, dir, ok := agentOwner(file, home)
		if !ok {
			return "", "", "", false
		}
		rel, _ := filepath.Rel(dir, file)
		return h, dir, filepath.ToSlash(rel), true
	}
	if h, dir, rel, ok := try(abs, home); ok {
		return h, dir, rel, nil
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		if home != "" {
			home = resolve(home)
		}
		if h, dir, rel, ok := try(filepath.Join(dir, filepath.Base(abs)), home); ok {
			return h, dir, rel, nil
		}
	}
	return "", "", "", fmt.Errorf("%s: %w (no harness keeps agents there); pass --from", path, ErrAmbiguous)
}

func agentOwner(file, home string) (harness.ID, string, bool) {
	for dir := filepath.Dir(file); ; {
		for _, h := range harness.All {
			if filepath.Ext(file) != agents[h].ext || (h == harness.Antigravity && dir != filepath.Dir(file)) {
				continue
			}
			l := mustLayout(h)
			for _, scope := range []paths.Scope{paths.ScopeUser, paths.ScopeProject} {
				rel, err := l.Dir(scope, ir.KindAgent)
				if err != nil {
					continue
				}
				if scope == paths.ScopeUser && home != "" && dir == filepath.Join(home, rel) ||
					scope == paths.ScopeProject && strings.HasSuffix(dir, string(filepath.Separator)+rel) {
					return h, dir, true
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

// AgentFiles lists the agent files of harness h below dir, as slash-separated paths relative to
// dir, the way h finds them: Claude Code with its command loader, Codex recursively, Antigravity one
// level deep. What cannot be read is skipped and returned as a warning; a missing dir has none.
func AgentFiles(h harness.ID, dir string) (rels, warnings []string, err error) {
	switch h {
	case harness.Claude:
		return CommandFiles(dir)
	case harness.Antigravity:
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		for _, e := range entries {
			if path.Ext(e.Name()) != ".md" {
				continue
			}
			info, err := os.Stat(filepath.Join(dir, e.Name()))
			switch {
			case err != nil:
				warnings = append(warnings, err.Error())
			case info.Mode().IsRegular():
				rels = append(rels, e.Name())
			}
		}
		return rels, warnings, nil
	}
	// WalkDir does not enter a symlinked root; Codex reads through one.
	root, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil && p == root:
			return err
		case err != nil:
			warnings = append(warnings, err.Error())
			return fs.SkipDir
		case d.IsDir() || filepath.Ext(p) != agents[h].ext:
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return rels, warnings, nil
}

// AgentDuplicates lists the agent files of harness h below dir that share a name with another, keyed
// by slash-separated path below dir, with the reason: the harness loads one of them, and which one is
// not something agentport can tell. One file reached by two paths is one agent, not a duplicate.
func AgentDuplicates(h harness.ID, dir string) (map[string]string, error) {
	rels, _, err := AgentFiles(h, dir)
	if err != nil {
		return nil, err
	}
	byName := map[string][]string{}
	for _, rel := range rels {
		if item, err := ReadAgent(h, filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			byName[item.Name] = append(byName[item.Name], rel)
		}
	}
	same := samePaths(dir, rels)
	out := map[string]string{}
	for name, group := range byName {
		for _, rel := range group {
			others := slices.DeleteFunc(slices.Clone(group), func(s string) bool { return s == rel || slices.Contains(same[rel], s) })
			if len(others) > 0 {
				out[rel] = fmt.Sprintf("%s is also named %s, and %s loads only one of them", strings.Join(others, ", "), name, h.Title())
			}
		}
	}
	return out, nil
}
