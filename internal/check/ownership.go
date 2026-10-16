package check

import (
	"fmt"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// ownerOf returns the agent that owns file and the matching entry. An exact
// file entry beats any directory; otherwise the longest directory wins.
func (r *Repo) ownerOf(file string) (agent, entry string) {
	best := -1
	for _, a := range r.Spec.Agents {
		for _, e := range a.Owns {
			var score int
			switch {
			case !strings.HasSuffix(e, "/") && e == file:
				score = 1 << 30
			case strings.HasSuffix(e, "/") && strings.HasPrefix(file, e):
				score = len(e)
			default:
				continue
			}
			if score > best {
				best, agent, entry = score, a.ID, e
			}
		}
	}
	return agent, entry
}

func checkOwnership(r *Repo) []Finding {
	var out []Finding
	claims := map[string]string{}
	for _, a := range r.Spec.Agents {
		for _, e := range a.Owns {
			if other, dup := claims[e]; dup {
				out = append(out, Finding{Message: fmt.Sprintf("%q is owned by both %q and %q", e, other, a.ID), Path: a.File})
			}
			claims[e] = a.ID
		}
	}
	used := map[string]bool{}
	// Files whose agent differs from their component's owner, grouped so a
	// wrong owner yields one finding per component rather than per file.
	type mismatch struct{ component, agent string }
	var order []mismatch
	mismatched := map[mismatch][]string{}
	for _, f := range r.Files {
		agent, entry := r.ownerOf(f)
		if agent == "" {
			out = append(out, Finding{Message: "no agent owns this file", Path: f})
			continue
		}
		used[entry] = true
		if c := r.componentOf(f); c != nil && c.Owner != agent {
			k := mismatch{c.ID, agent}
			if _, seen := mismatched[k]; !seen {
				order = append(order, k)
			}
			mismatched[k] = append(mismatched[k], f)
		}
	}
	for _, k := range order {
		files := mismatched[k]
		msg := fmt.Sprintf("owned by agent %q, but component %q is owned by %q", k.agent, k.component, r.Spec.Component(k.component).Owner)
		if len(files) > 1 {
			msg += fmt.Sprintf(" (and %d more files)", len(files)-1)
		}
		out = append(out, Finding{Message: msg, Component: k.component, Path: files[0]})
	}
	for _, a := range r.Spec.Agents {
		for _, e := range a.Owns {
			if !used[e] {
				out = append(out, Finding{Message: fmt.Sprintf("agent %q owns %q, which matches no files", a.ID, e), Path: a.File})
			}
		}
	}
	return out
}

// OwnerOf returns the agent that owns file under s, or "".
func OwnerOf(s *spec.Spec, file string) string {
	agent, _ := (&Repo{Spec: s}).ownerOf(file)
	return agent
}
