package safefs

import (
	"os"
	"path/filepath"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	must(t, err)
	t.Cleanup(func() { root.Close() })
	return root
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	root := openRoot(t, dir)
	must(t, WriteFile(root, filepath.Join("a", "b", "run.sh"), []byte("x"), 0o755))
	info, err := os.Stat(filepath.Join(dir, "a", "b", "run.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("written file = %v, %v; want mode 0755", info, err)
	}
	// A read-only file from an earlier run is replaced, not refused.
	must(t, WriteFile(root, "ro.txt", []byte("1"), 0o444))
	must(t, WriteFile(root, "ro.txt", []byte("2"), 0))
	data, _ := os.ReadFile(filepath.Join(dir, "ro.txt"))
	info, _ = os.Stat(filepath.Join(dir, "ro.txt"))
	if string(data) != "2" || info.Mode().Perm() != 0o644 {
		t.Errorf("replaced file = %q %v; want 2 with mode 0644", data, info.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.agentport-tmp")); len(leftovers) != 0 {
		t.Errorf("temporary files left: %v", leftovers)
	}
}

func TestWriteFileErrors(t *testing.T) {
	dir := t.TempDir()
	root := openRoot(t, dir)
	must(t, os.WriteFile(filepath.Join(dir, "file"), nil, 0o644))
	if err := WriteFile(root, filepath.Join("file", "x"), nil, 0o644); err == nil {
		t.Error("writing below a regular file succeeded")
	}
	must(t, os.Symlink(t.TempDir(), filepath.Join(dir, "out")))
	if err := WriteFile(root, filepath.Join("out", "x"), nil, 0o644); err == nil {
		t.Error("writing through a link out of the root succeeded")
	}
	must(t, os.Mkdir(filepath.Join(dir, "taken"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "taken", "keep"), nil, 0o644))
	if err := WriteFile(root, "taken", []byte("x"), 0o644); err == nil {
		t.Error("replacing a non-empty directory succeeded")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	root := openRoot(t, dir)
	must(t, WriteFile(root, filepath.Join("s", "x", "a.txt"), nil, 0o644))
	must(t, WriteFile(root, filepath.Join("s", "keep.txt"), nil, 0o644))
	must(t, Remove(root, filepath.Join("s", "x", "a.txt")))
	if _, err := os.Stat(filepath.Join(dir, "s", "x")); !os.IsNotExist(err) {
		t.Error("an emptied directory was left")
	}
	if _, err := os.Stat(filepath.Join(dir, "s", "keep.txt")); err != nil {
		t.Error("a non-empty parent was removed")
	}
	must(t, Remove(root, "gone.txt"))
	if err := Remove(root, "s"); err == nil {
		t.Error("removing a non-empty directory as a file succeeded")
	}
}

func TestLinked(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "real", "sub"), 0o755))
	must(t, os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")))
	for name, want := range map[string]bool{
		filepath.Join("real", "sub", "f"): false,
		filepath.Join("link", "sub", "f"): true,
		"link":                            true,
		filepath.Join("absent", "f"):      false,
	} {
		if got, err := Linked(dir, name); err != nil || got != want {
			t.Errorf("Linked(%s) = %v, %v; want %v", name, got, err, want)
		}
	}
}

func TestWriteFileClearsAPlantedTempLink(t *testing.T) {
	dir := t.TempDir()
	root := openRoot(t, dir)
	victim := filepath.Join(dir, "victim.md")
	must(t, os.WriteFile(victim, []byte("keep\n"), 0o600))
	must(t, os.Symlink("victim.md", filepath.Join(dir, "out.md.agentport-tmp")))
	must(t, WriteFile(root, "out.md", []byte("new\n"), 0o644))
	data, _ := os.ReadFile(victim)
	info, _ := os.Stat(victim)
	if string(data) != "keep\n" || info.Mode().Perm() != 0o600 {
		t.Errorf("the file behind the temp link became %q %v", data, info.Mode().Perm())
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "out.md")); string(got) != "new\n" {
		t.Errorf("out.md = %q", got)
	}
}
