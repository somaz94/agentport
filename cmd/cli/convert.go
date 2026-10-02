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
	to, from, out                        string
	print, strict, force, modelInvocable bool
}

func newConvertCmd(opts *options) *cobra.Command {
	c := &convertOptions{}
	cmd := &cobra.Command{
		Use:   "convert <skill-dir | command.md> --to <harness>",
		Short: "Convert one skill or command to another harness (preview unless --out is given)",
		Long: "Convert a skill directory or a Claude Code command file to another harness and report,\n" +
			"field by field, what was kept and what was lost. A command becomes a skill. Nothing is\n" +
			"written unless --out names a directory.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConvert(cmd, opts, c, args[0])
		},
	}
	f := cmd.Flags()
	f.StringVar(&c.to, "to", "", "target harness: "+strings.Join(harness.Names(), ", "))
	f.StringVar(&c.from, "from", "auto", "source harness, or auto to detect it from the path")
	f.StringVar(&c.out, "out", "", "write the converted skill under this directory")
	f.BoolVar(&c.print, "print", false, "print the converted files")
	f.BoolVar(&c.strict, "strict", false, fmt.Sprintf("exit %d when anything was approximated, dropped or warned about", loss.ExitLossy))
	f.BoolVar(&c.force, "force", false, "with --out, replace files in an existing skill directory")
	f.BoolVar(&c.modelInvocable, "model-invocable", false, "let the model start a converted command, as Claude Code does (default: only when invoked by name)")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runConvert(cmd *cobra.Command, opts *options, c *convertOptions, path string) error {
	to, err := harness.Parse(c.to)
	if err != nil {
		return err
	}
	item, err := readItem(c.from, path)
	if err != nil {
		return err
	}

	var target string
	exists := false
	if c.out != "" {
		if err := paths.ValidName(item.Name); err != nil {
			return fmt.Errorf("cannot place the skill under --out: %w", err)
		}
		target = filepath.Join(c.out, item.Name)
		switch _, err := os.Lstat(target); {
		case err == nil && !c.force:
			return fmt.Errorf("%s already exists; pass --force to replace its files", target)
		case err == nil:
			exists = true
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		if exists && to == harness.Codex {
			if item, err = convert.WithTargetSidecar(item, target); err != nil {
				return err
			}
		}
	}

	res, err := convert.Skill(item, to, convert.Options{ModelInvocableCommands: c.modelInvocable})
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
	if err := skilldir.Write(target, res.Files); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d file(s) to %s\n", len(res.Files), target)
	if exists {
		if extra := leftInPlace(target, res.Files); len(extra) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "left in place, not produced by this conversion: %s\n", strings.Join(extra, ", "))
		}
	}
	return nil
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

// readItem reads the skill at path, a skill directory or its SKILL.md, or the command file at path.
func readItem(from, path string) (*ir.Item, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || filepath.Base(path) == skilldir.Entry {
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
	if filepath.Ext(path) != ".md" {
		return nil, fmt.Errorf("%s is neither a skill directory nor a command file", path)
	}
	return readCommand(from, path)
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
