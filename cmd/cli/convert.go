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
	to, from, out        string
	print, strict, force bool
}

func newConvertCmd(opts *options) *cobra.Command {
	c := &convertOptions{}
	cmd := &cobra.Command{
		Use:   "convert <skill-dir> --to <harness>",
		Short: "Convert one skill to another harness (preview unless --out is given)",
		Long: "Convert one skill directory to another harness and report, field by field, what was\n" +
			"kept and what was lost. Nothing is written unless --out names a directory.",
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
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runConvert(cmd *cobra.Command, opts *options, c *convertOptions, path string) error {
	dir := path
	if filepath.Base(dir) == skilldir.Entry {
		dir = filepath.Dir(dir)
	}
	to, err := harness.Parse(c.to)
	if err != nil {
		return err
	}
	from, err := sourceHarness(c.from, dir)
	if err != nil {
		return err
	}
	item, err := convert.ReadSkill(from, dir)
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

	res, err := convert.Skill(item, to)
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
