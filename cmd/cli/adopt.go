package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/reconcile"
	"github.com/somaz94/agentport/internal/textdiff"
)

type adoptOptions struct {
	hubOptions
	apply, force bool
}

func newAdoptCmd(opts *options) *cobra.Command {
	a := &adoptOptions{}
	cmd := &cobra.Command{
		Use:   "adopt <target-path>",
		Short: "Bring an edit made in a target back into the hub (preview unless --apply is given)",
		Long: "Compare a skill directory or agent file that sync wrote with what the hub converts to now,\n" +
			"and carry each edited field the hub item can hold back into it, keeping every other hub\n" +
			"field as written. What the hub cannot hold is listed and left out. The conversion must not\n" +
			"have changed since the file was written, or adopting would undo that change; --force adopts\n" +
			"anyway, unless the hub item was renamed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdopt(cmd, opts, a, args[0])
		},
	}
	a.register(cmd, false)
	f := cmd.Flags()
	f.BoolVar(&a.apply, "apply", false, "write the changes into the hub")
	f.BoolVar(&a.force, "force", false, "adopt even when the hub changed since the file was written")
	return cmd
}

func runAdopt(cmd *cobra.Command, opts *options, a *adoptOptions, target string) error {
	ro, err := a.resolve()
	if err != nil {
		return err
	}
	ro.Force = a.force
	adoption, err := reconcile.Adopt(ro, target)
	if err != nil {
		return err
	}
	inSync := false
	var applyErr, planErr error
	if a.apply && len(adoption.Changes) > 0 {
		if applyErr = adoption.Apply(); applyErr == nil {
			ro.Targets = []harness.ID{adoption.Harness}
			ro.Force = false
			var plan *reconcile.Plan
			if plan, planErr = reconcile.New(ro); planErr == nil {
				inSync = unitInSync(plan, adoption.Unit)
			}
		}
	}
	// What adopting writes into the hub is shown even when writing or checking it failed.
	if err := writeAdoption(cmd.OutOrStdout(), opts.output, adoption, a.apply && applyErr == nil, inSync); err != nil {
		return err
	}
	if applyErr != nil {
		return fmt.Errorf("adopting %s failed part way; the hub may already hold some of the changes above: %w", adoption.Unit, applyErr)
	}
	if planErr != nil {
		return fmt.Errorf("adopted, but checking %s against the hub failed: %w", adoption.Unit, planErr)
	}
	if opts.output != outputText {
		return nil
	}
	switch {
	case len(adoption.Changes) == 0:
	case !a.apply:
		fmt.Fprintln(cmd.ErrOrStderr(), "dry run: nothing was written; pass --apply to write")
	case inSync:
		fmt.Fprintf(cmd.ErrOrStderr(), "%s now matches what the hub converts to; the next sync records it\n", adoption.Unit)
	default:
		fmt.Fprintf(cmd.ErrOrStderr(), "%s still differs from what the hub converts to; sync --force would replace it\n", adoption.Unit)
	}
	return nil
}

// unitInSync reports whether every file of the unit at path classifies as unchanged.
func unitInSync(plan *reconcile.Plan, path string) bool {
	for _, t := range plan.Targets {
		for _, u := range t.Units {
			if u.Path != path || u.Status != "" {
				continue
			}
			for _, f := range u.Files {
				if f.State != manifest.StateUnchanged {
					return false
				}
			}
			return len(u.Files) > 0
		}
	}
	return false
}

type adoptionJSON struct {
	Harness  harness.ID   `json:"harness"`
	Unit     string       `json:"unit"`
	Source   string       `json:"source"`
	Changes  []changeJSON `json:"changes"`
	Notes    []string     `json:"notes"`
	Reformat bool         `json:"reformat"`
	Applied  bool         `json:"applied"`
	InSync   bool         `json:"inSync"`
}

type changeJSON struct {
	Path    string `json:"path"`
	Created bool   `json:"created,omitempty"`
	Diff    string `json:"diff"`
}

func writeAdoption(w io.Writer, format string, a *reconcile.Adoption, applied, inSync bool) error {
	changes := make([]changeJSON, len(a.Changes))
	for i, c := range a.Changes {
		changes[i] = changeJSON{Path: c.Path, Created: c.Old == nil, Diff: changeDiff(c)}
	}
	if format == outputJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(adoptionJSON{Harness: a.Harness, Unit: a.Unit, Source: a.Source, Changes: changes,
			Notes: append([]string{}, a.Notes...), Reformat: a.Reformat, Applied: applied && len(changes) > 0, InSync: inSync})
	}
	fmt.Fprintf(w, "adopt %s (%s) into %s\n", a.Unit, a.Harness, a.Source)
	switch {
	case a.Reformat:
		fmt.Fprintln(w, "nothing to adopt: it differs from what the hub converts to only in ways the hub does not keep, such as quoting; sync --force rewrites it")
	case len(changes) == 0 && len(a.Notes) == 0:
		fmt.Fprintln(w, "nothing to adopt: it holds no edit the hub lacks")
	case len(changes) == 0:
		fmt.Fprintln(w, "nothing to adopt")
	}
	for _, c := range changes {
		fmt.Fprint(w, "\n", c.Diff)
	}
	if len(a.Notes) > 0 {
		fmt.Fprintln(w, "\nnot adopted:")
		for _, n := range a.Notes {
			fmt.Fprintln(w, "  "+n)
		}
	}
	return nil
}

func changeDiff(c reconcile.Change) string {
	switch {
	case c.Old != nil && bytes.Equal(c.Old, c.New):
		return fmt.Sprintf("mode of %s becomes %s\n", c.Path, manifest.Mode(c.Mode))
	case !utf8.Valid(c.Old) || !utf8.Valid(c.New):
		return fmt.Sprintf("binary file %s differs\n", c.Path)
	}
	from := c.Path
	if c.Old == nil {
		from = "/dev/null"
	}
	return textdiff.Unified(from, c.Path, c.Old, c.New)
}
