package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// Version, GitCommit and BuildDate are overridden with -ldflags -X at build time.
var (
	Version   = "dev"
	GitCommit = "none"
	BuildDate = "unknown"
)

func newVersionCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.output == outputJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
					"version": Version, "commit": GitCommit, "buildDate": BuildDate,
				})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "agentport %s (commit: %s, built: %s)\n", Version, GitCommit, BuildDate)
			return err
		},
	}
}
