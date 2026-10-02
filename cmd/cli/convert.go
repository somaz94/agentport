package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/somaz94/agentport/internal/convert"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/skilldir"
)

type convertOptions struct {
	to, from, kind, out                  string
	print, strict, force, modelInvocable bool
}

func newConvertCmd(opts *options) *cobra.Command {
	c := &convertOptions{}
	cmd := &cobra.Command{
		Use:   "convert <skill-dir | command.md | agent-file> --to <harness>",
		Short: "Convert one skill, command or agent to another harness (preview unless --out is given)",
		Long: "Convert a skill directory, a Claude Code command file or an agent file to another harness\n" +
			"and report, field by field, what was kept and what was lost. A command becomes a skill.\n" +
			"Nothing is written unless --out names a directory.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConvert(cmd, opts, c, args[0])
		},
	}
	f := cmd.Flags()
	f.StringVar(&c.to, "to", "", "target harness: "+strings.Join(harness.Names(), ", "))
	f.StringVar(&c.from, "from", "auto", "source harness, or auto to detect it from the path")
	f.StringVar(&c.kind, "kind", "auto", "skill, command or agent, or auto to decide from the path")
	f.StringVar(&c.out, "out", "", "write the converted item under this directory")
	f.BoolVar(&c.print, "print", false, "print the converted files")
	f.BoolVar(&c.strict, "strict", false, fmt.Sprintf("exit %d when anything was approximated, dropped or warned about", loss.ExitLossy))
	f.BoolVar(&c.force, "force", false, "with --out, replace an existing skill's files or agent file")
	f.BoolVar(&c.modelInvocable, "model-invocable", false, "let the model start a converted command, as Claude Code does (default: only when invoked by name)")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runConvert(cmd *cobra.Command, opts *options, c *convertOptions, path string) error {
	to, err := harness.Parse(c.to)
	if err != nil {
		return err
	}
	item, err := readItem(c.from, c.kind, path)
	if err != nil {
		return err
	}

	// A skill becomes a directory under --out, an agent a single file there.
	var dir, target string
	exists := false
	if c.out != "" {
		if err := paths.ValidName(item.Name); err != nil {
			return fmt.Errorf("cannot place %s under --out: %w", item.Kind, err)
		}
		dir = filepath.Join(c.out, item.Name)
		target = dir
		if item.Kind == ir.KindAgent {
			// A skill's own directory under a linked --out is real; an agent is written into --out itself.
			out := c.out
			if r, err := filepath.EvalSymlinks(out); err == nil {
				out = r
			}
			dir, target = out, filepath.Join(out, item.Name+convert.AgentExt(to))
		}
		switch _, err := os.Lstat(target); {
		case err == nil && !c.force:
			return fmt.Errorf("%s already exists; pass --force to replace it", target)
		case err == nil:
			exists = true
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		if exists && to == harness.Codex && item.Kind != ir.KindAgent {
			if item, err = convert.WithTargetSidecar(item, target); err != nil {
				return err
			}
		}
	}

	res, err := convert.Item(item, to, convert.Options{ModelInvocableCommands: c.modelInvocable, Agents: userAgents(item.Source.Harness, to)})
	if err != nil {
		return err
	}
	if err := writeConvert(cmd.OutOrStdout(), opts.output, res, c.print); err != nil {
		return err
	}
	if c.strict && res.Report.Lossy() {
		return &exitError{code: loss.ExitLossy, err: errors.New("the conversion is lossy (--strict); nothing was written")}
	}
	if target == "" {
		return nil
	}
	if err := skilldir.Write(dir, res.Files); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d file(s) to %s\n", len(res.Files), dir)
	if exists && item.Kind != ir.KindAgent {
		if extra := leftInPlace(target, res.Files); len(extra) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "left in place, not produced by this conversion: %s\n", strings.Join(extra, ", "))
		}
	}
	return nil
}

// userAgents lists the user-scope agents of both harnesses, so a body that refers to an agent the
// target lacks is reported. Nil when the source has none to refer to.
func userAgents(from, to harness.ID) *convert.AgentNames {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := func(h harness.ID) string {
		l, err := paths.For(h)
		if err != nil {
			return ""
		}
		rel, err := l.Dir(paths.ScopeUser, ir.KindAgent)
		if err != nil {
			return ""
		}
		return filepath.Join(home, rel)
	}
	src, dst := dir(from), dir(to)
	if src == "" || dst == "" {
		return nil
	}
	known := convert.ReadAgentNames(from, src)
	if len(known) == 0 {
		return nil
	}
	present := map[string]bool{}
	for _, name := range convert.ReadAgentNames(to, dst) {
		present[name] = true
	}
	return &convert.AgentNames{Known: known, Present: present}
}

// leftInPlace lists files under dir that the conversion did not write. --force never deletes, so
// a file from an earlier conversion stays until someone removes it.
func leftInPlace(dir string, written []ir.Resource) []string {
	want := map[string]bool{}
	for _, f := range written {
		want[f.Path] = true
	}
	var extra []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if rel = filepath.ToSlash(rel); !want[rel] {
			extra = append(extra, rel)
		}
		return nil
	})
	return extra
}

// readItem reads the skill, command or agent at path. Without --kind a directory or SKILL.md is a
// skill, a .toml file an agent, and a Markdown file whatever the directory holding it keeps:
// commands, agents, or, outside both, a command named after the file.
func readItem(from, kind, path string) (*ir.Item, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "auto", "skill", "command", "agent":
	default:
		return nil, fmt.Errorf("unknown --kind %q: skill, command, agent or auto", kind)
	}
	if kind == "skill" && !info.IsDir() && filepath.Base(path) != skilldir.Entry {
		return nil, fmt.Errorf("--kind skill takes a skill directory or its %s, not %s", skilldir.Entry, path)
	}
	if kind == "skill" || kind == "auto" && (info.IsDir() || filepath.Base(path) == skilldir.Entry) {
		dir := path
		if !info.IsDir() {
			dir = filepath.Dir(path)
		}
		h, err := sourceHarness(from, dir)
		if err != nil {
			return nil, err
		}
		return convert.ReadSkill(h, dir)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a %s file", path, kind)
	}
	home, _ := os.UserHomeDir()
	switch {
	case kind == "agent" || kind == "auto" && filepath.Ext(path) == ".toml":
		return readAgent(from, path, home)
	case kind == "command":
		return readCommand(from, path)
	case filepath.Ext(path) != ".md":
		return nil, fmt.Errorf("%s is neither a skill directory nor a command or agent file", path)
	}
	if _, _, err := convert.DetectCommand(path, home); err != nil {
		if _, _, _, err := convert.DetectAgent(path, home); err == nil {
			return readAgent(from, path, home)
		}
		if from == "auto" {
			return nil, fmt.Errorf("%w; for an agent, pass --kind agent and --from", err)
		}
	}
	return readCommand(from, path)
}

// readAgent reads an agent file, taking its harness from --from or from the agents directory
// holding it, and refuses one that shares its name with another agent there.
func readAgent(from, path, home string) (*ir.Item, error) {
	detected, dir, rel, detectErr := convert.DetectAgent(path, home)
	h := detected
	if from != "auto" {
		var err error
		if h, err = harness.Parse(from); err != nil {
			return nil, err
		}
	} else if detectErr != nil {
		return nil, detectErr
	}
	item, err := convert.ReadAgent(h, path)
	if err != nil || detectErr != nil || h != detected {
		return item, err
	}
	dups, err := convert.AgentDuplicates(h, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: check names: %w", path, err)
	}
	if why := dups[rel]; why != "" {
		return nil, fmt.Errorf("%s is not converted: %s", path, why)
	}
	return item, nil
}

// readCommand reads a command file, refusing one that convert.CommandBlocked blocks. A command
// found outside every commands directory is named after its file and has nothing to collide with.
func readCommand(from, path string) (*ir.Item, error) {
	home, _ := os.UserHomeDir()
	dirs, rel, detectErr := convert.DetectCommand(path, home)
	h := dirs.Harness
	if from != "auto" {
		var err error
		if h, err = harness.Parse(from); err != nil {
			return nil, err
		}
	} else if detectErr != nil {
		return nil, detectErr
	}
	item, err := convert.ReadCommand(h, path, rel)
	if err != nil {
		return nil, err
	}
	if detectErr != nil {
		return item, nil
	}
	why, err := convert.CommandBlocked(dirs, rel)
	if err != nil {
		return nil, fmt.Errorf("%s: check name collisions: %w", path, err)
	}
	if why != "" {
		return nil, fmt.Errorf("%s is not converted: %s", path, why)
	}
	return item, nil
}

func sourceHarness(flag, dir string) (harness.ID, error) {
	if flag != "auto" {
		return harness.Parse(flag)
	}
	home, _ := os.UserHomeDir()
	return convert.DetectSkill(dir, home)
}

type fileJSON struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Content string `json:"content,omitempty"`
}

func writeConvert(w io.Writer, format string, res convert.Result, withContent bool) error {
	if format == outputJSON {
		files := make([]fileJSON, len(res.Files))
		for i, f := range res.Files {
			files[i] = fileJSON{Path: f.Path, Mode: fmt.Sprintf("%04o", f.Mode)}
			if withContent {
				files[i].Content = string(f.Data)
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(struct {
			Report loss.Report `json:"report"`
			Files  []fileJSON  `json:"files"`
		}{res.Report, files})
	}
	if err := loss.WriteText(w, []loss.Report{res.Report}); err != nil {
		return err
	}
	fmt.Fprintln(w, "\nfiles:")
	for _, f := range res.Files {
		fmt.Fprintf(w, "  %s (%04o)\n", f.Path, f.Mode)
	}
	if withContent {
		for _, f := range res.Files {
			fmt.Fprintf(w, "\n==> %s <==\n%s", f.Path, f.Data)
		}
	}
	return nil
}
