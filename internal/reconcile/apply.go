package reconcile

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/safefs"
)

// Apply carries out the plan: it writes new and updated files, deletes orphans, and records the
// outcome in each target's manifest. A target whose plan failed is left alone.
func (p *Plan) Apply() error {
	var errs []error
	for _, t := range p.Targets {
		if t.Err != nil {
			continue
		}
		if err := p.applyTarget(t); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.Harness.Title(), err))
		}
	}
	return errors.Join(errs...)
}

func (p *Plan) applyTarget(t *Target) (err error) {
	before := maps.Clone(t.man.Entries)
	r := &roots{open: map[string]*os.Root{}, hubDirs: p.hubDirs}
	defer r.close()
	defer func() {
		// Whatever was written is recorded, even when a later file failed.
		if maps.Equal(before, t.man.Entries) {
			return
		}
		if saveErr := t.man.Save(t.root); saveErr != nil {
			err = errors.Join(err, saveErr)
		}
	}()

	// Writes come before deletions, so a failure part way never leaves a unit with its old files
	// gone and its new ones missing.
	for _, u := range t.Units {
		if u.Status != "" {
			continue
		}
		for _, f := range u.Files {
			switch {
			case f.State == manifest.StateNew, f.State == manifest.StateUpdate, f.State == manifest.StateDrift && p.opts.Force && f.desired:
				if err := r.write(u.base, f, p.opts.Root); err != nil {
					return err
				}
				t.man.Entries[f.Path] = p.entry(u, f)
			case f.State == manifest.StateUnchanged:
				t.man.Entries[f.Path] = p.entry(u, f)
			case f.State == manifest.StateDrift && p.opts.Force:
				// An edited file whose source is gone is kept, and no longer tracked.
				delete(t.man.Entries, f.Path)
			}
		}
	}
	for _, u := range t.Units {
		if u.Status != "" {
			continue
		}
		for _, f := range u.Files {
			if f.State != manifest.StateOrphan {
				continue
			}
			if err := r.remove(u.base, f, p.opts.Root); err != nil {
				return err
			}
			delete(t.man.Entries, f.Path)
		}
	}
	return nil
}

func (p *Plan) entry(u *Unit, f *File) manifest.Entry {
	return manifest.Entry{
		Source:     u.Source,
		SourceHash: u.hash,
		OutputHash: manifest.Hash(f.data),
		Mode:       manifest.Mode(f.mode),
		Generator:  p.opts.Generator,
	}
}

// roots opens each target directory once, as an os.Root, so no write or deletion can follow a
// symbolic link out of it; and refuses any whose path resolves into the hub.
type roots struct {
	open    map[string]*os.Root
	hubDirs []string
}

// guard is the last check before a write or deletion: whatever the plan said, a path that
// resolves into the hub is never touched.
func (r *roots) guard(base, name string) error {
	abs := resolveExisting(filepath.Join(base, name))
	for _, d := range r.hubDirs {
		if within(abs, resolveExisting(d)) {
			return fmt.Errorf("refusing to change %s: it resolves into the hub's %s", filepath.Join(base, name), d)
		}
	}
	return nil
}

func (r *roots) get(dir string, create bool) (*os.Root, error) {
	if root := r.open[dir]; root != nil {
		return root, nil
	}
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	r.open[dir] = root
	return root, nil
}

func (r *roots) write(base string, f *File, scopeRoot string) error {
	root, err := r.get(base, true)
	if err != nil {
		return err
	}
	name, err := below(base, f.Path, scopeRoot)
	if err != nil {
		return err
	}
	if err := r.guard(base, name); err != nil {
		return err
	}
	if err := safefs.WriteFile(root, name, f.data, f.mode); err != nil {
		return fmt.Errorf("write %s: %w", f.Path, err)
	}
	return nil
}

func (r *roots) remove(base string, f *File, scopeRoot string) error {
	root, err := r.get(base, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	name, err := below(base, f.Path, scopeRoot)
	if err != nil {
		return err
	}
	if err := r.guard(base, name); err != nil {
		return err
	}
	if err := safefs.Remove(root, name); err != nil {
		return fmt.Errorf("delete %s: %w", f.Path, err)
	}
	return nil
}

func (r *roots) close() {
	for _, root := range r.open {
		root.Close()
	}
}

// below returns key, a slash path under scopeRoot, relative to base.
func below(base, key, scopeRoot string) (string, error) {
	rel, err := filepath.Rel(base, filepath.Join(scopeRoot, filepath.FromSlash(key)))
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%s is outside %s", key, base)
	}
	return rel, nil
}
