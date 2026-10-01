package manifest

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, root, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(root, Dir), 0o755))
	must(t, os.WriteFile(Path(root), []byte(body), 0o644))
}

func TestLoadMissingIsEmpty(t *testing.T) {
	m, err := Load(t.TempDir())
	if err != nil || m.Version != SchemaVersion || len(m.Entries) != 0 {
		t.Errorf("Load(empty dir) = %+v, %v", m, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	m := New()
	m.Entries["skills/b/SKILL.md"] = Entry{Source: "skills/b/SKILL.md", SourceHash: Hash([]byte("s")), OutputHash: Hash([]byte("o")), Generator: "agentport dev"}
	m.Entries["agents/a.md"] = Entry{Source: "agents/a.md", SourceHash: "x", OutputHash: "y", Generator: "agentport dev"}
	must(t, m.Save(root))
	first, err := os.ReadFile(Path(root))
	must(t, err)
	must(t, m.Save(root))
	second, err := os.ReadFile(Path(root))
	must(t, err)
	if !bytes.Equal(first, second) {
		t.Error("saving an unchanged manifest changed its bytes")
	}
	if strings.Index(string(first), "agents/a.md") > strings.Index(string(first), "skills/b/SKILL.md") {
		t.Error("entries are not sorted by path")
	}
	got, err := Load(root)
	if err != nil || len(got.Entries) != 2 || got.Entries["agents/a.md"].OutputHash != "y" {
		t.Errorf("Load after Save = %+v, %v", got, err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, Dir, File+".*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestLoadRejects(t *testing.T) {
	for name, body := range map[string]string{
		"bad json":       "{",
		"wrong version":  `{"version": 99, "entries": {}}`,
		"escaping entry": `{"version": 1, "entries": {"../outside.md": {}}}`,
		"absolute entry": `{"version": 1, "entries": {"/etc/passwd": {}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeManifest(t, root, body)
			if _, err := Load(root); err == nil {
				t.Error("Load succeeded")
			}
		})
	}

	root := t.TempDir()
	writeManifest(t, root, `{"version": 1, "entries": null}`)
	if m, err := Load(root); err != nil || m.Entries == nil {
		t.Errorf("null entries: %+v, %v; want an empty map", m, err)
	}

	unreadable := t.TempDir()
	must(t, os.MkdirAll(Path(unreadable), 0o755))
	if _, err := Load(unreadable); err == nil {
		t.Error("Load succeeded when the manifest path is a directory")
	}
}

func TestSaveErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	must(t, os.WriteFile(file, nil, 0o644))
	if err := New().Save(file); err == nil {
		t.Error("Save under a regular file succeeded")
	}
	root := t.TempDir()
	must(t, os.MkdirAll(Path(root), 0o755))
	must(t, os.WriteFile(filepath.Join(Path(root), "keep"), nil, 0o644))
	if err := New().Save(root); err == nil {
		t.Error("Save over a non-empty directory succeeded")
	}
}

func TestSavedFileMode(t *testing.T) {
	root := t.TempDir()
	must(t, New().Save(root))
	info, err := os.Stat(Path(root))
	must(t, err)
	if info.Mode().Perm() != 0o644 {
		t.Errorf("manifest mode = %v; want 0644", info.Mode().Perm())
	}
}

func TestHash(t *testing.T) {
	h := Hash([]byte("abc"))
	if h != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("Hash(abc) = %s", h)
	}
}

func TestClassify(t *testing.T) {
	rec := &Entry{OutputHash: "old"}
	cases := []struct {
		name string
		o    Observation
		want State
	}{
		{"unmanaged, no source", Observation{TargetExists: true, TargetHash: "x"}, StateUnmanaged},
		{"new", Observation{SourceExists: true, OutputHash: "new"}, StateNew},
		{"unmanaged identical", Observation{SourceExists: true, OutputHash: "new", TargetExists: true, TargetHash: "new"}, StateUnchanged},
		{"conflict", Observation{SourceExists: true, OutputHash: "new", TargetExists: true, TargetHash: "theirs"}, StateConflict},
		{"update", Observation{Recorded: rec, SourceExists: true, OutputHash: "new", TargetExists: true, TargetHash: "old"}, StateUpdate},
		{"unchanged", Observation{Recorded: rec, SourceExists: true, OutputHash: "old", TargetExists: true, TargetHash: "old"}, StateUnchanged},
		{"drift", Observation{Recorded: rec, SourceExists: true, OutputHash: "new", TargetExists: true, TargetHash: "hand"}, StateDrift},
		{"hand edit equals new output", Observation{Recorded: rec, SourceExists: true, OutputHash: "new", TargetExists: true, TargetHash: "new"}, StateUnchanged},
		{"recreate deleted target", Observation{Recorded: rec, SourceExists: true, OutputHash: "new"}, StateNew},
		{"orphan", Observation{Recorded: rec, TargetExists: true, TargetHash: "old"}, StateOrphan},
		{"orphan already gone", Observation{Recorded: rec}, StateOrphan},
		{"edited orphan is drift", Observation{Recorded: rec, TargetExists: true, TargetHash: "hand"}, StateDrift},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.o); got != c.want {
				t.Errorf("Classify = %s; want %s", got, c.want)
			}
		})
	}
}
