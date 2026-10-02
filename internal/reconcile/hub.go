package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/convert"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/skilldir"
)

// hubUnit is one skill directory, command file or agent file in the hub.
type hubUnit struct {
	kind ir.Kind
	abs  string
	// rel is slash-separated below the root, as manifest entries record their source.
	rel    string
	suffix string
	item   *ir.Item
	hash   string
	// reason says why the unit is not converted; err that it could not be read.
	reason string
	err    error
}

// hub is every unit the configuration does not skip, in listing order.
type hub struct {
	dir string
	// dirs are the hub's skill, command and agent directories, pairs included.
	dirs  []string
	units []*hubUnit
	// agents are the names of the main agents directory: what a body can refer to.
	agents   []string
	warnings []string
	// incomplete are hub directories, slash-separated below the root, that could not be listed in
	// full: an item missing from them may only be unreadable, so its output is not deleted.
	incomplete []string
}

func (h *hub) partial(opts Options, dir string, problems ...string) {
	h.warnings = append(h.warnings, problems...)
	h.incomplete = append(h.incomplete, h.relTo(opts, dir))
}

func (h *hub) relTo(opts Options, p string) string {
	if rel, err := filepath.Rel(opts.Root, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return p
}

// incompleteIn reports whether dir, or something in it, could not be read in full.
func (h *hub) incompleteIn(opts Options, dir string) bool {
	rel := h.relTo(opts, dir)
	return slices.ContainsFunc(h.incomplete, func(s string) bool { return s == rel || strings.HasPrefix(s, rel+"/") })
}

// underAny reports whether src, a hub path, is one of dirs or lies below one.
func underAny(src string, dirs []string) bool {
	for _, d := range dirs {
		if src == d || strings.HasPrefix(src, d+"/") {
			return true
		}
	}
	return false
}

// readHub lists and reads the hub at the root as the hub harness loads it. A unit the
// configuration skips is left out entirely; one that collides with another, or fails to read, is
// kept with the reason, so its earlier output stays in place.
func readHub(opts Options) (*hub, error) {
	l, err := paths.For(opts.Config.Hub)
	if err != nil {
		return nil, err
	}
	rootRel, err := l.Root(paths.ScopeUser)
	if err != nil {
		return nil, err
	}
	h := &hub{dir: filepath.Join(opts.Root, rootRel)}
	switch info, err := os.Stat(h.dir); {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("no %s hub at %s", opts.Config.Hub.Title(), h.dir)
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("the %s hub %s is not a directory", opts.Config.Hub.Title(), h.dir)
	}
	dirOf := func(kind ir.Kind, suffix string) string {
		d, err := l.Dir(paths.ScopeUser, kind)
		if err != nil {
			return ""
		}
		return filepath.Join(opts.Root, d) + suffix
	}
	for _, suffix := range append([]string{""}, opts.Config.Pairs...) {
		skills, commands, agents := dirOf(ir.KindSkill, suffix), dirOf(ir.KindCommand, suffix), dirOf(ir.KindAgent, suffix)
		for _, d := range []string{skills, commands, agents} {
			if d != "" {
				h.dirs = append(h.dirs, d)
			}
		}
		h.readSkills(opts, skills, suffix)
		if commands != "" {
			h.readCommands(opts, convert.CommandDirs{Harness: opts.Config.Hub, Commands: commands, Skills: skills}, suffix)
		}
		h.readAgents(opts, agents, suffix)
	}
	return h, nil
}

func (h *hub) add(opts Options, u *hubUnit) bool {
	rel, err := filepath.Rel(opts.Root, u.abs)
	if err != nil {
		rel = u.abs
	}
	u.rel = filepath.ToSlash(rel)
	below, _ := filepath.Rel(h.dir, u.abs)
	if opts.Config.Skipped(filepath.ToSlash(below)) {
		return false
	}
	h.units = append(h.units, u)
	return true
}

// dangling reports whether p is a link to nothing, such as a dotfiles link to a moved repository
// or an unmounted volume: what it held may come back, so it does not count as deleted.
func dangling(p string) bool {
	info, err := os.Lstat(p)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		return false
	}
	_, err = os.Stat(p)
	return err != nil
}

// danglingLink reports a hub directory that is a link to nothing as a problem, keeping its output.
func (h *hub) danglingLink(opts Options, dir string) bool {
	if !dangling(dir) {
		return false
	}
	h.partial(opts, dir, dir+" links to a missing directory; its earlier output is kept")
	return true
}

func (h *hub) readSkills(opts Options, dir, suffix string) {
	if h.danglingLink(opts, dir) {
		return
	}
	dirs, err := convert.SkillDirs(opts.Config.Hub, dir)
	if err != nil {
		h.partial(opts, dir, err.Error())
		return
	}
	// SkillDirs passes over a skill it cannot check, which must not read as one deleted.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if h.danglingLink(opts, p) {
			continue
		}
		info, err := os.Stat(p)
		if err == nil && info.IsDir() {
			entry := filepath.Join(p, skilldir.Entry)
			if dangling(entry) {
				h.partial(opts, p, entry+" links to a missing file; its earlier output is kept")
				continue
			}
			_, err = os.Stat(entry)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			h.partial(opts, p, err.Error())
		}
	}
	byName := map[string][]*hubUnit{}
	var listed []*hubUnit
	for _, d := range dirs {
		u := &hubUnit{kind: ir.KindSkill, abs: d, suffix: suffix}
		u.item, u.err = convert.ReadSkill(opts.Config.Hub, d)
		name := filepath.Base(d)
		if u.err == nil {
			name = u.item.Name
			u.hash = skillHash(d, u.item)
		}
		byName[name] = append(byName[name], u)
		listed = append(listed, u)
	}
	for name, group := range byName {
		if len(group) < 2 {
			continue
		}
		for _, u := range group {
			var others []string
			for _, o := range group {
				if o != u {
					others = append(others, filepath.Base(o.abs))
				}
			}
			u.reason = fmt.Sprintf("skills %s are also named %s, and %s loads only one of them", strings.Join(others, ", "), name, opts.Config.Hub.Title())
		}
	}
	for _, u := range listed {
		h.add(opts, u)
	}
}

func (h *hub) readCommands(opts Options, d convert.CommandDirs, suffix string) {
	if h.danglingLink(opts, d.Commands) {
		return
	}
	rels, warnings, err := convert.CommandFiles(d.Commands)
	if err != nil {
		h.partial(opts, d.Commands, err.Error())
		return
	}
	if len(warnings) > 0 {
		h.partial(opts, d.Commands, warnings...)
	}
	shadowed, shadowErr := convert.Shadowed(d)
	if shadowErr == nil && h.incompleteIn(opts, d.Skills) {
		shadowErr = errors.New("a skill beside them could not be read, so its name is unknown")
	}
	for _, rel := range rels {
		u := &hubUnit{kind: ir.KindCommand, abs: filepath.Join(d.Commands, filepath.FromSlash(rel)), suffix: suffix}
		if !h.add(opts, u) {
			continue
		}
		switch {
		case shadowErr != nil:
			u.reason = "name collisions not checked: " + shadowErr.Error()
		case shadowed[rel] != "":
			u.reason = shadowed[rel]
		default:
			u.item, u.err = convert.ReadCommand(opts.Config.Hub, u.abs, rel)
			u.hash = fileHash(u.abs)
		}
	}
}

func (h *hub) readAgents(opts Options, dir, suffix string) {
	if h.danglingLink(opts, dir) {
		return
	}
	rels, warnings, err := convert.AgentFiles(opts.Config.Hub, dir)
	if err != nil {
		h.partial(opts, dir, err.Error())
		return
	}
	if len(warnings) > 0 {
		h.partial(opts, dir, warnings...)
	}
	dups, dupErr := convert.AgentDuplicates(opts.Config.Hub, dir)
	for _, rel := range rels {
		u := &hubUnit{kind: ir.KindAgent, abs: filepath.Join(dir, filepath.FromSlash(rel)), suffix: suffix}
		item, err := convert.ReadAgent(opts.Config.Hub, u.abs)
		if err == nil && suffix == "" && !slices.Contains(h.agents, item.Name) {
			h.agents = append(h.agents, item.Name)
		}
		if !h.add(opts, u) {
			continue
		}
		switch {
		case dupErr != nil:
			u.reason = "names not checked: " + dupErr.Error()
		case dups[rel] != "":
			u.reason = dups[rel]
		default:
			u.item, u.err = item, err
			u.hash = fileHash(u.abs)
		}
	}
}

// fileHash is the manifest hash of a command or agent file; empty when it cannot be read, which
// the reader has already reported.
func fileHash(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return manifest.Hash(data)
}

// skillHash covers SKILL.md and every bundled file the reader kept, with paths and modes.
func skillHash(dir string, item *ir.Item) string {
	sum := sha256.New()
	entry, _ := os.ReadFile(filepath.Join(dir, skilldir.Entry))
	files := append([]ir.Resource{{Path: skilldir.Entry, Mode: 0o644, Data: entry}}, item.Resources...)
	for _, f := range files {
		fmt.Fprintf(sum, "%s\x00%04o\x00%d\x00", path.Clean(f.Path), f.Mode.Perm(), len(f.Data))
		sum.Write(f.Data)
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// hubKind maps a hub kind to the kind it becomes in a target.
func hubKind(k ir.Kind) ir.Kind {
	if k == ir.KindAgent {
		return ir.KindAgent
	}
	return ir.KindSkill
}
