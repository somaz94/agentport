// Package config reads agentport's settings file. Every key is optional; a missing file means the
// defaults.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/paths"
)

// Config is the validated settings.
type Config struct {
	// Hub is the harness whose customizations are the source.
	Hub harness.ID
	// Targets are the harnesses sync writes to when no --to is given; none means every harness
	// that is set up.
	Targets []harness.ID
	// Pairs are the suffixes of translation mirrors: with `-ko`, `skills-ko/` beside the hub's
	// `skills/` is converted into `skills-ko/` beside each target's.
	Pairs []string
	// Skip are patterns, relative to the hub directory, of items sync leaves out.
	Skip []string
	// ModelInvocableCommands lets the model start converted commands, as Claude Code does.
	ModelInvocableCommands bool
	// Path is the file the settings came from, empty when none was read.
	Path string
}

type file struct {
	Hub                    string   `yaml:"hub"`
	Targets                []string `yaml:"targets"`
	Pairs                  []string `yaml:"pairs"`
	Skip                   []string `yaml:"skip"`
	ModelInvocableCommands bool     `yaml:"modelInvocableCommands"`
}

// Default returns the settings used when no file exists.
func Default() *Config {
	return &Config{Hub: harness.Claude}
}

// DefaultPath is `$XDG_CONFIG_HOME/agentport/config.yaml`, with `~/.config` when the variable is
// unset, on every platform.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "agentport", "config.yaml"), nil
}

// Load reads the settings at p, or at DefaultPath when p is empty. A missing file is the defaults
// unless p named it.
func Load(p string) (*Config, error) {
	explicit := p != ""
	if !explicit {
		var err error
		if p, err = DefaultPath(); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	c.Path = p
	return c, nil
}

func parse(data []byte) (*Config, error) {
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	c := Default()
	if f.Hub != "" {
		h, err := harness.Parse(f.Hub)
		if err != nil {
			return nil, fmt.Errorf("hub: %w", err)
		}
		c.Hub = h
	}
	if c.Hub != harness.Claude {
		return nil, fmt.Errorf("hub: only %s can be the hub", harness.Claude)
	}
	if f.Targets != nil {
		targets, err := ParseTargets(f.Targets, c.Hub)
		if err != nil {
			return nil, fmt.Errorf("targets: %w", err)
		}
		c.Targets = targets
	}
	for _, s := range f.Pairs {
		if s == "" || paths.ValidName("skills"+s) != nil || slices.Contains(c.Pairs, s) {
			return nil, fmt.Errorf("pairs: %q is empty, repeated or not a directory-name suffix", s)
		}
		c.Pairs = append(c.Pairs, s)
	}
	for _, p := range f.Skip {
		if _, err := path.Match(p, ""); err != nil || p == "" || path.IsAbs(p) || strings.Contains(p, `\`) {
			return nil, fmt.Errorf("skip: %q is not a slash-separated pattern relative to the hub directory", p)
		}
		c.Skip = append(c.Skip, p)
	}
	c.ModelInvocableCommands = f.ModelInvocableCommands
	return c, nil
}

// ParseTargets resolves harness names into targets: known, not repeated, and not the hub.
func ParseTargets(names []string, hub harness.ID) ([]harness.ID, error) {
	if len(names) == 0 {
		return nil, errors.New("no target harness")
	}
	var out []harness.ID
	for _, name := range names {
		h, err := harness.Parse(name)
		if err != nil {
			return nil, err
		}
		if h == hub {
			return nil, fmt.Errorf("%s is the hub, not a target", h)
		}
		if slices.Contains(out, h) {
			return nil, fmt.Errorf("%s is listed twice", h)
		}
		out = append(out, h)
	}
	return out, nil
}

// Skipped reports whether rel, a slash-separated path relative to the hub directory such as
// `commands/a/b.md`, matches a skip pattern, or sits below a directory that does.
func (c *Config) Skipped(rel string) bool {
	for _, p := range c.Skip {
		for r := rel; r != "." && r != "/"; r = path.Dir(r) {
			if ok, _ := path.Match(p, r); ok {
				return true
			}
		}
	}
	return false
}
