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

	"github.com/somaz94/agentport/internal/adapters/claude"
	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/skilldir"
)

// CommandDirs are the directories a command depends on: the commands directory it lives under,
// which names it, and the skills directory beside it, whose skills keep a name a command derives.
type CommandDirs struct {
	Harness  harness.ID
	Commands string
	Skills   string
}

var commandReaders = map[harness.ID]func(path, rel string) (*ir.Item, error){
	harness.Claude: claude.ReadCommand,
}

// ReadCommand reads the command file path as harness h. rel, its path below the commands
// directory, names it; an empty rel uses the file name.
func ReadCommand(h harness.ID, path, rel string) (*ir.Item, error) {
	read, ok := commandReaders[h]
	if !ok {
		return nil, fmt.Errorf("%s has no commands to read", h.Title())
	}
	return read(path, rel)
}

// DetectCommand finds, from the location table, the commands directory that holds the command
// file path, and returns it with the file's slash-separated path below it. The path is tried as
// given, then with its directory's symlinks resolved; the file itself is never resolved, because
// Claude Code names a linked command after the link.
func DetectCommand(path, home string) (CommandDirs, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return CommandDirs{}, "", err
	}
	if d, rel, ok := commandDirsOf(abs, home); ok {
		return d, rel, nil
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		if home != "" {
			home = resolve(home)
		}
		if d, rel, ok := commandDirsOf(filepath.Join(dir, filepath.Base(abs)), home); ok {
			return d, rel, nil
		}
	}
	return CommandDirs{}, "", fmt.Errorf("%s: %w (no harness keeps commands there); pass --from to read it as a command", path, ErrAmbiguous)
}

// commandDirsOf walks up from file to the nearest commands directory: a harness's user one under
// home, or a project one anywhere.
func commandDirsOf(file, home string) (CommandDirs, string, bool) {
	for dir := filepath.Dir(file); ; {
		for _, h := range harness.All {
			l := mustLayout(h)
			for _, scope := range []paths.Scope{paths.ScopeUser, paths.ScopeProject} {
				cmds, err := l.Dir(scope, ir.KindCommand)
				if err != nil {
					continue
				}
				var root string
				switch {
				case scope == paths.ScopeUser && home != "" && dir == filepath.Join(home, cmds):
					root = home
				case scope == paths.ScopeProject && strings.HasSuffix(dir, string(filepath.Separator)+cmds):
					root = strings.TrimSuffix(dir, cmds)
				default:
					continue
				}
				skills, err := l.Dir(scope, ir.KindSkill)
				if err != nil {
					continue
				}
				rel, _ := filepath.Rel(dir, file)
				return CommandDirs{Harness: h, Commands: dir, Skills: filepath.Join(root, skills)}, filepath.ToSlash(rel), true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return CommandDirs{}, "", false
		}
		dir = parent
	}
}

// Shadowed lists the commands below d.Commands that cannot become skills under their derived
// names, keyed by slash-separated path below d.Commands, with the reason. A skill beside them keeps
// its name, as Claude Code prefers a skill over a command; commands that derive one name, or name
// one file, are all refused, since none has a better claim to it.
func Shadowed(d CommandDirs) (map[string]string, error) {
	rels, _, err := CommandFiles(d.Commands)
	if err != nil {
		return nil, err
	}
	return shadowedAmong(d, rels)
}

// CommandBlocked says why the command at rel below d.Commands cannot become a skill, or "" when it
// can: the listing never reaches it, or Shadowed refuses it.
func CommandBlocked(d CommandDirs, rel string) (string, error) {
	rels, _, err := CommandFiles(d.Commands)
	if err != nil {
		return "", err
	}
	if !slices.Contains(rels, rel) {
		return "listing the commands directory as Claude Code does never reaches it: it is below an unreadable directory, or one already entered by another path", nil
	}
	shadowed, err := shadowedAmong(d, rels)
	if err != nil {
		return "", err
	}
	return shadowed[rel], nil
}

func shadowedAmong(d CommandDirs, rels []string) (map[string]string, error) {
	skills, err := skillNames(d.Harness, d.Skills)
	if err != nil {
		return nil, err
	}
	byName := map[string][]string{}
	for _, rel := range rels {
		name := ir.CommandSkillName(rel)
		byName[name] = append(byName[name], rel)
	}
	out := map[string]string{}
	for name, group := range byName {
		for _, rel := range group {
			switch {
			case skills[name]:
				out[rel] = fmt.Sprintf("a skill named %s exists and keeps the name", name)
			case len(group) > 1:
				others := slices.DeleteFunc(slices.Clone(group), func(s string) bool { return s == rel })
				out[rel] = fmt.Sprintf("its skill name %s is also derived from %s", name, strings.Join(others, ", "))
			}
		}
	}
	for rel, others := range samePaths(d.Commands, rels) {
		if _, ok := out[rel]; !ok {
			out[rel] = fmt.Sprintf("the same file is also listed as %s; Claude Code loads it under only one of these names", strings.Join(others, ", "))
		}
	}
	return out, nil
}

// samePaths maps each of rels that names the same file as others in rels to those others.
func samePaths(dir string, rels []string) map[string][]string {
	infos := make([]fs.FileInfo, len(rels))
	for i, rel := range rels {
		infos[i], _ = os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	}
	out := map[string][]string{}
	for i, a := range infos {
		for j, b := range infos {
			if i != j && a != nil && b != nil && os.SameFile(a, b) {
				out[rels[i]] = append(out[rels[i]], rels[j])
			}
		}
	}
	return out
}

// CommandFiles lists the command files below dir as Claude Code walks them, as slash-separated
// paths relative to dir: links are followed and keep their names, each directory is entered once,
// and a file reached by two paths is listed under both. An unreadable directory or a broken link is
// skipped with a warning; a missing dir holds no commands.
func CommandFiles(dir string) (rels, warnings []string, err error) {
	seen := map[string]bool{}
	var walk func(abs, rel string) error
	walk = func(abs, rel string) error {
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return err
		}
		if seen[resolved] {
			return nil
		}
		seen[resolved] = true
		entries, err := os.ReadDir(abs)
		if err != nil {
			return err
		}
		for _, e := range entries {
			p, r := filepath.Join(abs, e.Name()), path.Join(rel, e.Name())
			info, err := os.Stat(p)
			switch {
			case err != nil:
				warnings = append(warnings, err.Error())
			case info.IsDir():
				if err := walk(p, r); err != nil {
					warnings = append(warnings, err.Error())
				}
			case info.Mode().IsRegular() && strings.HasSuffix(e.Name(), ".md"):
				rels = append(rels, r)
			}
		}
		return nil
	}
	if err := walk(dir, ""); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return rels, warnings, nil
}

// SkillDirs lists the skill directories in dir, the skills directory of harness h: subdirectories
// holding a SKILL.md, symlinked ones included. Claude Code's synced/ holds skills claude.ai
// manages, not the user's own, so it is left out. A missing dir has none.
func SkillDirs(h harness.ID, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if h == harness.Claude && e.Name() == "synced" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		// Stat, not the DirEntry, so a symlinked skill directory counts.
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(p, skilldir.Entry)); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func skillNames(h harness.ID, dir string) (map[string]bool, error) {
	dirs, err := SkillDirs(h, dir)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, d := range dirs {
		names[skillName(d)] = true
	}
	return names, nil
}

// skillName is the name a skill loads under: its SKILL.md name, or the directory name when it
// has none or does not parse.
func skillName(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, skilldir.Entry)); err == nil {
		if doc, err := frontmatter.Parse(data); err == nil {
			if n, ok := doc.Get("name"); ok {
				if name := common.Text(n); name != "" {
					return name
				}
			}
		}
	}
	return filepath.Base(dir)
}
