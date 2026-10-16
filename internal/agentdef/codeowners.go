package agentdef

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// CodeOwners is the GitHub CODEOWNERS file generated from the charters,
// relative to the repository root.
const CodeOwners = ".github/CODEOWNERS"

// RenderCodeOwners returns the CODEOWNERS file for the charters: every
// owned path, reviewed by its charter's reviewers, or by the reviewers of
// every human charter when its own has none, since people supervise the
// agents. It returns nil when no charter names reviewers.
func RenderCodeOwners(s *spec.Spec) []byte {
	var humans []string
	for _, a := range s.Agents {
		if a.Kind == "human" {
			humans = appendNew(humans, a.Reviewers...)
		}
	}
	type line struct{ path, owners string }
	var lines []line
	for _, a := range s.Agents {
		reviewers := a.Reviewers
		if len(reviewers) == 0 {
			reviewers = humans
		}
		if len(reviewers) == 0 {
			continue
		}
		for _, p := range a.Owns {
			lines = append(lines, line{p, strings.Join(reviewers, " ")})
		}
	}
	if len(lines) == 0 {
		return nil
	}
	// GitHub applies the last matching line, and fausto gives a file to its
	// most specific entry: a file entry beats any directory, and a longer
	// directory beats a shorter one. So list directories from shortest to
	// longest, then files.
	sort.SliceStable(lines, func(i, j int) bool {
		di, dj := strings.HasSuffix(lines[i].path, "/"), strings.HasSuffix(lines[j].path, "/")
		if di != dj {
			return di
		}
		if di && len(lines[i].path) != len(lines[j].path) {
			return len(lines[i].path) < len(lines[j].path)
		}
		return lines[i].path < lines[j].path
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# %s from the agent charters in %s/agents. Don't edit it:\n", generatedBy, spec.Dir)
	b.WriteString("# change a charter's owns or reviewers through a fausto proposal.\n\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "/%s %s\n", l.path, l.owners)
	}
	return []byte(b.String())
}

func appendNew(list []string, items ...string) []string {
	for _, it := range items {
		found := false
		for _, have := range list {
			found = found || have == it
		}
		if !found {
			list = append(list, it)
		}
	}
	return list
}
