package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/somaz94/agentport/internal/crosswalk"
	"github.com/somaz94/agentport/internal/harness"
)

func newMapCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "map [kind]",
		Short: "Show what each harness calls a customization and where it lives",
		Long: "Show the crosswalk between Claude Code, Codex and Antigravity.\n\n" +
			"Kinds: " + strings.Join(crosswalk.Kinds(), ", "),
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: crosswalk.Kinds(),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := ""
			if len(args) == 1 {
				kind = args[0]
			}
			rows, err := crosswalk.Filter(kind)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				enc.SetEscapeHTML(false)
				return enc.Encode(rows)
			}
			return writeMap(cmd.OutOrStdout(), rows)
		},
	}
}

func writeMap(w io.Writer, rows []crosswalk.Row) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, r := range rows {
		if i > 0 {
			fmt.Fprintln(tw)
		}
		fmt.Fprintf(tw, "%s (%s)\n", r.Concept, r.Kind)
		for _, h := range harness.All {
			fmt.Fprintf(tw, "  %s\t%s\n", h.Title(), r.Terms[h])
		}
	}
	return tw.Flush()
}
