package check

import (
	"fmt"
	"io"
	"strings"
)

// WriteText writes the report as `fausto check` prints it.
func (report *Report) WriteText(w io.Writer) {
	failed := 0
	for _, r := range report.Results {
		fmt.Fprintf(w, "%-7s  %-12s  %s\n", strings.ToUpper(string(r.Status)), r.Check, r.Description)
		for _, f := range r.Findings {
			var where []string
			if f.Component != "" {
				where = append(where, f.Component)
			}
			if f.Path != "" {
				where = append(where, f.Path)
			}
			loc := ""
			if len(where) > 0 {
				loc = "[" + strings.Join(where, " ") + "] "
			}
			fmt.Fprintf(w, "         - %s%s\n", loc, f.Message)
		}
		if r.Command != "" && r.Status == Skipped {
			fmt.Fprintf(w, "         - not run: %s (run it with fausto check -commands)\n", r.Command)
		}
		if r.Status == Fail {
			failed++
		}
	}
	if failed == 0 {
		fmt.Fprintln(w, "\nThe code matches the system spec.")
	} else {
		fmt.Fprintf(w, "\n%d of %d checks failed.\n", failed, len(report.Results))
	}
}
