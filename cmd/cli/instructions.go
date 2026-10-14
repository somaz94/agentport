package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/somaz94/agentport/internal/instructions"
)

type instructionsOptions struct {
	hubOptions
	mark bool
}

func newInstructionsCmd(opts *options) *cobra.Command {
	o := &instructionsOptions{}
	cmd := &cobra.Command{
		Use:   "instructions",
		Short: "Show which hub instruction sections changed since each target's AGENTS.md was last reviewed",
		Long: "Instructions are not generated: each target keeps a hand-condensed AGENTS.md. List, per\n" +
			"target, the `## ` sections of the hub's CLAUDE.md added, changed or removed since that file\n" +
			"was last marked reviewed. After updating it, --mark records the current sections as\n" +
			"reviewed; it writes only .agentport/instructions.json in the target, never AGENTS.md.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstructions(cmd, opts, o)
		},
	}
	o.register(cmd, true)
	cmd.Flags().BoolVar(&o.mark, "mark", false, "record the hub's current sections as reviewed for each target that has an instruction file")
	return cmd
}

type instructionsJSON struct {
	Target   string             `json:"target"`
	File     string             `json:"file"`
	Hub      string             `json:"hub"`
	State    instructions.State `json:"state"`
	Sections int                `json:"sections"`
	Marked   bool               `json:"marked,omitempty"`
	instructions.Diff
}

func runInstructions(cmd *cobra.Command, opts *options, o *instructionsOptions) error {
	ro, err := o.resolve()
	if err != nil {
		return err
	}
	pairs, err := instructions.Pairs(ro.Config.Hub, ro.Targets)
	if err != nil {
		return err
	}
	var rows []instructionsJSON
	marked := 0
	for _, p := range pairs {
		rep, err := instructions.Check(ro.Root, p)
		if err != nil {
			return err
		}
		row := instructionsJSON{
			Target: string(p.Target), File: homeDisplay(ro.Root, p.File), Hub: homeDisplay(ro.Root, p.Hub),
			State: rep.State, Sections: rep.Sections, Diff: rep.Diff,
		}
		if o.mark && rep.State != instructions.NoFile && rep.State != instructions.NoHub {
			n, err := instructions.Mark(ro.Root, p)
			if err != nil {
				return err
			}
			row.State, row.Sections, row.Diff, row.Marked = instructions.Current, n, instructions.Diff{}, true
			marked++
		}
		rows = append(rows, row)
	}
	if err := writeInstructions(cmd, opts.output, rows); err != nil {
		return err
	}
	if o.mark && marked == 0 {
		return errors.New("nothing to mark: no target has an instruction file to review against the hub's")
	}
	return nil
}

func writeInstructions(cmd *cobra.Command, format string, rows []instructionsJSON) error {
	w := cmd.OutOrStdout()
	if format == outputJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if rows == nil {
			rows = []instructionsJSON{}
		}
		return enc.Encode(rows)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Target, r.File, r.State, instructionsDetail(r))
	}
	return tw.Flush()
}

func instructionsDetail(r instructionsJSON) string {
	switch r.State {
	case instructions.NoFile:
		return "no instruction file; nothing to review"
	case instructions.NoHub:
		return fmt.Sprintf("%s does not exist; nothing to compare against", r.Hub)
	case instructions.Unrecorded:
		return fmt.Sprintf("never marked reviewed against %s; run with --mark once it covers every section", r.Hub)
	case instructions.Changed:
		return r.Diff.String()
	}
	if r.Marked {
		return fmt.Sprintf("marked reviewed against %d sections of %s", r.Sections, r.Hub)
	}
	return fmt.Sprintf("reviewed against the current %s (%d sections)", r.Hub, r.Sections)
}

// homeDisplay shows rel, a path below root, as `~/...` when root is the user's own home directory.
func homeDisplay(root, rel string) string {
	if home, err := os.UserHomeDir(); err == nil && home == root {
		return filepath.Join("~", rel)
	}
	return filepath.Join(root, rel)
}
