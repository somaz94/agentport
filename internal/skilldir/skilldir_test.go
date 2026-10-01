package skilldir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/ir"
)

func write(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestReadSkipsLeftovers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Entry), []byte("---\nname: s\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "scripts", "run.py"), 0o755)
	write(t, filepath.Join(dir, "scripts", "__pycache__", "run.cpython-312.pyc"), 0o644)
	write(t, filepath.Join(dir, "scripts", "stale.pyc"), 0o644)
	write(t, filepath.Join(dir, ".DS_Store"), 0o644)

	doc, res, notes, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name, _ := doc.Scalar("name"); name != "s" {
		t.Errorf("name = %q", name)
	}
	if len(res) != 1 || res[0].Path != "scripts/run.py" || res[0].Mode != 0o755 || len(notes) != 0 {
		t.Errorf("resources = %+v, notes = %+v; want only scripts/run.py with mode 0755 and no notes", res, notes)
	}
}

func TestReadSymlinks(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, Entry), []byte("---\nname: s\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(real, "scripts", "run.sh"), 0o755)
	write(t, filepath.Join(t.TempDir(), "unused"), 0o644)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "data.txt"), 0o644)
	for name, target := range map[string]string{
		"linked-dir":  outside,
		"linked-file": filepath.Join(outside, "data.txt"),
		"dangling":    filepath.Join(outside, "missing"),
	} {
		if err := os.Symlink(target, filepath.Join(real, name)); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	_, res, notes, err := Read(link)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, r := range res {
		paths[r.Path] = true
	}
	if !paths["scripts/run.sh"] || !paths["linked-file"] || len(res) != 2 {
		t.Errorf("a symlinked skill directory lost its files: %+v", res)
	}
	if len(notes) != 3 {
		t.Errorf("notes = %+v; want one each for the linked directory, the linked file and the dangling link", notes)
	}
}

func TestReadErrors(t *testing.T) {
	if _, _, _, err := Read(t.TempDir()); err == nil {
		t.Error("Read of a directory without SKILL.md succeeded")
	}
	if _, _, _, err := Read(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("Read of a missing directory succeeded")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Entry), []byte("---\nname: [x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Read(dir); err == nil {
		t.Error("Read of an unparsable SKILL.md succeeded")
	}
	unreadable := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unreadable, Entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Read(unreadable); err == nil {
		t.Error("Read succeeded when SKILL.md is a directory")
	}
}

func TestLayoutAndWrite(t *testing.T) {
	doc := &frontmatter.Document{Body: "body\n"}
	doc.SetString("name", "s")
	files, err := Layout(doc, []ir.Resource{{Path: "z.txt", Data: []byte("z")}, {Path: "a/b.sh", Mode: 0o755, Data: []byte("b")}})
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Path != Entry || files[1].Path != "a/b.sh" || files[2].Path != "z.txt" {
		t.Errorf("Layout order = %v", files)
	}
	dir := filepath.Join(t.TempDir(), "s")
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "b.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("a/b.sh: %v, %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "z.txt")); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("a resource without a mode should be written 0644: %v, %v", info, err)
	}
	for _, bad := range []string{"../escape", "/abs", "a/../../b", "./a", ""} {
		if err := Write(dir, []ir.Resource{{Path: bad}}); err == nil {
			t.Errorf("Write accepted %q", bad)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	write(t, file, 0o644)
	if err := Write(filepath.Join(file, "sub"), files); err == nil {
		t.Error("Write under a regular file succeeded")
	}
}

func TestWriteOverReadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	files := []ir.Resource{{Path: "gen.txt", Mode: 0o444, Data: []byte("v1")}}
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	files[0].Data = []byte("v2")
	if err := Write(dir, files); err != nil {
		t.Fatalf("rewriting a read-only file: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "gen.txt"))
	if err != nil || string(got) != "v2" {
		t.Errorf("gen.txt = %q, %v", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.agentport-tmp")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestWriteRefusesSymlinks(t *testing.T) {
	hub := t.TempDir()
	write(t, filepath.Join(hub, Entry), 0o644)
	link := filepath.Join(t.TempDir(), "linked-skill")
	if err := os.Symlink(hub, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []ir.Resource{{Path: Entry, Data: []byte("overwritten")}}); err == nil {
		t.Error("Write followed a symlinked target directory")
	}
	if got, _ := os.ReadFile(filepath.Join(hub, Entry)); string(got) == "overwritten" {
		t.Error("the linked original was overwritten")
	}

	dir := filepath.Join(t.TempDir(), "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hub, filepath.Join(dir, "scripts")); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, []ir.Resource{{Path: "scripts/x.sh", Data: []byte("x")}}); err == nil {
		t.Error("Write escaped through a symlinked subdirectory")
	}
	if _, err := os.Stat(filepath.Join(hub, "x.sh")); err == nil {
		t.Error("a file landed outside the target through a symlinked subdirectory")
	}
}

func TestFirstLine(t *testing.T) {
	cases := map[string]string{
		"\n\n## Heading text\nmore": "Heading text",
		"plain first\nsecond":       "plain first",
		"   \n\t\n":                 "",
	}
	for body, want := range cases {
		if got := FirstLine(body); got != want {
			t.Errorf("FirstLine(%q) = %q; want %q", body, got, want)
		}
	}
}
