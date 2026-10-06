package cli

import (
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version, GitCommit and BuildDate are set with -ldflags -X, or by fromBuildInfo when -X left
// Version at "dev".
var (
	Version   = "dev"
	GitCommit = "none"
	BuildDate = "unknown"
)

const modulePath = "github.com/somaz94/agentport"

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		fromBuildInfo(info)
	}
}

// fromBuildInfo fills in what -ldflags did not set, as after `go install …@<version>`; a checkout
// build also stamps the VCS revision and commit time, which stands in for the build date. cmd/cli is
// importable, so build info naming another main module is ignored.
func fromBuildInfo(info *debug.BuildInfo) {
	if Version != "dev" || info.Main.Path != modulePath || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return
	}
	Version = info.Main.Version
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && GitCommit == "none":
			GitCommit = s.Value[:min(7, len(s.Value))]
		case s.Key == "vcs.time" && BuildDate == "unknown":
			BuildDate = s.Value
		}
	}
}

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
