// Package doctor checks the setup agentport works in: which harness versions are installed against
// the versions its facts were verified on, where each harness keeps its items, locations a
// harness deprecated or removed, and the health of agentport's own manifests.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/somaz94/agentport/internal/config"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/paths"
)

// Status grades a finding.
type Status string

// Statuses, from fine to broken.
const (
	OK    Status = "ok"
	Info  Status = "info"
	Warn  Status = "warn"
	Error Status = "error"
)

// Finding is the outcome of one check.
type Finding struct {
	Check   string `json:"check"`
	Status  Status `json:"status"`
	Message string `json:"message"`
}

// Options say where to look.
type Options struct {
	Scope paths.Scope
	// Root is the scope's root: the home directory or the repository.
	Root string
	// Config is the settings file named on the command line, empty for the default.
	Config string
}

// ErrNotInstalled means a harness's version could not be found because it is not installed.
var ErrNotInstalled = errors.New("not installed")

// Versions finds each harness's installed version and where it was read from. Tests replace it.
var Versions = map[harness.ID]func(ctx context.Context) (version, where string, err error){
	harness.Claude:      commandVersion("claude"),
	harness.Codex:       commandVersion("codex"),
	harness.Antigravity: antigravityVersion,
}

var semver = regexp.MustCompile(`\d+\.\d+\.\d+`)

// Run performs every check; ctx bounds the version probes, which run installed programs.
func Run(ctx context.Context, opts Options) []Finding {
	var out []Finding
	add := func(check string, s Status, format string, args ...any) {
		out = append(out, Finding{check, s, fmt.Sprintf(format, args...)})
	}
	cfg, err := config.Load(opts.Config)
	switch {
	case err != nil:
		add("config", Error, "%v", err)
		cfg = config.Default()
	case cfg.Path == "":
		add("config", OK, "no settings file; using the defaults")
	default:
		add("config", OK, "%s", cfg.Path)
	}
	for _, h := range harness.All {
		l, err := paths.For(h)
		if err != nil {
			add(string(h), Error, "%v", err)
			continue
		}
		version(ctx, h, l, add)
		locations(opts, h, l, add)
		legacy(opts, h, l, add)
		if opts.Scope == paths.ScopeUser && h != cfg.Hub {
			manifests(opts, h, l, add)
		}
	}
	if opts.Scope == paths.ScopeUser {
		instructions(opts, add)
	}
	return out
}

type adder func(check string, s Status, format string, args ...any)

func version(ctx context.Context, h harness.ID, l paths.Layout, add adder) {
	check := string(h) + " version"
	got, where, err := Versions[h](ctx)
	switch {
	case errors.Is(err, ErrNotInstalled):
		add(check, Info, "%s is not installed", h.Title())
	case err != nil:
		add(check, Warn, "could not read the %s version: %v", h.Title(), err)
	case got == semver.FindString(l.Version):
		add(check, OK, "%s %s (%s), the version agentport's facts were verified on", h.Title(), got, where)
	default:
		add(check, Warn, "%s %s (%s); agentport's facts were verified on %s, and this version may load files differently", h.Title(), got, where, l.Version)
	}
}

func locations(opts Options, h harness.ID, l paths.Layout, add adder) {
	check := string(h) + " locations"
	root, err := l.Root(opts.Scope)
	if err != nil {
		add(check, Error, "%v", err)
		return
	}
	if !isDir(filepath.Join(opts.Root, root)) {
		add(check, Info, "%s is not set up: %s does not exist", h.Title(), display(opts, root))
		return
	}
	for _, kind := range []ir.Kind{ir.KindSkill, ir.KindCommand, ir.KindAgent} {
		dir, err := l.Dir(opts.Scope, kind)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(opts.Root, dir))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			add(check, OK, "%s: %s (none yet)", kind, display(opts, dir))
		case err != nil:
			add(check, Warn, "%s: %v", kind, err)
		default:
			add(check, OK, "%s: %s (%d entries)", kind, display(opts, dir), len(entries))
		}
	}
}

func legacy(opts Options, h harness.ID, l paths.Layout, add adder) {
	for _, x := range l.Legacy(opts.Scope) {
		p := filepath.Join(opts.Root, x.Path)
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if entries, err := os.ReadDir(p); err == nil && len(entries) == 0 {
				continue
			}
		}
		add(string(h)+" legacy", Warn, "%s exists; %s", display(opts, x.Path), x.Note)
	}
}

func manifests(opts Options, h harness.ID, l paths.Layout, add adder) {
	check := string(h) + " manifest"
	root, err := l.Root(opts.Scope)
	if err != nil {
		return
	}
	dir := filepath.Join(opts.Root, root)
	if !isDir(dir) {
		return
	}
	if _, err := os.Stat(manifest.Path(dir)); errors.Is(err, fs.ErrNotExist) {
		add(check, Info, "none yet: nothing has been synced to %s", h.Title())
		return
	}
	m, err := manifest.Load(dir)
	if err != nil {
		add(check, Error, "%v", err)
		return
	}
	missing := 0
	for key := range m.Entries {
		if _, err := os.Lstat(filepath.Join(opts.Root, filepath.FromSlash(key))); err != nil {
			missing++
		}
	}
	rel, _ := filepath.Rel(opts.Root, manifest.Path(dir))
	if missing > 0 {
		add(check, Warn, "%s: %d files recorded, %d of them missing; sync writes them again or forgets them", display(opts, rel), len(m.Entries), missing)
		return
	}
	add(check, OK, "%s: %d files recorded", display(opts, rel), len(m.Entries))
}

// instructions flags an Antigravity configuration directory holding both instruction files, which
// Antigravity loads as two separate global entries (docs/spec/antigravity.md).
func instructions(opts Options, add adder) {
	l, err := paths.For(harness.Antigravity)
	if err != nil {
		return
	}
	root, err := l.Root(opts.Scope)
	if err != nil {
		return
	}
	both := true
	for _, name := range []string{"AGENTS.md", "GEMINI.md"} {
		if _, err := os.Stat(filepath.Join(opts.Root, root, name)); err != nil {
			both = false
		}
	}
	if both {
		add("antigravity instructions", Warn, "%s holds both AGENTS.md and GEMINI.md; Antigravity loads both, so keep one", display(opts, root))
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// display shows a path below the root: `~/...` below the user's own home directory, and relative
// to the repository in the project scope.
func display(opts Options, rel string) string {
	if opts.Scope == paths.ScopeProject {
		return rel
	}
	if home, err := os.UserHomeDir(); err == nil && home == opts.Root {
		return filepath.Join("~", rel)
	}
	return filepath.Join(opts.Root, rel)
}

// commandVersion runs `name --version` from PATH and takes the first version number it prints.
func commandVersion(name string) func(context.Context) (string, string, error) {
	return func(ctx context.Context) (string, string, error) {
		bin, err := exec.LookPath(name)
		if err != nil {
			return "", "", ErrNotInstalled
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--version")
		// A launcher script can leave a child holding stdout after it is killed.
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.Output()
		if err != nil {
			return "", "", fmt.Errorf("%s --version: %w", bin, err)
		}
		if v := semver.FindString(string(out)); v != "" {
			return v, bin, nil
		}
		return "", "", fmt.Errorf("%s --version printed no version", bin)
	}
}

// antigravityApp is where the desktop app installs on macOS, the only platform its version is read on.
var antigravityApp = "/Applications/Antigravity.app"

var bundleVersion = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)

func antigravityVersion(context.Context) (string, string, error) {
	if runtime.GOOS != "darwin" {
		return "", "", errors.New("the version is read from the macOS app bundle only")
	}
	plist := filepath.Join(antigravityApp, "Contents", "Info.plist")
	data, err := os.ReadFile(plist)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", ErrNotInstalled
	}
	if err != nil {
		return "", "", err
	}
	if m := bundleVersion.FindSubmatch(data); m != nil {
		return string(m[1]), antigravityApp, nil
	}
	return "", "", fmt.Errorf("no version in %s", plist)
}
