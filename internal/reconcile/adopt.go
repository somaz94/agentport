package reconcile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/adapters/claude"
	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/args"
	"github.com/somaz94/agentport/internal/config"
	"github.com/somaz94/agentport/internal/convert"
	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/safefs"
	"github.com/somaz94/agentport/internal/skilldir"
)

// ErrHubChanged means what the hub converts to changed after agentport wrote the target unit,
// through the hub, the settings or agentport itself, so adopting the unit as it is would undo that.
var ErrHubChanged = errors.New("the conversion changed since agentport wrote this")

// Adoption is what adopting an edited target unit would change in the hub.
type Adoption struct {
	Harness harness.ID
	// Unit is the target unit's slash-separated path below the root; Source is the hub item's.
	Unit, Source string
	Changes      []Change
	// Notes say what the target holds that is not adopted, and why.
	Notes []string
	// Reformat is set when the unit differs from what the hub converts to in nothing adopt can
	// carry, such as quoting or key order; sync --force rewrites it.
	Reformat bool
	adopter  *adopter
}

// Change is one hub file that adopting writes.
type Change struct {
	// Path is slash-separated below the root.
	Path string
	// Old is the file's current content, nil when adopting creates it.
	Old, New []byte
	Mode     fs.FileMode
}

// Adopt plans bringing the edits made to the target unit at target, a skill directory, a file in
// one, or an agent file, back into the hub item agentport converted it from. The hub must still
// convert to what agentport recorded writing, or adopting would undo the hub's own changes since;
// opts.Force adopts anyway unless the hub item now converts to another path.
func Adopt(opts Options, target string) (*Adoption, error) {
	if opts.Config == nil {
		opts.Config = config.Default()
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(opts.Root, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("%s is not under %s", target, opts.Root)
	}
	a := &adopter{opts: opts}
	if err := a.locate(filepath.ToSlash(rel)); err != nil {
		return nil, err
	}
	if err := a.readSource(); err != nil {
		return nil, err
	}
	if err := a.compare(); err != nil {
		return nil, err
	}
	return a.out, nil
}

// Apply writes the changes into the hub. The hub's new conversion then becomes what the manifest
// compares the unit with, so what is left of the edit is what the hub cannot hold.
func (a *Adoption) Apply() error {
	if len(a.Changes) == 0 {
		return nil
	}
	for _, c := range a.Changes {
		// A hub file kept as a link, as dotfiles managers keep it, is updated where it lives.
		abs := resolveExisting(filepath.Join(a.adopter.opts.Root, filepath.FromSlash(c.Path)))
		dir := filepath.Dir(abs)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		err = safefs.WriteFile(root, filepath.Base(abs), c.New, c.Mode)
		root.Close()
		if err != nil {
			return fmt.Errorf("write %s: %w", c.Path, err)
		}
	}
	return a.adopter.rebase()
}

// rebase converts the adopted hub item again and records that output for the files of the unit
// agentport already tracks. A file it does not track is recorded by sync only once it holds
// exactly the output, so adopting never claims someone else's file.
func (a *adopter) rebase() error {
	if err := a.readSource(); err != nil {
		return err
	}
	m, err := manifest.Load(a.manifestRoot)
	if err != nil {
		return err
	}
	hash := a.sourceHash()
	for key, f := range a.desired {
		e, tracked := a.entries[key]
		// A file nobody edited stays recorded as written, so sync still updates it.
		if !tracked || a.onDisk[key] == e.Fingerprint() {
			continue
		}
		m.Entries[key] = manifest.Entry{Source: a.out.Source, SourceHash: hash, OutputHash: manifest.Hash(f.data),
			Mode: manifest.Mode(f.mode), Generator: a.opts.Generator}
	}
	return m.Save(a.manifestRoot)
}

func (a *adopter) sourceHash() string {
	if a.hubKind == ir.KindSkill {
		return skillHash(a.hubAbs, a.item)
	}
	return fileHash(a.hubAbs)
}

type adopter struct {
	opts Options
	out  *Adoption

	to           harness.ID
	dir          *kindDir
	manifestRoot string
	entries      map[string]manifest.Entry
	// onDisk are the fingerprints of the unit's files as compare found them.
	onDisk map[string]string

	hubKind ir.Kind
	hubAbs  string
	item    *ir.Item
	desired map[string]*File
}

// locate finds the target harness, directory and unit that hold rel, and the manifest entries for
// the unit's files.
func (a *adopter) locate(rel string) error {
	for _, h := range harness.All {
		if h == a.opts.Config.Hub {
			continue
		}
		l, err := paths.For(h)
		if err != nil {
			return err
		}
		for _, suffix := range append([]string{""}, a.opts.Config.Pairs...) {
			for _, kind := range []ir.Kind{ir.KindSkill, ir.KindAgent} {
				d, err := l.Dir(paths.ScopeUser, kind)
				if err != nil {
					continue
				}
				kd := &kindDir{kind: kind, rel: filepath.ToSlash(d) + suffix, abs: filepath.Join(a.opts.Root, d) + suffix, suffix: suffix}
				below, ok := strings.CutPrefix(rel, kd.rel+"/")
				if !ok {
					continue
				}
				if kind == ir.KindAgent {
					kd.ext = convert.AgentExt(h)
				}
				unit := rel
				if kind == ir.KindSkill {
					first, _, _ := strings.Cut(below, "/")
					unit = kd.rel + "/" + first
				}
				a.to, a.dir = h, kd
				a.out = &Adoption{Harness: h, Unit: unit, adopter: a}
				return a.loadEntries()
			}
		}
	}
	return fmt.Errorf("%s is not in a skills or agents directory of %s", rel, strings.Join(targetNames(a.opts.Config.Hub), " or "))
}

func targetNames(hubID harness.ID) []string {
	var out []string
	for _, h := range harness.All {
		if h != hubID {
			out = append(out, h.Title())
		}
	}
	return out
}

func (a *adopter) loadEntries() error {
	l, err := paths.For(a.to)
	if err != nil {
		return err
	}
	rootRel, err := l.Root(paths.ScopeUser)
	if err != nil {
		return err
	}
	a.manifestRoot = filepath.Join(a.opts.Root, rootRel)
	m, err := manifest.Load(a.manifestRoot)
	if err != nil {
		return err
	}
	a.entries = map[string]manifest.Entry{}
	for key, e := range m.Entries {
		if key == a.out.Unit || strings.HasPrefix(key, a.out.Unit+"/") {
			a.entries[key] = e
			if a.out.Source != "" && a.out.Source != e.Source {
				return fmt.Errorf("the manifest records %s as coming from both %s and %s", a.out.Unit, a.out.Source, e.Source)
			}
			a.out.Source = e.Source
		}
	}
	if len(a.entries) == 0 {
		return fmt.Errorf("%s was not written by agentport, so there is nothing to adopt", a.out.Unit)
	}
	return nil
}

// readSource reads the hub item the unit came from and converts it again, as sync would.
func (a *adopter) readSource() error {
	hubID := a.opts.Config.Hub
	l, err := paths.For(hubID)
	if err != nil {
		return err
	}
	src := a.out.Source
	// A manifest can arrive with a cloned repository, so its source must not steer a write elsewhere.
	if path.Clean(src) != src || !filepath.IsLocal(filepath.FromSlash(src)) {
		return fmt.Errorf("the manifest names %q as the source, which is not a path below %s", src, a.opts.Root)
	}
	a.hubAbs = filepath.Join(a.opts.Root, filepath.FromSlash(src))
	if _, err := os.Stat(a.hubAbs); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("its source %s is gone, so there is nothing to adopt into", src)
	} else if err != nil {
		return err
	}
	for _, suffix := range append([]string{""}, a.opts.Config.Pairs...) {
		for _, kind := range []ir.Kind{ir.KindSkill, ir.KindCommand, ir.KindAgent} {
			d, err := l.Dir(paths.ScopeUser, kind)
			if err != nil {
				continue
			}
			below, ok := strings.CutPrefix(a.out.Source, filepath.ToSlash(d)+suffix+"/")
			if !ok {
				continue
			}
			a.hubKind = kind
			switch kind {
			case ir.KindSkill:
				a.item, err = convert.ReadSkill(hubID, a.hubAbs)
			case ir.KindCommand:
				a.item, err = convert.ReadCommand(hubID, a.hubAbs, below)
			default:
				a.item, err = convert.ReadAgent(hubID, a.hubAbs)
			}
			if err != nil {
				return err
			}
			return a.convert()
		}
	}
	return fmt.Errorf("the manifest names %s as the source, which is not a hub skill, command or agent", a.out.Source)
}

func (a *adopter) convert() error {
	res, err := convert.Item(a.item, a.to, convert.Options{ModelInvocableCommands: a.opts.Config.ModelInvocableCommands})
	if err != nil {
		return err
	}
	unit := a.dir.rel + "/" + a.item.Name
	if a.dir.kind == ir.KindAgent {
		unit += a.dir.ext
	}
	if unit != a.out.Unit {
		return fmt.Errorf("%w: %s now converts to %s", ErrHubChanged, a.out.Source, unit)
	}
	a.desired = map[string]*File{}
	for _, f := range res.Files {
		key := unit + "/" + f.Path
		if a.dir.kind == ir.KindAgent {
			key = unit
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		a.desired[key] = &File{Path: key, desired: true, data: f.Data, mode: mode}
	}
	return nil
}

// compare finds what differs between the target unit and what the hub converts to, and turns each
// difference into a hub change or a note.
func (a *adopter) compare() error {
	onDisk, err := a.unitFiles()
	if err != nil {
		return err
	}
	a.onDisk = map[string]string{}
	for key, f := range onDisk {
		a.onDisk[key] = manifest.Fingerprint(f.content, f.mode)
	}
	recorded, converted := map[string]string{}, map[string]string{}
	for key, e := range a.entries {
		recorded[key] = e.Fingerprint()
	}
	for key, d := range a.desired {
		converted[key] = manifest.Fingerprint(d.data, d.mode)
	}
	// Not edited since agentport wrote it, or already what the hub converts to.
	if matches(onDisk, recorded) || matches(onDisk, converted) {
		return nil
	}
	if !maps.Equal(recorded, converted) && !a.opts.Force {
		what := "the changes made to " + a.out.Source + " since"
		if a.sourceUnchanged() {
			what = "what agentport now writes for " + a.out.Source + " (a newer agentport, or other settings)"
		}
		return fmt.Errorf("%w: adopting %s would undo %s; run sync to see the difference, or pass --force", ErrHubChanged, a.out.Unit, what)
	}
	entry := a.out.Unit
	if a.dir.kind == ir.KindSkill {
		entry += "/" + skilldir.Entry
	}
	// A sidecar agentport generated only states the invocation policy, which the merge compares.
	sidecar := a.out.Unit + "/" + common.CodexSidecar
	if a.dir.kind != ir.KindSkill || a.hubHasSidecar() {
		sidecar = ""
	}
	needMerge := false
	for key, data := range onDisk {
		d := a.desired[key]
		if d != nil && bytes.Equal(d.data, data.content) && d.mode == data.mode {
			continue
		}
		switch {
		case key == entry:
			needMerge = true
		case key == sidecar:
			needMerge = true
			if s := string(data.content); s != common.GeneratedSidecar && s != common.DefaultSidecar {
				a.note("%s: Codex-only settings beyond the invocation policy; not adopted", key)
			}
		case a.hubKind != ir.KindSkill:
			a.note("%s: a %s has no bundled files; not adopted", key, a.hubKind)
		default:
			if err := a.copyBack(key, data); err != nil {
				return err
			}
		}
	}
	for key := range a.desired {
		if _, ok := onDisk[key]; !ok {
			if key == sidecar {
				needMerge = true
				continue
			}
			a.note("%s was deleted in the target; delete it from the hub by hand if that was meant", key)
		}
	}
	if needMerge {
		if err := a.mergeEntry(); err != nil {
			return err
		}
	}
	slices.SortFunc(a.out.Changes, func(x, y Change) int { return strings.Compare(x.Path, y.Path) })
	slices.Sort(a.out.Notes)
	a.out.Reformat = len(a.out.Changes) == 0 && len(a.out.Notes) == 0
	return nil
}

// sourceUnchanged reports whether the hub item is what it was when each file was written, so a
// different conversion comes from agentport or its settings.
func (a *adopter) sourceUnchanged() bool {
	hash := a.sourceHash()
	for _, e := range a.entries {
		if e.SourceHash != hash {
			return false
		}
	}
	return true
}

type fileData struct {
	content []byte
	mode    fs.FileMode
}

// matches reports whether the files on disk are exactly those fingerprints lists.
func matches(onDisk map[string]fileData, fingerprints map[string]string) bool {
	if len(onDisk) != len(fingerprints) {
		return false
	}
	for key, f := range onDisk {
		if fp, ok := fingerprints[key]; !ok || fp != manifest.Fingerprint(f.content, f.mode) {
			return false
		}
	}
	return true
}

// unitFiles reads every regular file of the target unit.
func (a *adopter) unitFiles() (map[string]fileData, error) {
	out := map[string]fileData{}
	abs := filepath.Join(a.opts.Root, filepath.FromSlash(a.out.Unit))
	err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.IsDir() && d.Name() == "__pycache__":
			return filepath.SkipDir
		case d.IsDir(), !d.Type().IsRegular(), skilldir.Leftover(d.Name()):
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(a.opts.Root, p)
		out[filepath.ToSlash(rel)] = fileData{data, info.Mode().Perm()}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist", a.out.Unit)
	}
	return out, err
}

func (a *adopter) hubHasSidecar() bool {
	for _, r := range a.item.Resources {
		if r.Path == common.CodexSidecar {
			return true
		}
	}
	return false
}

func (a *adopter) note(format string, args ...any) {
	a.out.Notes = append(a.out.Notes, fmt.Sprintf(format, args...))
}

// copyBack adopts a bundled file byte for byte, as sync copied it out.
func (a *adopter) copyBack(key string, data fileData) error {
	below := strings.TrimPrefix(key, a.out.Unit+"/")
	hubPath := a.out.Source + "/" + below
	// The hub reader does not follow a linked directory inside a skill, so nothing written
	// through one would be read back; and writing over a link to nothing would replace the link.
	if sub := path.Dir(below); sub != "." {
		if linked, err := safefs.Linked(a.hubAbs, filepath.FromSlash(sub)); err != nil || linked {
			a.note("%s: %s/%s is a link the hub does not read through; not adopted", key, a.out.Source, sub)
			return nil
		}
	}
	if dangling(filepath.Join(a.opts.Root, filepath.FromSlash(hubPath))) {
		a.note("%s: %s is a link to a missing file; not adopted", key, hubPath)
		return nil
	}
	old, err := os.ReadFile(filepath.Join(a.opts.Root, filepath.FromSlash(hubPath)))
	if errors.Is(err, fs.ErrNotExist) {
		old = nil
	} else if err != nil {
		return err
	}
	a.out.Changes = append(a.out.Changes, Change{Path: hubPath, Old: old, New: data.content, Mode: data.mode})
	return nil
}

// mergeEntry adopts the fields of the edited SKILL.md or agent file that the hub item can hold,
// keeping every other hub field as written.
func (a *adopter) mergeEntry() error {
	edited, err := a.readTarget(filepath.Join(a.opts.Root, filepath.FromSlash(a.out.Unit)))
	if err != nil {
		return fmt.Errorf("read the edited %s: %w", a.out.Unit, err)
	}
	generated, err := a.readGenerated()
	if err != nil {
		return err
	}
	hubFile := a.hubAbs
	if a.hubKind == ir.KindSkill {
		hubFile = filepath.Join(a.hubAbs, skilldir.Entry)
	}
	old, err := os.ReadFile(hubFile)
	if err != nil {
		return err
	}
	doc, err := frontmatter.Parse(old)
	if err != nil {
		return fmt.Errorf("%s: %w", hubFile, err)
	}
	m := &merge{a: a, doc: doc}
	m.fields(edited, generated)
	if !m.front && !m.body {
		return nil
	}
	var data []byte
	if !m.front {
		data, _ = frontmatter.ReplaceBody(old, doc.Body)
	}
	if data == nil {
		if data, err = doc.Marshal(); err != nil {
			return err
		}
	}
	if bytes.Equal(data, old) {
		return nil
	}
	info, err := os.Stat(hubFile)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(a.opts.Root, hubFile)
	a.out.Changes = append(a.out.Changes, Change{Path: filepath.ToSlash(rel), Old: old, New: data, Mode: info.Mode().Perm()})
	return nil
}

func (a *adopter) readTarget(abs string) (*ir.Item, error) {
	if a.dir.kind == ir.KindAgent {
		return convert.ReadAgent(a.to, abs)
	}
	return convert.ReadSkill(a.to, abs)
}

// readGenerated reads what the hub converts to back with the target's reader, so both sides of
// the comparison went through the same parser.
func (a *adopter) readGenerated() (*ir.Item, error) {
	tmp, err := os.MkdirTemp("", "agentport-adopt-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	unit := filepath.Join(tmp, path.Base(a.out.Unit))
	for key, f := range a.desired {
		p := unit
		if a.dir.kind == ir.KindSkill {
			p = filepath.Join(unit, filepath.FromSlash(strings.TrimPrefix(key, a.out.Unit+"/")))
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, f.data, f.mode); err != nil {
			return nil, err
		}
	}
	return a.readTarget(unit)
}

// merge applies the differences between the edited and the generated item to the hub document.
type merge struct {
	a           *adopter
	doc         *frontmatter.Document
	front, body bool
}

func (m *merge) set(key string, value *yaml.Node) {
	if value == nil {
		if _, ok := m.doc.Get(key); !ok {
			return
		}
		m.doc.Delete(key)
	} else {
		m.doc.Set(key, value)
	}
	m.front = true
}

func str(s string) *yaml.Node {
	if s == "" {
		return nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func (m *merge) fields(edited, generated *ir.Item) {
	a := m.a
	title := a.to.Title()
	if edited.Name != generated.Name {
		a.note("name: renaming in the target is not adopted; rename the hub item instead")
	}
	if edited.Description != generated.Description {
		if _, ok := m.doc.Get("when_to_use"); ok && a.hubKind != ir.KindAgent {
			a.note("description: the hub joins description and when_to_use into it; edit them by hand")
		} else {
			m.set("description", str(edited.Description))
		}
	}
	eb, _ := args.Strip(edited.Body)
	gb, _ := args.Strip(generated.Body)
	if eb != gb {
		m.doc.Body, m.body = eb, true
	}
	if a.hubKind == ir.KindAgent {
		m.agent(edited, generated)
	} else {
		m.skill(edited, generated)
	}
	for _, key := range changedExtensions(edited, generated) {
		if a.hubKind != ir.KindAgent && common.PortableKeys[key] {
			v, _ := edited.Extension(key)
			m.set(key, v)
			continue
		}
		a.note("%s: %s-only; not adopted", key, title)
	}
}

func (m *merge) skill(edited, generated *ir.Item) {
	a := m.a
	if edited.Invocation.ArgumentHint != generated.Invocation.ArgumentHint {
		m.set("argument-hint", str(edited.Invocation.ArgumentHint))
	}
	if edited.Invocation.UserInvocable != generated.Invocation.UserInvocable {
		var v *yaml.Node
		if !edited.Invocation.UserInvocable {
			v = common.BoolNode(false)
		}
		m.set("user-invocable", v)
	}
	if edited.Invocation.ModelInvocable != generated.Invocation.ModelInvocable {
		if a.hubKind == ir.KindCommand {
			a.note("model invocation: converted commands follow the modelInvocableCommands setting; not adopted")
			return
		}
		var v *yaml.Node
		if !edited.Invocation.ModelInvocable {
			v = common.BoolNode(true)
		}
		m.set("disable-model-invocation", v)
	}
}

func (m *merge) agent(edited, generated *ir.Item) {
	a := m.a
	title := a.to.Title()
	if model(edited.Model) != model(generated.Model) {
		a.note("model: Claude Code has no %s model; not adopted", edited.Model)
	}
	if edited.Effort != generated.Effort {
		switch edited.Effort {
		case "", "low", "medium", "high", "xhigh", "max":
			m.set("effort", str(edited.Effort))
		default:
			a.note("effort: Claude Code has no %s effort; not adopted", edited.Effort)
		}
	}
	if !slices.Equal(edited.Preload, generated.Preload) {
		if len(edited.Preload) == 0 {
			m.set("skills", nil)
		} else {
			was, _ := m.doc.Get("skills")
			if was != nil && was.Kind != yaml.SequenceNode {
				m.set("skills", str(strings.Join(edited.Preload, ", ")))
			} else {
				m.doc.SetList("skills", edited.Preload)
				m.front = true
			}
		}
	}
	et, gt := edited.Tools.Granted(), generated.Tools.Granted()
	removed := slices.DeleteFunc(slices.Clone(gt), func(c ir.Capability) bool { return slices.Contains(et, c) })
	added := slices.DeleteFunc(slices.Clone(et), func(c ir.Capability) bool { return slices.Contains(gt, c) })
	if len(removed)+len(added) > 0 {
		switch {
		case a.to == harness.Codex:
			a.note("tools: Codex has no per-agent tool list; not adopted")
		case !claude.AdoptTools(m.doc, removed, added):
			a.note("tools: the edit leaves no tool, which is not adopted")
		default:
			m.front = true
		}
	}
	if extra := slices.DeleteFunc(slices.Clone(edited.Tools.Unknown), func(n string) bool { return slices.Contains(generated.Tools.Unknown, n) }); len(extra) > 0 {
		a.note("tools: %s has no Claude Code counterpart for %s; not adopted", title, strings.Join(extra, ", "))
	}
}

func model(m string) string {
	if strings.EqualFold(m, "inherit") {
		return ""
	}
	return m
}

// changedExtensions lists the keys only one side has, or that differ, in order.
func changedExtensions(edited, generated *ir.Item) []string {
	var keys []string
	for _, f := range append(slices.Clone(edited.Extensions), generated.Extensions...) {
		if !slices.Contains(keys, f.Key) {
			keys = append(keys, f.Key)
		}
	}
	var out []string
	for _, key := range keys {
		e, eok := edited.Extension(key)
		g, gok := generated.Extension(key)
		if eok != gok || eok && encode(e) != encode(g) {
			out = append(out, key)
		}
	}
	return out
}

func encode(n *yaml.Node) string {
	data, _ := yaml.Marshal(n)
	return string(data)
}
