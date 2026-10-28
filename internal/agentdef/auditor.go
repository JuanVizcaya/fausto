package agentdef

import (
	"fmt"
	"slices"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// auditorParts returns the definition of an auditor: a reviewer that reads a
// pull request against the spec and reports, citing the spec, without
// changing anything.
func auditorParts(s *spec.Spec, a *spec.Agent) (description, prompt string) {
	description = fmt.Sprintf("%s for %s. %s Use it to review a pull request, or a branch's changes, against the architecture spec in %s/.",
		a.Name, s.System.Name, strings.TrimSpace(a.Scope), spec.Dir)
	var b strings.Builder
	fmt.Fprintf(&b, "You are the **%s** of the %s project. People and agents change %s, each in the paths their charter owns; "+
		"an orchestrator (a human) decides the architecture in `%s/`. You review changes against that spec. "+
		"You never change files, and you never approve or merge: you report.\n\n", a.Name, s.System.Name, s.System.Name, spec.Dir)
	fmt.Fprintf(&b, "## Your scope\n\n%s\n\n", strings.TrimSpace(a.Scope))
	if len(a.Rules) > 0 {
		b.WriteString("## Your rules\n\n")
		for _, r := range a.Rules {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(r))
		}
		b.WriteString("\n")
	}

	b.WriteString("## What you check\n\n")
	b.WriteString("Commands already check part of the spec, and report separately: fausto's drift checks (`fausto/check`) and the project checks. " +
		"Don't repeat their findings; spend your review on what no command checks:\n\n")
	var review, other []string
	for _, p := range s.Principles.Principles {
		line := fmt.Sprintf("- `%s`: **%s** %s", p.ID, strings.TrimSpace(p.Title), strings.TrimSpace(p.Rationale))
		if len(p.Checks) == 0 || slices.Contains(p.Checks, "review") {
			review = append(review, line)
		} else {
			other = append(other, fmt.Sprintf("- `%s`: %s (checked by %s)", p.ID, strings.TrimSpace(p.Title), strings.Join(p.Checks, ", ")))
		}
	}
	if len(review) > 0 {
		fmt.Fprintf(&b, "1. The principles people enforce, from `%s/principles.yaml`. Check every change against each that applies:\n\n%s\n\n", spec.Dir, strings.Join(review, "\n"))
	}
	if len(other) > 0 {
		fmt.Fprintf(&b, "   The other principles have checks; report one only for what its check can't see:\n\n%s\n\n", strings.Join(other, "\n"))
	}
	fmt.Fprintf(&b, "2. The accepted decisions in `%s/decisions/`: a change that contradicts one needs a new decision, not a quiet exception.\n", spec.Dir)
	fmt.Fprintf(&b, "3. Ownership: match each changed file to the charter that owns it in `%s/agents/` (the most specific `owns` entry wins). "+
		"A change outside its author's paths, or one pull request spanning several owners, needs those owners' review; say which.\n", spec.Dir)
	fmt.Fprintf(&b, "4. The spec itself: a new component, contract, dependency, database, or event that `%[1]s/system.yaml` or `%[1]s/events.yaml` doesn't declare needs a spec change, proposed in fausto. "+
		"A change that creates or removes a risk in `%[1]s/risks.yaml` should say so.\n\n", spec.Dir)

	fmt.Fprintf(&b, `## How to review

1. Read `+"`CLAUDE.md`"+` and the parts of `+"`%[1]s/`"+` above.%[2]s
2. Read the whole diff against the base branch, and enough of the surrounding code to judge it. The pull request's description should name the principles it touches; check those first, then the rest.
3. Report once, as one review. For each finding give:
   - the rule: a principle id, a decision id, a charter, or the spec file it breaks; never a rule the spec doesn't state;
   - where: the file and line;
   - what is wrong, and why it matters for that rule;
   - what would fix it, or what spec change it needs.
4. Mark each finding **blocking** (it breaks a rule) or **advisory** (a risk or a question). With no findings, say so in one line, naming what you checked.
5. Be concrete and brief. Don't restate the diff, don't praise, and don't report style the spec doesn't ask for.
`, spec.Dir, specWhere(s))
	return description, b.String()
}

// specWhere tells an agent where to find a spec fausto keeps.
func specWhere(s *spec.Spec) string {
	if s.Source != spec.ChartersInFausto {
		return ""
	}
	return " The spec lives in fausto, not in the repository: `fausto spec pull` writes it to `" + spec.Dir + "/` (git ignores it), the `fausto_spec` tool reads it, and in a pull request's workflow it is the spec ref fausto handed off."
}
