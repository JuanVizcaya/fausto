package check

import (
	"fmt"
	"io"
	"strings"
)

// WriteGitHub writes the report's findings as GitHub Actions workflow
// commands, so each shows as an annotation on its file in the pull request.
func (report *Report) WriteGitHub(w io.Writer) {
	for _, r := range report.Results {
		for _, f := range r.Findings {
			props := []string{"title=" + escapeProperty("fausto "+r.Check)}
			if f.Path != "" {
				props = append(props, "file="+escapeProperty(f.Path))
			}
			msg := f.Message
			if f.Component != "" {
				msg = f.Component + ": " + msg
			}
			fmt.Fprintf(w, "::error %s::%s\n", strings.Join(props, ","), escapeData(msg))
		}
	}
}

// WriteMarkdown writes the report as a Markdown summary, for a workflow's
// job summary.
func (report *Report) WriteMarkdown(w io.Writer) {
	failed := 0
	for _, r := range report.Results {
		if r.Status == Fail {
			failed++
		}
	}
	if failed == 0 {
		fmt.Fprintln(w, "### fausto check: the code matches the system spec")
	} else {
		fmt.Fprintf(w, "### fausto check: %d of %d checks found drift\n", failed, len(report.Results))
	}
	fmt.Fprintln(w, "\n| Check | Result | Findings |\n|---|---|---|")
	icon := map[Status]string{Pass: "✅ pass", Fail: "❌ fail", Skipped: "⏭️ skipped"}
	for _, r := range report.Results {
		fmt.Fprintf(w, "| `%s` | %s | %d |\n", r.Check, icon[r.Status], len(r.Findings))
	}
	for _, r := range report.Results {
		if len(r.Findings) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n<details><summary><code>%s</code>: %s</summary>\n\n", r.Check, r.Description)
		for _, f := range r.Findings {
			line := "- "
			if f.Component != "" {
				line += "**" + f.Component + "** "
			}
			if f.Path != "" {
				line += "`" + f.Path + "` "
			}
			fmt.Fprintln(w, line+f.Message)
		}
		fmt.Fprintln(w, "\n</details>")
	}
}

func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}
