// Package safefs writes and removes files below a directory without following a symbolic link out
// of it, and without leaving a half-written file behind.
package safefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WriteFile replaces name, a path below root, with data and mode: written to a temporary file,
// given its mode and flushed, then renamed into place, so a read-only file from an earlier run is
// no obstacle and a crash never leaves a partly written file under name.
func WriteFile(root *os.Root, name string, data []byte, mode fs.FileMode) (err error) {
	if parent := filepath.Dir(name); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	if mode == 0 {
		mode = 0o644
	}
	tmp := name + ".agentport-tmp"
	// Remove never follows a link, so a leftover or planted entry is cleared, not written through.
	if err := root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	w, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	_, err = w.Write(data)
	if err == nil {
		err = w.Chmod(mode)
	}
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return root.Rename(tmp, name)
}

// Remove deletes the file name below root, then each parent directory up to root that the removal
// left empty. A file already gone is not an error.
func Remove(root *os.Root, name string) error {
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for dir := filepath.Dir(name); dir != "."; dir = filepath.Dir(dir) {
		// Remove refuses a non-empty directory, which ends the walk.
		if root.Remove(dir) != nil {
			break
		}
	}
	return nil
}

// Linked reports whether name, a path below dir, or any directory between them is a symbolic link.
// Writing there would land somewhere other than the path says.
func Linked(dir, name string) (bool, error) {
	p := dir
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(name)), "/") {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}
