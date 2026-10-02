package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/somaz94/agentport/internal/doctor"
	"github.com/somaz94/agentport/internal/paths"
)

type doctorOptions struct {
	scope, root, config string
}

func newDoctorCmd(opts *options) *cobra.Command {
	d := &doctorOptions{}
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check installed harness versions, locations, deprecated paths and agentport's manifests",
		Long: "Compare each installed harness with the version agentport's facts were verified on, show\n" +
			"where each keeps its skills, commands and agents, warn about locations a harness deprecated\n" +
			"or removed, and check agentport's manifests and settings file. Exits 1 when a check fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := paths.ParseScope(d.scope)
			if err != nil {
				return err
			}
			root, err := scopeRoot(scope, d.root)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			findings := doctor.Run(ctx, doctor.Options{Scope: scope, Root: root, Config: d.config})
			if err := writeFindings(cmd, opts.output, findings); err != nil {
				return err
			}
			for _, f := range findings {
				if f.Status == doctor.Error {
					return &exitError{code: 1, err: errors.New("a check failed")}
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&d.scope, "scope", string(paths.ScopeUser), "user or project")
	f.StringVar(&d.root, "root", "", "scope root: the home directory for user scope, the repository for project scope")
	f.StringVar(&d.config, "config", "", "settings file (default: $XDG_CONFIG_HOME/agentport/config.yaml)")
	return cmd
}

func writeFindings(cmd *cobra.Command, format string, findings []doctor.Finding) error {
	w := cmd.OutOrStdout()
	if format == outputJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(findings)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range findings {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Status, f.Check, f.Message)
	}
	return tw.Flush()
}
