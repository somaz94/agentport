package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/somaz94/agentport/internal/config"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/loss"
	"github.com/somaz94/agentport/internal/manifest"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/reconcile"
)

// exitOutOfSync is the exit code `sync --check` uses when a target is not in sync with the hub.
const exitOutOfSync = 3

// hubOptions are the flags every command that reads the hub and its targets shares.
type hubOptions struct {
	to     []string
	root   string
	config string
}

// register adds --root and --config, and --to when the command picks its targets.
func (h *hubOptions) register(cmd *cobra.Command, withTargets bool) {
	f := cmd.Flags()
	if withTargets {
		f.StringSliceVar(&h.to, "to", nil, "target harnesses (repeatable; default: the config's targets, or every one that is set up): "+strings.Join(targetNames(), ", "))
	}
	f.StringVar(&h.root, "root", "", "home directory holding the hub and the targets (default: yours)")
	f.StringVar(&h.config, "config", "", "settings file (default: $XDG_CONFIG_HOME/agentport/config.yaml)")
}

// targetNames are the harnesses --to accepts: every one but the hub.
func targetNames() []string {
	var out []string
	for _, h := range harness.All {
		if h != config.Default().Hub {
			out = append(out, string(h))
		}
	}
	return out
}

// resolve loads the settings and turns the flags into reconcile options.
func (h *hubOptions) resolve() (reconcile.Options, error) {
	cfg, err := config.Load(h.config)
	if err != nil {
		return reconcile.Options{}, err
	}
	root, err := scopeRoot(paths.ScopeUser, h.root)
	if err != nil {
		return reconcile.Options{}, err
	}
	targets := cfg.Targets
	if len(h.to) > 0 {
		if targets, err = config.ParseTargets(h.to, cfg.Hub); err != nil {
			return reconcile.Options{}, fmt.Errorf("--to: %w", err)
		}
	}
	return reconcile.Options{Root: root, Config: cfg, Targets: targets, Generator: "agentport " + Version}, nil
}

type syncOptions struct {
	hubOptions
	apply, check, strict, force bool
}

func newSyncCmd(opts *options) *cobra.Command {
	s := &syncOptions{}
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Convert the whole hub into each target harness (preview unless --apply is given)",
		Long: "Convert every skill, command and agent in the hub (~/.claude) into each target and show\n" +
			"what would change. With --apply, write new and updated files and delete those whose source\n" +
			"is gone. A file agentport does not track is never changed, and is reported as a conflict when\n" +
			"it is in the way; a file edited since agentport wrote it is reported as drift and changed\n" +
			"only with --force.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, opts, s)
		},
	}
	s.register(cmd, true)
	f := cmd.Flags()
	f.BoolVar(&s.apply, "apply", false, "write the changes")
	f.BoolVar(&s.check, "check", false, fmt.Sprintf("write nothing; exit %d when a target is out of sync", exitOutOfSync))
	f.BoolVar(&s.strict, "strict", false, fmt.Sprintf("exit %d when any conversion is lossy; nothing is written", loss.ExitLossy))
	f.BoolVar(&s.force, "force", false, "replace files edited in a target, and stop tracking edited files whose source is gone")
	return cmd
}

func runSync(cmd *cobra.Command, opts *options, s *syncOptions) error {
	if s.check && s.apply {
		return errors.New("--check never writes; drop --apply")
	}
	ro, err := s.resolve()
	if err != nil {
		return err
	}
	ro.Force = s.force
	plan, err := reconcile.New(ro)
	if err != nil {
		return err
	}
	strict := s.strict && plan.Lossy()
	var applyErr error
	if s.apply && !strict {
		applyErr = plan.Apply()
	}
	if err := writePlan(cmd.OutOrStdout(), opts.output, plan, false, s.apply && !strict && applyErr == nil); err != nil {
		return err
	}
	warn(cmd.ErrOrStderr(), plan)
	switch {
	case applyErr != nil:
		return applyErr
	case strict:
		return &exitError{code: loss.ExitLossy, err: errors.New("a conversion is lossy (--strict); nothing was written")}
	case failed(plan):
		return errors.New("some items could not be synced; see above")
	case s.check && plan.Changes():
		return &exitError{code: exitOutOfSync}
	}
	if !s.apply && !s.check && opts.output == outputText {
		fmt.Fprintln(cmd.ErrOrStderr(), "dry run: nothing was written; pass --apply to write")
	}
	return nil
}

func newStatusCmd(opts *options) *cobra.Command {
	h := &hubOptions{}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the state of every file in each target: managed, edited, orphaned or someone else's",
		Long: "Classify everything in each target's skill and agent directories against the hub and the\n" +
			"manifest of files agentport wrote: unchanged, new or update (the hub changed), orphan (its\n" +
			"source is gone), drift (edited in the target), conflict (someone else's file is in the way)\n" +
			"and unmanaged (not agentport's). Nothing is written.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ro, err := h.resolve()
			if err != nil {
				return err
			}
			plan, err := reconcile.New(ro)
			if err != nil {
				return err
			}
			if err := writePlan(cmd.OutOrStdout(), opts.output, plan, true, false); err != nil {
				return err
			}
			warn(cmd.ErrOrStderr(), plan)
			return nil
		},
	}
	h.register(cmd, true)
	return cmd
}

func warn(w io.Writer, plan *reconcile.Plan) {
	for _, msg := range plan.Warnings {
		fmt.Fprintln(w, "warning:", msg)
	}
	for _, t := range plan.Targets {
		for _, msg := range t.Warnings {
			fmt.Fprintf(w, "warning: %s: %s\n", t.Harness, msg)
		}
	}
}

func failed(plan *reconcile.Plan) bool {
	for _, t := range plan.Targets {
		if t.Err != nil {
			return true
		}
		for _, u := range t.Units {
			if u.Status == reconcile.Failed {
				return true
			}
		}
	}
	return false
}

type planJSON struct {
	Root     string       `json:"root"`
	Applied  bool         `json:"applied"`
	Targets  []targetJSON `json:"targets"`
	Warnings []string     `json:"warnings"`
}

type targetJSON struct {
	Harness   harness.ID     `json:"harness"`
	Manifest  string         `json:"manifest,omitempty"`
	Error     string         `json:"error,omitempty"`
	Counts    map[string]int `json:"counts"`
	Units     []unitJSON     `json:"units"`
	Unmanaged []string       `json:"unmanaged"`
	Warnings  []string       `json:"warnings"`
}

type unitJSON struct {
	Kind   string       `json:"kind"`
	Path   string       `json:"path,omitempty"`
	Source string       `json:"source,omitempty"`
	State  string       `json:"state"`
	Reason string       `json:"reason,omitempty"`
	Files  []fileState  `json:"files,omitempty"`
	Report *loss.Report `json:"report,omitempty"`
}

type fileState struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// unitState sums a unit up in one word: its status, the state all its files share, update when a
// sync would change any of them, or else the state that needs attention.
func unitState(u *reconcile.Unit) string {
	if u.Status != "" {
		return u.Status
	}
	counts := map[manifest.State]int{}
	for _, f := range u.Files {
		counts[f.State]++
	}
	for s, n := range counts {
		if n == len(u.Files) {
			return string(s)
		}
	}
	for _, s := range []manifest.State{manifest.StateNew, manifest.StateUpdate, manifest.StateOrphan} {
		if counts[s] > 0 {
			return string(manifest.StateUpdate)
		}
	}
	for _, s := range []manifest.State{manifest.StateConflict, manifest.StateDrift} {
		if counts[s] > 0 {
			return string(s)
		}
	}
	return string(manifest.StateUnchanged)
}

// fileNotes explain the file states that need attention.
var fileNotes = map[manifest.State]string{
	manifest.StateDrift:    "edited since agentport wrote it; adopt it, or --force replaces it",
	manifest.StateConflict: "not written by agentport, or not a regular file",
}

func writePlan(w io.Writer, format string, plan *reconcile.Plan, all, applied bool) error {
	if format == outputJSON {
		out := planJSON{Root: plan.Root, Applied: applied, Warnings: append([]string{}, plan.Warnings...)}
		for _, t := range plan.Targets {
			tj := targetJSON{Harness: t.Harness, Manifest: t.Manifest, Counts: counts(t), Units: []unitJSON{},
				Unmanaged: append([]string{}, t.Unmanaged...), Warnings: append([]string{}, t.Warnings...)}
			if t.Err != nil {
				tj.Error = t.Err.Error()
			}
			for _, u := range t.Units {
				uj := unitJSON{Kind: string(u.Kind), Path: u.Path, Source: u.Source, State: unitState(u), Reason: u.Reason, Report: u.Report}
				// A unit with a status is left as it is, so its files have no state of their own.
				for _, f := range u.Files {
					if u.Status == "" {
						uj.Files = append(uj.Files, fileState{f.Path, string(f.State)})
					}
				}
				tj.Units = append(tj.Units, uj)
			}
			out.Targets = append(out.Targets, tj)
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(out)
	}
	var buf bytes.Buffer
	for i, t := range plan.Targets {
		if i > 0 {
			fmt.Fprintln(&buf)
		}
		if t.Err != nil {
			fmt.Fprintf(&buf, "%s: %v\n", t.Harness, t.Err)
			continue
		}
		fmt.Fprintf(&buf, "%s: %s\n", t.Harness, summarize(counts(t)))
		tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		for _, u := range t.Units {
			state := unitState(u)
			if all || state != string(manifest.StateUnchanged) {
				fmt.Fprintf(tw, "  %s\t%s\t%s\n", state, unitLabel(u), unitDetail(u, state))
			}
			if u.Status != "" || len(u.Files) < 2 {
				continue
			}
			for _, f := range u.Files {
				if note := fileNotes[f.State]; note != "" {
					fmt.Fprintf(tw, "  %s\t%s\t%s\n", f.State, f.Path, note)
				}
			}
		}
		if all {
			for _, p := range t.Unmanaged {
				fmt.Fprintf(tw, "  %s\t%s\t\n", manifest.StateUnmanaged, p)
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	// The detail column is often empty, and tabwriter pads the cell before it.
	for _, line := range strings.SplitAfter(buf.String(), "\n") {
		text, newline := strings.CutSuffix(line, "\n")
		if newline {
			text = strings.TrimRight(text, " ") + "\n"
		}
		if _, err := io.WriteString(w, text); err != nil {
			return err
		}
	}
	return nil
}

func unitLabel(u *reconcile.Unit) string {
	if u.Path != "" {
		return u.Path
	}
	return u.Source
}

func unitDetail(u *reconcile.Unit, state string) string {
	if u.Reason != "" {
		return u.Reason
	}
	var parts []string
	if state == string(manifest.StateNew) || state == string(manifest.StateUpdate) {
		if n := len(u.Files); n > 1 {
			parts = append(parts, fmt.Sprintf("%d files", n))
		}
	}
	if u.Report != nil && (state == string(manifest.StateNew) || state == string(manifest.StateUpdate)) {
		if l := lossSummary(*u.Report); l != "" {
			parts = append(parts, l)
		}
	}
	if state == string(manifest.StateOrphan) {
		parts = append(parts, "its source "+u.Source+" is gone")
	}
	if len(u.Files) == 1 {
		if note := fileNotes[u.Files[0].State]; note != "" {
			parts = append(parts, note)
		}
	}
	return strings.Join(parts, "; ")
}

func lossSummary(r loss.Report) string {
	counts := map[loss.Status]int{}
	for _, e := range r.Entries {
		counts[e.Status]++
	}
	var parts []string
	for _, s := range []loss.Status{loss.Dropped, loss.Approximated, loss.Warn} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	return strings.Join(parts, ", ")
}

// counts tallies a target's units by state, and the unmanaged paths.
func counts(t *reconcile.Target) map[string]int {
	out := map[string]int{}
	for _, u := range t.Units {
		out[unitState(u)]++
	}
	if len(t.Unmanaged) > 0 {
		out[string(manifest.StateUnmanaged)] = len(t.Unmanaged)
	}
	return out
}

func summarize(c map[string]int) string {
	var parts []string
	for _, s := range []string{"unchanged", "new", "update", "orphan", "drift", "conflict", "skipped", "error", "unmanaged"} {
		if n := c[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	if len(parts) == 0 {
		return "nothing to sync"
	}
	return strings.Join(parts, ", ")
}
