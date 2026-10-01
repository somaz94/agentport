package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const (
	outputText = "text"
	outputJSON = "json"
)

type options struct {
	output string
}

// NewRootCmd builds the command tree. Each call returns fresh state, so tests can run commands
// in isolation.
func NewRootCmd() *cobra.Command {
	// Keeps the --output check running when a subcommand later adds its own PersistentPreRunE.
	cobra.EnableTraverseRunHooks = true
	opts := &options{}
	root := &cobra.Command{
		Use:   "agentport",
		Short: "Port Claude Code skills, commands and agents to Codex and Antigravity",
		Long: "agentport translates Claude Code customizations into Codex and Antigravity formats,\n" +
			"reports field by field what each conversion kept and lost, and never touches a file it\n" +
			"did not write.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if opts.output != outputText && opts.output != outputJSON {
				return fmt.Errorf("invalid --output %q (want %s or %s)", opts.output, outputText, outputJSON)
			}
			return nil
		},
	}
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", outputText, "output format: text or json")
	root.AddCommand(newVersionCmd(opts), newMapCmd(opts))
	return root
}

// Execute runs the root command and prints any error to stderr.
func Execute() error {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return err
	}
	return nil
}
