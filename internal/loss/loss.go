// Package loss records, field by field, what a conversion kept and what it could not.
package loss

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// Status is what happened to one source field.
type Status string

// Statuses, from lossless to lossy.
const (
	Mapped       Status = "mapped"
	Transformed  Status = "transformed"
	Approximated Status = "approximated"
	Dropped      Status = "dropped"
	Warn         Status = "warn"
)

// ExitLossy is the exit code `--strict` uses when any report is lossy.
const ExitLossy = 2

// Entry is the outcome for one field.
type Entry struct {
	Field  string `json:"field"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Report is the outcome of converting one item.
type Report struct {
	Source  string  `json:"source"`
	Target  string  `json:"target"`
	Entries []Entry `json:"entries"`
}

// Add records an entry with a literal detail.
func (r *Report) Add(field string, s Status, detail string) {
	r.Entries = append(r.Entries, Entry{Field: field, Status: s, Detail: detail})
}

// Addf records an entry whose detail is formatted like fmt.Sprintf.
func (r *Report) Addf(field string, s Status, format string, args ...any) {
	r.Add(field, s, fmt.Sprintf(format, args...))
}

// Lossy reports whether anything was approximated, dropped or warned about.
func (r Report) Lossy() bool {
	for _, e := range r.Entries {
		if e.Status == Approximated || e.Status == Dropped || e.Status == Warn {
			return true
		}
	}
	return false
}

// AnyLossy reports whether any report is lossy.
func AnyLossy(reports []Report) bool {
	for _, r := range reports {
		if r.Lossy() {
			return true
		}
	}
	return false
}

// WriteText renders reports as one aligned block per item.
func WriteText(w io.Writer, reports []Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, r := range reports {
		if i > 0 {
			fmt.Fprintln(tw)
		}
		fmt.Fprintf(tw, "%s -> %s\n", r.Source, r.Target)
		for _, e := range r.Entries {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", e.Status, e.Field, e.Detail)
		}
	}
	return tw.Flush()
}

// WriteJSON renders reports as an indented JSON array.
func WriteJSON(w io.Writer, reports []Report) error {
	out := make([]Report, len(reports))
	for i, r := range reports {
		if r.Entries == nil {
			r.Entries = []Entry{}
		}
		out[i] = r
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}
