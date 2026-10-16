package proposal

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/spec"
)

// ItemChange is one spec item added, removed, or modified by a proposal.
type ItemChange struct {
	Kind   string   `json:"kind"`
	ID     string   `json:"id"`
	Name   string   `json:"name,omitempty"`
	Action string   `json:"action"` // added, removed, modified
	Fields []string `json:"fields,omitempty"`
}

func (c ItemChange) String() string {
	label := fmt.Sprintf("%s `%s`", c.Kind, c.ID)
	if c.Name != "" && c.Name != c.ID {
		label += " (" + c.Name + ")"
	}
	if c.Action == "modified" {
		return label + " changed: " + strings.Join(c.Fields, ", ")
	}
	return label + " " + c.Action
}

type item struct {
	id, name string
	fields   map[string]any
}

// items flattens a spec into comparable items by kind.
func items(s *spec.Spec) map[string]map[string]item {
	out := map[string]map[string]item{}
	add := func(kind, id, name string, v any, extra map[string]any) {
		var fields map[string]any
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &fields)
		for k, x := range extra {
			fields[k] = x
		}
		if out[kind] == nil {
			out[kind] = map[string]item{}
		}
		out[kind][id] = item{id, name, fields}
	}
	if s == nil {
		return out
	}
	for _, c := range s.System.Components {
		add("component", c.ID, c.Name, c, nil)
	}
	for _, l := range s.System.Layers {
		add("layer", l.ID, "", l, nil)
	}
	for _, t := range s.Tech.Technologies {
		add("technology", t.ID, t.Name, t, nil)
	}
	for _, p := range s.Principles.Principles {
		add("principle", p.ID, p.Title, p, nil)
	}
	for _, d := range s.Domains.Domains {
		add("domain", d.ID, d.Name, d, nil)
	}
	for _, t := range s.Domains.Glossary {
		add("term", t.Term, "", t, nil)
	}
	for _, a := range s.Agents {
		add("agent", a.ID, a.Name, a, nil)
	}
	for _, m := range s.Roadmap.Milestones {
		add("milestone", m.ID, m.Title, m, nil)
	}
	for _, r := range s.Requirements.Requirements {
		add("requirement", r.ID, r.Title, r, nil)
	}
	for _, r := range s.Risks.Risks {
		add("risk", r.ID, r.Title, r, nil)
	}
	for _, e := range s.Events.Events {
		add("event", e.ID, e.Title, e, nil)
	}
	for _, d := range s.Decisions {
		add("decision", d.ID, d.Title, d, map[string]any{"body": d.Body})
	}
	return out
}

var kindOrder = []string{"milestone", "risk", "layer", "component", "event", "requirement", "technology", "principle", "domain", "term", "agent", "decision"}

// DiffSpecs lists the items that differ between before and after.
func DiffSpecs(before, after *spec.Spec) []ItemChange {
	a, b := items(before), items(after)
	var out []ItemChange
	for _, kind := range kindOrder {
		var ids []string
		seen := map[string]bool{}
		for id := range a[kind] {
			ids, seen[id] = append(ids, id), true
		}
		for id := range b[kind] {
			if !seen[id] {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			old, inOld := a[kind][id]
			cur, inNew := b[kind][id]
			switch {
			case !inOld:
				out = append(out, ItemChange{Kind: kind, ID: id, Name: cur.name, Action: "added"})
			case !inNew:
				out = append(out, ItemChange{Kind: kind, ID: id, Name: old.name, Action: "removed"})
			default:
				var fields []string
				keys := map[string]bool{}
				for k := range old.fields {
					keys[k] = true
				}
				for k := range cur.fields {
					keys[k] = true
				}
				for k := range keys {
					if !reflect.DeepEqual(old.fields[k], cur.fields[k]) {
						fields = append(fields, k)
					}
				}
				if len(fields) > 0 {
					sort.Strings(fields)
					out = append(out, ItemChange{Kind: kind, ID: id, Name: cur.name, Action: "modified", Fields: fields})
				}
			}
		}
	}
	return out
}

// TaskPlan is the task an agent will get when a proposal is accepted.
type TaskPlan struct {
	ID          string   `json:"id"`
	Agent       string   `json:"agent"`
	Components  []string `json:"components"`
	SpecChanges []string `json:"specChanges"`
	Findings    []string `json:"findings"`
}

// PlanTasks works out which charters (agent or human) a proposal affects
// and why. Spec changes reach the owners of the components they touch;
// principles and system-wide decisions reach every charter; failing checks
// on the merged result reach the owner of the component or file. Findings
// with no owning charter are returned as unassigned for the orchestrator.
func PlanTasks(proposalID string, changes []ItemChange, before, after *spec.Spec, report *check.Report) (plans []TaskPlan, unassigned []string) {
	type acc struct {
		components map[string]bool
		changes    []string
		findings   []string
	}
	agents := map[string]*spec.Agent{}
	if after != nil {
		for i := range after.Agents {
			agents[after.Agents[i].ID] = &after.Agents[i]
		}
	}
	work := map[string]*acc{}
	get := func(agent string) *acc {
		if agents[agent] == nil {
			return nil
		}
		if work[agent] == nil {
			work[agent] = &acc{components: map[string]bool{}}
		}
		return work[agent]
	}
	specs := []*spec.Spec{before, after}
	component := func(id string) []*spec.Component {
		var out []*spec.Component
		for _, s := range specs {
			if s != nil {
				if c := s.Component(id); c != nil {
					out = append(out, c)
				}
			}
		}
		return out
	}
	// touch records that change line affects component id.
	touch := func(id, line string) {
		for _, c := range component(id) {
			if a := get(c.Owner); a != nil {
				a.components[id] = true
				if len(a.changes) == 0 || a.changes[len(a.changes)-1] != line {
					a.changes = append(a.changes, line)
				}
			}
		}
	}
	everyone := func(line string) {
		for id := range agents {
			a := get(id)
			a.changes = append(a.changes, line)
		}
	}
	componentsWhere := func(match func(spec.Component) bool) []string {
		set := map[string]bool{}
		for _, s := range specs {
			if s == nil {
				continue
			}
			for _, c := range s.System.Components {
				if match(c) {
					set[c.ID] = true
				}
			}
		}
		return sortedSet(set)
	}

	for _, ch := range changes {
		line := ch.String()
		switch ch.Kind {
		case "component":
			touch(ch.ID, line)
		case "layer":
			for _, id := range componentsWhere(func(c spec.Component) bool { return c.Layer == ch.ID }) {
				touch(id, line)
			}
		case "technology":
			for _, id := range componentsWhere(func(c spec.Component) bool { return contains(c.Tech, ch.ID) }) {
				touch(id, line)
			}
		case "principle":
			everyone(line)
		case "domain":
			for _, s := range specs {
				if s == nil {
					continue
				}
				for _, d := range s.Domains.Domains {
					if d.ID == ch.ID {
						for _, id := range d.Components {
							touch(id, line)
						}
					}
				}
			}
		case "milestone":
			for _, s := range specs {
				if m := milestone(s, ch.ID); m != nil {
					for _, id := range m.Components {
						touch(id, line)
					}
				}
			}
		case "requirement":
			for _, s := range specs {
				if s == nil {
					continue
				}
				for _, r := range s.Requirements.Requirements {
					if r.ID != ch.ID {
						continue
					}
					for i := range s.System.Components {
						if c := &s.System.Components[i]; c.Status != "planned" && r.Applies(c) {
							touch(c.ID, line)
						}
					}
				}
			}
		case "event":
			for _, s := range specs {
				if s == nil {
					continue
				}
				for _, e := range s.Events.Events {
					if e.ID == ch.ID {
						touch(e.Producer, line)
						for _, id := range e.Consumers {
							touch(id, line)
						}
					}
				}
			}
		case "risk":
			for _, s := range specs {
				if s == nil {
					continue
				}
				for _, r := range s.Risks.Risks {
					if r.ID != ch.ID {
						continue
					}
					for _, id := range r.Components {
						touch(id, line)
					}
					if a := get(r.Owner); a != nil && !contains(a.changes, line) {
						a.changes = append(a.changes, line)
					}
				}
			}
		case "agent":
			if a := get(ch.ID); a != nil {
				a.changes = append(a.changes, line)
			}
		case "decision":
			var affects []string
			for _, s := range specs {
				if s == nil {
					continue
				}
				for _, d := range s.Decisions {
					if d.ID == ch.ID {
						affects = append(affects, d.Affects...)
					}
				}
			}
			if len(affects) == 0 {
				everyone(line + " (system-wide)")
			}
			for _, id := range affects {
				touch(id, line)
			}
		}
	}

	if report != nil {
		for _, r := range report.Results {
			for _, f := range r.Findings {
				line := fmt.Sprintf("[%s] %s", r.Check, f.Message)
				if f.Component != "" {
					line = fmt.Sprintf("[%s] %s: %s", r.Check, f.Component, f.Message)
				}
				if f.Path != "" {
					line += " (`" + f.Path + "`)"
				}
				// The fix belongs to whoever owns the file to change (a
				// runtime finding is fixed in compose.yaml, for example),
				// else to the component's owner.
				owner := ""
				if f.Path != "" && after != nil {
					owner = check.OwnerOf(after, f.Path)
				}
				if (owner == "" || agents[owner] == nil) && f.Component != "" && after != nil {
					if c := after.Component(f.Component); c != nil {
						owner = c.Owner
					}
				}
				if a := get(owner); a != nil {
					a.findings = append(a.findings, line)
					if f.Component != "" {
						a.components[f.Component] = true
					}
				} else {
					unassigned = append(unassigned, line)
				}
			}
		}
	}

	// Empty lists, not null, for the UI: a proposal may affect no agent.
	plans, unassigned = []TaskPlan{}, nonNil(unassigned)
	for _, id := range sortedKeys(work) {
		a := work[id]
		// A removed component still explains itself in the change line, but
		// can't be listed in Components: the task would then reference a
		// component that no longer exists in the merged spec.
		comps := map[string]bool{}
		for cid := range a.components {
			if after != nil && after.Component(cid) != nil {
				comps[cid] = true
			}
		}
		plans = append(plans, TaskPlan{
			ID:          proposalID + "--" + id,
			Agent:       id,
			Components:  sortedSet(comps),
			SpecChanges: nonNil(a.changes),
			Findings:    nonNil(a.findings),
		})
	}
	return plans, unassigned
}

func milestone(s *spec.Spec, id string) *spec.Milestone {
	if s == nil {
		return nil
	}
	return s.Milestone(id)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedSet(set map[string]bool) []string {
	out := []string{}
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
