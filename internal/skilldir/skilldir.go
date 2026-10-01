// Package skilldir reads a skill directory — SKILL.md plus bundled files — and lays one out again.
package skilldir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/ir"
)

// Entry is the instruction file every harness looks for in a skill directory.
const Entry = "SKILL.md"

// Read parses dir/SKILL.md and returns every other regular file under dir, slash-separated and in
// path order, with its mode. Python caches and .DS_Store are skipped silently; symlinked
// directories and special files are skipped with a note, so the loss report can say so. A
// symlinked skill directory is read through its link.
func Read(dir string) (*frontmatter.Document, []ir.Resource, []ir.Note, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read %s: %w", dir, err)
	}
	data, err := os.ReadFile(filepath.Join(root, Entry))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil, fmt.Errorf("%s has no %s", dir, Entry)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", filepath.Join(dir, Entry), err)
	}

	var res []ir.Resource
	var notes []ir.Note
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir() && d.Name() == "__pycache__":
			return filepath.SkipDir
		case d.IsDir(), rel == Entry, d.Name() == ".DS_Store", strings.HasSuffix(d.Name(), ".pyc"):
			return nil
		}
		info, err := os.Stat(p)
		if err != nil {
			notes = append(notes, ir.Note{Field: "resources", Detail: fmt.Sprintf("skipped %s: %v", rel, err)})
			return nil
		}
		switch {
		case info.IsDir():
			notes = append(notes, ir.Note{Field: "resources", Detail: "skipped " + rel + ": symlinked directory, not followed"})
			return nil
		case !info.Mode().IsRegular():
			notes = append(notes, ir.Note{Field: "resources", Detail: "skipped " + rel + ": not a regular file"})
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			notes = append(notes, ir.Note{Field: "resources", Detail: rel + " is a symlink; its target's content was copied"})
		}
		res = append(res, ir.Resource{Path: rel, Mode: info.Mode().Perm(), Data: body})
		return nil
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read %s: %w", dir, err)
	}
	return doc, res, notes, nil
}

// Layout returns SKILL.md followed by the resources in path order, ready to write under a skill
// directory.
func Layout(doc *frontmatter.Document, resources []ir.Resource) ([]ir.Resource, error) {
	entry, err := doc.Marshal()
	if err != nil {
		return nil, err
	}
	out := []ir.Resource{{Path: Entry, Mode: 0o644, Data: entry}}
	rest := slices.Clone(resources)
	slices.SortFunc(rest, func(a, b ir.Resource) int { return strings.Compare(a.Path, b.Path) })
	return append(out, rest...), nil
}

// Write creates dir and writes files into it with their modes. Nothing lands outside dir: every
// path must be local, dir itself must not be a symlink, and writes go through an os.Root. Each
// file is replaced atomically, so a read-only file from an earlier run is no obstacle.
func Write(dir string, files []ir.Resource) error {
	for _, f := range files {
		if f.Path == "" || !filepath.IsLocal(filepath.FromSlash(f.Path)) || path.Clean(f.Path) != f.Path {
			return fmt.Errorf("refusing to write %q outside %s", f.Path, dir)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(dir); err != nil {
		return err
	} else if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write through the symlink %s", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range files {
		if err := writeFile(root, filepath.FromSlash(f.Path), f); err != nil {
			return fmt.Errorf("write %s: %w", filepath.Join(dir, f.Path), err)
		}
	}
	return nil
}

func writeFile(root *os.Root, name string, f ir.Resource) error {
	if parent := filepath.Dir(name); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	mode := f.Mode
	if mode == 0 {
		mode = 0o644
	}
	tmp := name + ".agentport-tmp"
	w, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := w.Write(f.Data); err != nil {
		w.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Chmod(tmp, mode); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return root.Rename(tmp, name)
}

// FirstLine returns the first non-empty line of body without heading markers. Claude Code falls back
// to the first body line when a skill has no description; a target that requires one gets this.
func FirstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			return line
		}
	}
	return ""
}
