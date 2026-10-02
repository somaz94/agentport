// Package manifest records which target files agentport wrote, so a sync updates or deletes only
// its own output and never a file someone else owns.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Location of the manifest under a target root.
const (
	Dir  = ".agentport"
	File = "manifest.json"
)

// SchemaVersion is bumped when the on-disk format changes incompatibly.
const SchemaVersion = 1

// Entry is one file agentport wrote. Source is the slash-separated path, relative to the same root
// as the entry's key, of the hub item that produced it.
type Entry struct {
	Source     string `json:"source"`
	SourceHash string `json:"sourceHash"`
	OutputHash string `json:"outputHash"`
	// Mode is the permission bits written, in octal.
	Mode      string `json:"mode"`
	Generator string `json:"generator"`
}

// Fingerprint is what a later run compares against the file on disk: content and mode.
func (e Entry) Fingerprint() string {
	return e.OutputHash + " " + e.Mode
}

// Manifest maps a slash-separated path relative to the scope's root (the home directory) to the
// entry that produced it.
type Manifest struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// New returns an empty manifest.
func New() *Manifest {
	return &Manifest{Version: SchemaVersion, Entries: map[string]Entry{}}
}

// Path returns the manifest path for root, a target harness's configuration directory.
func Path(root string) string {
	return filepath.Join(root, Dir, File)
}

// Load reads the manifest under root. A missing manifest is an empty one: nothing is managed yet.
func Load(root string) (*Manifest, error) {
	data, err := os.ReadFile(Path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	m := New()
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(root), err)
	}
	if m.Version != SchemaVersion {
		return nil, fmt.Errorf("%s has schema version %d, this build reads %d", Path(root), m.Version, SchemaVersion)
	}
	if m.Entries == nil {
		m.Entries = map[string]Entry{}
	}
	// A project-scope manifest arrives with a cloned repository, so an entry must not be able to
	// steer an orphan deletion outside the root.
	for p := range m.Entries {
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return nil, fmt.Errorf("%s: entry %q leaves the root its paths are relative to", Path(root), p)
		}
	}
	return m, nil
}

// Save writes the manifest under root atomically. Keys are sorted, so an unchanged manifest
// produces identical bytes.
func (m *Manifest) Save(root string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, File+".*")
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	// Without Sync a crash can leave an empty manifest behind the rename, which Load rejects.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmp.Name(), Path(root)); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// Hash returns the content hash recorded in a manifest.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Mode renders permission bits as an Entry records them.
func Mode(m fs.FileMode) string {
	return fmt.Sprintf("%04o", m.Perm())
}

// Fingerprint identifies a file's content and mode, in the form Entry.Fingerprint returns.
func Fingerprint(data []byte, mode fs.FileMode) string {
	return Entry{OutputHash: Hash(data), Mode: Mode(mode)}.Fingerprint()
}

// State is what a sync does with one target path.
type State string

const (
	// StateNew writes a file that does not exist yet.
	StateNew State = "new"
	// StateUpdate rewrites a managed file whose source changed.
	StateUpdate State = "update"
	// StateUnchanged leaves the file alone. The caller still records its entry: the path may be
	// unmanaged, or recorded under an older hash or mode.
	StateUnchanged State = "unchanged"
	// StateDrift leaves a managed file someone edited by hand; adopt or --force resolves it.
	StateDrift State = "drift"
	// StateOrphan deletes a managed, unedited file whose source is gone.
	StateOrphan State = "orphan"
	// StateConflict leaves an unmanaged file that occupies the path agentport would write.
	StateConflict State = "conflict"
	// StateUnmanaged is any other file under a target root; agentport never touches it.
	StateUnmanaged State = "unmanaged"
)

// Observation is everything Classify needs to know about one target path. Fingerprints come from
// Fingerprint and Entry.Fingerprint, so a changed mode counts as a change.
type Observation struct {
	// Recorded is the manifest entry for the path, nil when unmanaged.
	Recorded *Entry
	// SourceExists is false when the source no longer produces the path.
	SourceExists bool
	// Output is the fingerprint of what the source converts to now; empty when the source is gone.
	Output string
	// TargetExists and Target describe the file on disk.
	TargetExists bool
	Target       string
}

// Classify decides what a sync does with one target path. It never chooses to overwrite or delete
// a file whose content or mode differs from what agentport last wrote there.
func Classify(o Observation) State {
	if o.Recorded == nil {
		switch {
		case !o.SourceExists:
			return StateUnmanaged
		case !o.TargetExists:
			return StateNew
		case o.Target == o.Output:
			return StateUnchanged
		default:
			return StateConflict
		}
	}
	if o.TargetExists && o.Target != o.Recorded.Fingerprint() {
		if o.SourceExists && o.Target == o.Output {
			return StateUnchanged
		}
		return StateDrift
	}
	switch {
	case !o.SourceExists:
		return StateOrphan
	case !o.TargetExists:
		return StateNew
	case o.Output == o.Target:
		return StateUnchanged
	default:
		return StateUpdate
	}
}
