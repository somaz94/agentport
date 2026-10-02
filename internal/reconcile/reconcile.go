// Package reconcile compares what the hub converts to with what each target harness holds, and
// applies the difference. It writes or deletes a target file only when the manifest records it and
// nobody has changed it since, Options.Force aside; a file not yet recorded that already holds
// exactly what agentport would write is recorded.
package reconcile

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/somaz94/agentport/internal/config"
	"github.com/somaz94/agentport/internal/convert"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/safefs"
	"github.com/somaz94/agentport/internal/skilldir"
)

// Options say what to sync and how.
type Options struct {
	// Root is the scope's root, the home directory; Unit and File paths are relative to it.
	Root string
	// Config supplies the hub, translation pairs, skip patterns and conversion settings.
	Config *config.Config
	// Targets are the harnesses to sync; when empty, every one that is set up.
	Targets []harness.ID
	// Generator is recorded in the manifest beside each file written.
	Generator string
	// Force replaces target files edited by hand, and stops tracking edited files whose source is
	// gone instead of reporting them on every run.
	Force bool
}

// Unit statuses that leave every file of the unit as it is.
const (
	Conflict = "conflict"
	Skipped  = "skipped"
	Failed   = "error"
)

// Plan is what a sync would do, target by target.
type Plan struct {
	Root     string
	Targets  []*Target
	Warnings []string
	opts     Options
	// hubDirs are the hub and its directories, which Apply refuses to write or delete in.
	hubDirs []string
}

// Target is the plan for one target harness.
type Target struct {
	Harness harness.ID
	// Manifest is the absolute path of the target's manifest.
	Manifest string
	Units    []*Unit
	// Unmanaged lists what agentport did not write and will not touch: units in the target's
	// directories, and stray files inside the units it manages.
	Unmanaged []string
	Warnings  []string
	// Err is set when nothing can be synced to the target; it then has no units.
	Err error

	root string
	man  *manifest.Manifest
	dirs []*kindDir
}

// Unit is one skill directory or agent file in a target, with the hub item it comes from.
type Unit struct {
	Kind ir.Kind
	// Path is slash-separated below the root; empty when the hub item was skipped or failed.
	Path string
	// Source is the hub item's slash-separated path below the root.
	Source string
	// Status is empty when the states of Files decide, otherwise Conflict, Skipped or Failed,
	// explained by Reason.
	Status string
	Reason string
	Report *loss.Report
	Files  []*File

	hash string
	dir  *kindDir
	// base is the absolute directory the unit's files are written and deleted below.
	base string
}

// File is one target file and what a sync does with it.
type File struct {
	// Path is slash-separated below the root.
	Path  string
	State manifest.State
	// desired is set when the hub produces the file, with its content and mode.
	desired bool
	data    []byte
	mode    fs.FileMode
}

// kindDir is one directory a target keeps items of one kind in.
type kindDir struct {
	kind   ir.Kind
	rel    string
	abs    string
	suffix string
	ext    string
}

// Changes reports whether applying the plan would write or delete anything, or leaves anything out
// of sync: a file edited in the target or a conflict.
func (p *Plan) Changes() bool {
	for _, t := range p.Targets {
		for _, u := range t.Units {
			if u.Status == Conflict {
				return true
			}
			if u.Status != "" {
				continue
			}
			for _, f := range u.Files {
				if f.State != manifest.StateUnchanged {
					return true
				}
			}
		}
	}
	return false
}

// Lossy reports whether any converted unit lost something.
func (p *Plan) Lossy() bool {
	for _, t := range p.Targets {
		for _, u := range t.Units {
			if u.Report != nil && u.Report.Lossy() {
				return true
			}
		}
	}
	return false
}

// New plans a sync: the hub is read and converted once per target, and every target file is
// classified. Nothing is written.
func New(opts Options) (*Plan, error) {
	if opts.Config == nil {
		opts.Config = config.Default()
	}
	h, err := readHub(opts)
	if err != nil {
		return nil, err
	}
	targets := opts.Targets
	if len(targets) == 0 {
		if targets = setUp(opts); len(targets) == 0 {
			return nil, fmt.Errorf("no target harness is set up under %s", opts.Root)
		}
	}
	p := &Plan{Root: opts.Root, Warnings: h.warnings, opts: opts, hubDirs: append([]string{h.dir}, h.dirs...)}
	for _, to := range targets {
		if to == opts.Config.Hub {
			return nil, fmt.Errorf("%s is the hub, not a target", to)
		}
		t := planTarget(h, to, opts)
		for _, earlier := range p.Targets {
			if t.Err == nil && earlier.Err == nil && overlap(t, earlier) {
				t.Err, t.Units, t.Unmanaged = fmt.Errorf("its directories resolve to the same place as %s's; syncing both would make each overwrite the other", earlier.Harness.Title()), nil, nil
			}
		}
		p.Targets = append(p.Targets, t)
	}
	return p, nil
}

// setUp lists the harnesses other than the hub whose configuration directory exists.
func setUp(opts Options) []harness.ID {
	var out []harness.ID
	for _, h := range harness.All {
		if h == opts.Config.Hub {
			continue
		}
		l, err := paths.For(h)
		if err != nil {
			continue
		}
		if root, err := l.Root(paths.ScopeUser); err == nil {
			if info, err := os.Stat(filepath.Join(opts.Root, root)); err == nil && info.IsDir() {
				out = append(out, h)
			}
		}
	}
	return out
}

// overlap reports whether a directory of one target resolves into one of the other's, as when
// one harness's skills directory links to another's.
func overlap(a, b *Target) bool {
	for _, x := range a.dirs {
		for _, y := range b.dirs {
			rx, ry := resolveExisting(x.abs), resolveExisting(y.abs)
			if within(rx, ry) || within(ry, rx) {
				return true
			}
		}
	}
	return false
}

func planTarget(h *hub, to harness.ID, opts Options) *Target {
	t := &Target{Harness: to}
	l, err := paths.For(to)
	if err != nil {
		t.Err = err
		return t
	}
	rootRel, err := l.Root(paths.ScopeUser)
	if err != nil {
		t.Err = err
		return t
	}
	root := filepath.Join(opts.Root, rootRel)
	switch info, err := os.Stat(root); {
	case errors.Is(err, fs.ErrNotExist):
		t.Err = fmt.Errorf("%s is not set up: %s does not exist", to.Title(), root)
		return t
	case err != nil:
		t.Err = err
		return t
	case !info.IsDir():
		t.Err = fmt.Errorf("%s is not set up: %s is not a directory", to.Title(), root)
		return t
	}
	t.root, t.Manifest = root, manifest.Path(root)
	if t.man, err = manifest.Load(root); err != nil {
		t.Err = err
		return t
	}
	for _, suffix := range append([]string{""}, opts.Config.Pairs...) {
		for _, kind := range []ir.Kind{ir.KindSkill, ir.KindAgent} {
			d, err := l.Dir(paths.ScopeUser, kind)
			if err != nil {
				t.Err = err
				return t
			}
			kd := &kindDir{kind: kind, rel: filepath.ToSlash(d) + suffix, abs: filepath.Join(opts.Root, d) + suffix, suffix: suffix}
			if kind == ir.KindAgent {
				kd.ext = convert.AgentExt(to)
			}
			// A link to nothing resolves to its own path, so another target could create what it
			// points at in the middle of an apply.
			if li, err := os.Lstat(kd.abs); err == nil && li.Mode()&fs.ModeSymlink != 0 {
				if _, err := os.Stat(kd.abs); err != nil {
					t.Err = fmt.Errorf("%s links to a missing directory; create it or remove the link", kd.abs)
					return t
				}
			}
			if err := outsideHub(kd.abs, append([]string{h.dir}, h.dirs...)); err != nil {
				t.Err = err
				return t
			}
			t.dirs = append(t.dirs, kd)
		}
	}
	(&planner{t: t, h: h, opts: opts, layout: l}).run()
	return t
}

// outsideHub refuses a target directory that is, contains or sits in the hub or one of its
// directories, through links in either direction: a sync would write over its own source.
func outsideHub(dir string, hubDirs []string) error {
	d := resolveExisting(dir)
	for _, hd := range hubDirs {
		if r := resolveExisting(hd); within(d, r) || within(r, d) {
			return fmt.Errorf("%s and the hub's %s resolve to the same place; syncing would overwrite the source", dir, hd)
		}
	}
	return nil
}

// resolveExisting resolves the symlinks of p's deepest existing ancestor and keeps the rest.
func resolveExisting(p string) string {
	p = filepath.Clean(p)
	var rest []string
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(append([]string{p}, rest...)...)
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}

// within reports whether p is dir or lies below it. Letter case is ignored, since a link on the
// default macOS file system can spell a directory in any case; a false match only refuses.
func within(p, dir string) bool {
	rel, err := filepath.Rel(strings.ToLower(dir), strings.ToLower(p))
	return err == nil && filepath.IsLocal(rel)
}

// planner fills one target's plan.
type planner struct {
	t      *Target
	h      *hub
	opts   Options
	layout paths.Layout

	// desired maps each path the hub produces to its file; units are keyed by path.
	desired map[string]*File
	units   map[string]*Unit
	// held are manifest paths left alone: their source was skipped, failed or could not be listed,
	// or the entry lies outside the target's directories.
	held map[string]bool
	// unmanagedNames maps, per directory, the name of each unit agentport does not manage to its path.
	unmanagedNames map[*kindDir]map[string]string
}

func (p *planner) run() {
	p.desired, p.units, p.held = map[string]*File{}, map[string]*Unit{}, map[string]bool{}
	p.validateManifest()
	existing := p.existingUnits()
	p.convertHub()
	for key, e := range p.t.man.Entries {
		if p.desired[key] == nil && underAny(e.Source, p.h.incomplete) {
			p.held[key] = true
		}
	}
	p.classify(existing)
	p.collectUnmanaged(existing)
	sort.Slice(p.t.Units, func(i, j int) bool {
		a, b := p.t.Units[i], p.t.Units[j]
		return cmpKey(a) < cmpKey(b)
	})
	slices.Sort(p.t.Unmanaged)
}

func cmpKey(u *Unit) string {
	if u.Path != "" {
		return u.Path
	}
	return u.Source
}

// validateManifest holds entries a crafted or hand-edited manifest could use to reach outside the
// target's directories: they are reported and never acted on.
func (p *planner) validateManifest() {
	for _, key := range slices.Sorted(maps.Keys(p.t.man.Entries)) {
		if p.dirOf(key) == nil && !p.underKnownDir(key, p.t.man.Entries[key].Source) {
			p.held[key] = true
			p.t.Warnings = append(p.t.Warnings, fmt.Sprintf("manifest entry %s is outside %s's directories; left alone", key, p.t.Harness.Title()))
		}
	}
}

// dirOf returns the configured directory holding key, a slash path below the root.
func (p *planner) dirOf(key string) *kindDir {
	if path.Clean(key) != key {
		return nil
	}
	for _, d := range p.t.dirs {
		if strings.HasPrefix(key, d.rel+"/") {
			return d
		}
	}
	return nil
}

// underKnownDir accepts a key below a directory of a translation pair no longer configured, such
// as `.gemini/config/skills-ja/...` from `.claude/commands-ja/...`: its source is gone, so it
// becomes an orphan. The source must come from the hub directory with the same suffix.
func (p *planner) underKnownDir(key, source string) bool {
	if path.Clean(key) != key || !filepath.IsLocal(key) {
		return false
	}
	dir, suffix := p.pairDir(key)
	if dir == "" {
		return false
	}
	for _, hd := range p.h.dirs {
		rel, err := filepath.Rel(p.opts.Root, hd)
		if err == nil && strings.HasPrefix(source, filepath.ToSlash(rel)+suffix+"/") {
			return true
		}
	}
	return false
}

// pairDir returns the directory of a translation pair no longer configured that holds key, a
// sibling of a target directory whose name extends it such as `.gemini/config/skills-ja`, and the
// suffix that extends it.
func (p *planner) pairDir(key string) (dir, suffix string) {
	for _, d := range p.t.dirs {
		if d.suffix != "" {
			continue
		}
		parent, base := path.Split(d.rel)
		rest, ok := strings.CutPrefix(key, parent)
		if !ok {
			continue
		}
		if first, _, found := strings.Cut(rest, "/"); found && strings.HasPrefix(first, base) {
			return parent + first, strings.TrimPrefix(first, base)
		}
	}
	return "", ""
}

// kindDirOf returns the directory, configured or a pair no longer configured, that holds key.
func (p *planner) kindDirOf(key string) string {
	if d := p.dirOf(key); d != nil {
		return d.rel
	}
	dir, _ := p.pairDir(key)
	return dir
}

// unitPath returns the unit a target file belongs to: a skill's directory, or the agent file.
func (p *planner) unitPath(key string) (string, ir.Kind) {
	if d := p.dirOf(key); d != nil {
		if d.kind == ir.KindAgent {
			return key, ir.KindAgent
		}
		first, _, _ := strings.Cut(strings.TrimPrefix(key, d.rel+"/"), "/")
		return d.rel + "/" + first, ir.KindSkill
	}
	// A pair no longer configured: the first two components below its parent directory.
	for _, d := range p.t.dirs {
		parent, _ := path.Split(d.rel)
		if rest, ok := strings.CutPrefix(key, parent); ok {
			parts := strings.SplitN(rest, "/", 3)
			if d.kind == ir.KindSkill && strings.HasPrefix(parts[0], path.Base(d.rel)) && len(parts) > 1 {
				return parent + parts[0] + "/" + parts[1], ir.KindSkill
			}
		}
	}
	return key, ir.KindAgent
}

// existingUnit is a unit found on disk in a target directory.
type existingUnit struct {
	path    string
	dir     *kindDir
	linked  bool
	managed bool
}

// existingUnits lists the skill directories and agent files in the target's directories, the way
// the target finds agents, and the name each unit agentport does not manage would load under.
func (p *planner) existingUnits() []existingUnit {
	p.unmanagedNames = map[*kindDir]map[string]string{}
	var out []existingUnit
	for _, d := range p.t.dirs {
		p.unmanagedNames[d] = map[string]string{}
		var rels []string
		if d.kind == ir.KindSkill {
			entries, err := os.ReadDir(d.abs)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				p.t.Warnings = append(p.t.Warnings, err.Error())
			}
			for _, e := range entries {
				if e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
					rels = append(rels, e.Name())
				}
			}
		} else {
			var warnings []string
			var err error
			rels, warnings, err = convert.AgentFiles(p.t.Harness, d.abs)
			p.t.Warnings = append(p.t.Warnings, warnings...)
			if err != nil {
				p.t.Warnings = append(p.t.Warnings, err.Error())
			}
		}
		for _, rel := range rels {
			key := d.rel + "/" + rel
			info, err := os.Lstat(filepath.Join(d.abs, filepath.FromSlash(rel)))
			linked := err == nil && info.Mode()&fs.ModeSymlink != 0
			if !linked {
				linked, _ = safefs.Linked(d.abs, filepath.FromSlash(rel))
			}
			u := existingUnit{path: key, dir: d, linked: linked, managed: p.manages(key, d.kind)}
			out = append(out, u)
			if !u.managed {
				if name := p.unitName(d, rel); name != "" {
					p.unmanagedNames[d][name] = key
				}
			}
		}
	}
	return out
}

// manages reports whether the manifest has an entry for the unit at key.
func (p *planner) manages(key string, kind ir.Kind) bool {
	if kind == ir.KindAgent {
		_, ok := p.t.man.Entries[key]
		return ok
	}
	for k := range p.t.man.Entries {
		if strings.HasPrefix(k, key+"/") {
			return true
		}
	}
	return false
}

// unitName is the name the target loads a unit under; empty when it would not load.
func (p *planner) unitName(d *kindDir, rel string) string {
	abs := filepath.Join(d.abs, filepath.FromSlash(rel))
	if d.kind == ir.KindAgent {
		item, err := convert.ReadAgent(p.t.Harness, abs)
		if err != nil {
			return ""
		}
		return item.Name
	}
	item, err := convert.ReadSkill(p.t.Harness, abs)
	if err != nil {
		return ""
	}
	return item.Name
}

// convertHub converts every hub unit for the target and records the files it produces.
func (p *planner) convertHub() {
	cfg := p.opts.Config
	opts := convert.Options{ModelInvocableCommands: cfg.ModelInvocableCommands, Agents: p.agentNames()}
	type converted struct {
		u   *Unit
		res convert.Result
	}
	var done []converted
	folded := map[string][]*Unit{}
	for _, hu := range p.h.units {
		u := &Unit{Kind: hubKind(hu.kind), Source: hu.rel, hash: hu.hash, dir: p.dir(hubKind(hu.kind), hu.suffix)}
		u.base = u.dir.abs
		switch {
		case hu.reason != "":
			u.Status, u.Reason = Skipped, hu.reason
		case hu.err != nil:
			u.Status, u.Reason = Failed, hu.err.Error()
		}
		if u.Status != "" {
			p.hold(u)
			continue
		}
		res, err := convert.Item(hu.item, p.t.Harness, opts)
		if err != nil {
			u.Status, u.Reason = Failed, err.Error()
			p.hold(u)
			continue
		}
		u.Path, u.Report = p.targetPath(u, hu.item.Name), &res.Report
		target := resolveExisting(filepath.Join(p.opts.Root, filepath.FromSlash(u.Path)))
		if src := resolveExisting(hu.abs); within(target, src) || within(src, target) {
			u.Status, u.Reason = Skipped, "the target already reads the hub's own "+hu.rel+" through a link"
			p.hold(u)
			p.holdPath(u.Path)
			continue
		}
		// macOS and Windows file systems ignore letter case, so Foo and foo are one directory.
		folded[strings.ToLower(u.Path)] = append(folded[strings.ToLower(u.Path)], u)
		done = append(done, converted{u, res})
	}
	for _, c := range done {
		u := c.u
		if group := folded[strings.ToLower(u.Path)]; len(group) > 1 {
			var others []string
			for _, o := range group {
				if o != u {
					others = append(others, o.Path+" (from "+o.Source+")")
				}
			}
			u.Status, u.Reason = Conflict, strings.Join(others, ", ")+" is the same path where letter case is ignored"
			p.hold(u)
			p.holdPath(u.Path)
			continue
		}
		p.units[u.Path] = u
		p.t.Units = append(p.t.Units, u)
		for _, f := range c.res.Files {
			key := u.Path + "/" + f.Path
			if u.Kind == ir.KindAgent {
				key = u.Path
			}
			file := &File{Path: key, desired: true, data: f.Data, mode: f.Mode}
			if file.mode == 0 {
				file.mode = 0o644
			}
			p.desired[key] = file
			u.Files = append(u.Files, file)
		}
	}
}

// hold keeps the manifest entries of a unit that is listed but not converted, so its earlier
// output is neither updated nor deleted.
// holdPath keeps every manifest entry under a refused unit's path, whichever source recorded it,
// since what lies there may be the hub's own files or another unit's.
func (p *planner) holdPath(unit string) {
	for key := range p.t.man.Entries {
		if up, _ := p.unitPath(key); strings.EqualFold(up, unit) {
			p.held[key] = true
		}
	}
}

func (p *planner) hold(u *Unit) {
	for key, e := range p.t.man.Entries {
		if e.Source == u.Source {
			p.held[key] = true
		}
	}
	p.t.Units = append(p.t.Units, u)
}

func (p *planner) dir(kind ir.Kind, suffix string) *kindDir {
	for _, d := range p.t.dirs {
		if d.kind == kind && d.suffix == suffix {
			return d
		}
	}
	return nil
}

func (p *planner) targetPath(u *Unit, name string) string {
	if u.Kind == ir.KindAgent {
		return u.dir.rel + "/" + name + u.dir.ext
	}
	return u.dir.rel + "/" + name
}

// agentNames are the hub's agents and those the target will have: the converted ones, and those
// already in its main agents directory.
func (p *planner) agentNames() *convert.AgentNames {
	if len(p.h.agents) == 0 {
		return nil
	}
	present := map[string]bool{}
	for _, hu := range p.h.units {
		if hu.kind == ir.KindAgent && hu.suffix == "" && hu.item != nil && hu.reason == "" {
			present[hu.item.Name] = true
		}
	}
	if d := p.dir(ir.KindAgent, ""); d != nil {
		for _, name := range convert.ReadAgentNames(p.t.Harness, d.abs) {
			present[name] = true
		}
	}
	return &convert.AgentNames{Known: p.h.agents, Present: present}
}

// classify decides each file's state, then applies the rules that make a whole unit a conflict.
func (p *planner) classify(existing []existingUnit) {
	linked := map[string]bool{}
	for _, e := range existing {
		if e.linked {
			linked[e.path] = true
		}
	}
	// Managed paths the hub no longer produces belong to a converted unit or to an orphan unit.
	orphans := map[string]*Unit{}
	for key, e := range p.t.man.Entries {
		if p.held[key] || p.desired[key] != nil {
			continue
		}
		up, kind := p.unitPath(key)
		u := p.units[up]
		if u == nil {
			if u = orphans[up]; u == nil {
				u = &Unit{Kind: kind, Path: up, Source: e.Source, dir: p.dirOf(key)}
				u.base = filepath.Join(p.opts.Root, filepath.FromSlash(p.kindDirOf(key)))
				if u.dir == nil {
					if err := outsideHub(u.base, append([]string{p.h.dir}, p.h.dirs...)); err != nil {
						u.Status, u.Reason = Conflict, err.Error()
					}
				}
				orphans[up] = u
				p.t.Units = append(p.t.Units, u)
			}
		}
		u.Files = append(u.Files, &File{Path: key})
	}
	for _, u := range p.t.Units {
		if u.Status != "" {
			continue
		}
		if linked[u.Path] {
			u.Status, u.Reason = Conflict, "it is a symbolic link; agentport never writes through one"
			continue
		}
		for _, f := range u.Files {
			f.State = p.classifyFile(f)
		}
		p.unitConflict(u)
		sort.Slice(u.Files, func(i, j int) bool { return u.Files[i].Path < u.Files[j].Path })
	}
}

func (p *planner) classifyFile(f *File) manifest.State {
	o := manifest.Observation{}
	if e, ok := p.t.man.Entries[f.Path]; ok {
		o.Recorded = &e
	}
	if d := p.desired[f.Path]; d != nil {
		o.SourceExists = true
		o.Output = manifest.Fingerprint(d.data, d.mode)
	}
	abs := filepath.Join(p.opts.Root, filepath.FromSlash(f.Path))
	if kd := p.kindDirOf(f.Path); kd != "" {
		base := filepath.Join(p.opts.Root, filepath.FromSlash(kd))
		rel, _ := filepath.Rel(base, abs)
		if linked, err := safefs.Linked(base, rel); linked || err != nil {
			return manifest.StateConflict
		}
	}
	// Anything that cannot be read as a regular file is someone else's, or in the way.
	info, err := os.Lstat(abs)
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return manifest.StateConflict
	case err == nil && !info.Mode().IsRegular():
		return manifest.StateConflict
	case err == nil:
		data, err := os.ReadFile(abs)
		if err != nil {
			return manifest.StateConflict
		}
		o.TargetExists, o.Target = true, manifest.Fingerprint(data, info.Mode())
	}
	return manifest.Classify(o)
}

// unitConflict turns a unit into a conflict when what is in the way belongs to someone else: its
// entry file is unmanaged and different, or another unit the target loads under the same name is.
func (p *planner) unitConflict(u *Unit) {
	if len(u.Files) == 0 || u.dir == nil {
		return
	}
	entry := u.Path
	if u.Kind == ir.KindSkill {
		entry = u.Path + "/" + skilldir.Entry
	}
	for _, f := range u.Files {
		if f.Path == entry && f.State == manifest.StateConflict {
			u.Status, u.Reason = Conflict, path.Base(entry)+" there is not agentport's, or cannot be written as a file"
			return
		}
	}
	if p.desired[entry] == nil {
		return
	}
	name := strings.TrimSuffix(path.Base(u.Path), u.dir.ext)
	if other, ok := p.unmanagedNames[u.dir][name]; ok && other != u.Path {
		u.Status, u.Reason = Conflict, fmt.Sprintf("%s, not written by agentport, is also named %s; %s loads only one", other, name, p.t.Harness.Title())
	}
}

// collectUnmanaged lists units nobody claims, and stray files inside the units agentport manages.
func (p *planner) collectUnmanaged(existing []existingUnit) {
	for _, e := range existing {
		u := p.units[e.path]
		if u == nil && !e.managed {
			p.t.Unmanaged = append(p.t.Unmanaged, e.path)
			continue
		}
		if e.dir.kind != ir.KindSkill || e.linked || u == nil || u.Status != "" {
			continue
		}
		root := filepath.Join(p.opts.Root, filepath.FromSlash(e.path))
		_ = filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || skilldir.Leftover(d.Name()) {
				return nil
			}
			rel, _ := filepath.Rel(p.opts.Root, abs)
			key := filepath.ToSlash(rel)
			if _, managed := p.t.man.Entries[key]; !managed && p.desired[key] == nil {
				p.t.Unmanaged = append(p.t.Unmanaged, key)
			}
			return nil
		})
	}
}
