package check

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/schema"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func checkSchema(r *Repo) []Finding {
	var out []Finding
	out = append(out, validateDocuments(r)...)
	out = append(out, checkReferences(r.Spec)...)
	return out
}

func validateDocuments(r *Repo) []Finding {
	var out []Finding
	compiler := jsonschema.NewCompiler()
	schemas := map[string]*jsonschema.Schema{}
	for _, d := range r.Spec.Documents {
		sch, ok := schemas[d.Schema]
		if !ok {
			// The schemas are fausto's, embedded in it; a failure here is a
			// bug in fausto, reported against the document.
			data, err := schema.Read(d.Schema)
			if err != nil {
				out = append(out, Finding{Message: "fausto has no schema " + d.Schema, Path: d.Path})
				schemas[d.Schema] = nil
				continue
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				out = append(out, Finding{Message: "invalid schema JSON in " + d.Schema + ": " + err.Error(), Path: d.Path})
				schemas[d.Schema] = nil
				continue
			}
			url := "fausto:///schema/" + d.Schema
			if err := compiler.AddResource(url, doc); err != nil {
				out = append(out, Finding{Message: "invalid schema " + d.Schema + ": " + err.Error(), Path: d.Path})
			} else if sch, err = compiler.Compile(url); err != nil {
				out = append(out, Finding{Message: "invalid schema " + d.Schema + ": " + err.Error(), Path: d.Path})
				sch = nil
			}
			schemas[d.Schema] = sch
		}
		if sch == nil {
			continue
		}
		if err := sch.Validate(d.Value); err != nil {
			for _, msg := range validationMessages(err) {
				out = append(out, Finding{Message: msg, Path: d.Path})
			}
		}
	}
	return out
}

// validationMessages flattens a validation error into one line per cause,
// dropping the summary line that names the schema URL.
func validationMessages(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{err.Error()}
	}
	lines := strings.Split(ve.Error(), "\n")
	var out []string
	for _, l := range lines[1:] {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "-"))
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		out = []string{ve.Error()}
	}
	return out
}

// checkReferences verifies that ids are unique and that every reference
// between spec files points at something that exists.
func checkReferences(s *spec.Spec) []Finding {
	var out []Finding
	add := func(component, path, format string, args ...any) {
		out = append(out, Finding{Message: fmt.Sprintf(format, args...), Component: component, Path: path})
	}
	const systemFile = spec.Dir + "/system.yaml"

	layers := ids(s.System.Layers, func(l spec.Layer) string { return l.ID }, func(id string) {
		add("", systemFile, "duplicate layer %q", id)
	})
	components := ids(s.System.Components, func(c spec.Component) string { return c.ID }, func(id string) {
		add(id, systemFile, "duplicate component %q", id)
	})
	tech := ids(s.Tech.Technologies, func(t spec.Technology) string { return t.ID }, func(id string) {
		add("", spec.Dir+"/tech.yaml", "duplicate technology %q", id)
	})
	ids(s.Principles.Principles, func(p spec.Principle) string { return p.ID }, func(id string) {
		add("", spec.Dir+"/principles.yaml", "duplicate principle %q", id)
	})
	ids(s.Domains.Domains, func(d spec.Domain) string { return d.ID }, func(id string) {
		add("", spec.Dir+"/domains.yaml", "duplicate domain %q", id)
	})
	agents := map[string]*spec.Agent{}
	for i := range s.Agents {
		a := &s.Agents[i]
		if _, dup := agents[a.ID]; dup {
			add("", a.File, "duplicate agent %q", a.ID)
		}
		agents[a.ID] = a
		if want := spec.Dir + "/agents/" + a.ID + ".yaml"; a.File != want {
			add("", a.File, "agent %q must be defined in %s", a.ID, want)
		}
	}
	decisions := map[string]bool{}
	for _, d := range s.Decisions {
		if decisions[d.ID] {
			add("", d.File, "duplicate decision %q", d.ID)
		}
		decisions[d.ID] = true
		if !strings.HasPrefix(filepath.Base(d.File), d.ID+"-") {
			add("", d.File, "decision file name must start with its id %q", d.ID)
		}
	}

	for _, l := range s.System.Layers {
		for _, dep := range l.MayDependOn {
			if !layers[dep] {
				add("", systemFile, "layer %q may_depend_on unknown layer %q", l.ID, dep)
			}
		}
	}
	for _, c := range s.System.Components {
		if !layers[c.Layer] {
			add(c.ID, systemFile, "unknown layer %q", c.Layer)
		}
		if _, ok := agents[c.Owner]; !ok && s.Charters != "" {
			add(c.ID, systemFile, "owner %q is not an agent in %s/agents/", c.Owner, spec.Dir)
		}
		for _, t := range c.Tech {
			if !tech[t] {
				add(c.ID, systemFile, "technology %q is not in %s/tech.yaml", t, spec.Dir)
			}
		}
		for _, d := range c.DependsOn {
			target := s.Component(d.Target)
			if target == nil {
				add(c.ID, systemFile, "depends on unknown component %q", d.Target)
				continue
			}
			if target.ID == c.ID {
				add(c.ID, systemFile, "depends on itself")
				continue
			}
			if l := s.Layer(c.Layer); l != nil && c.Layer != target.Layer && !slices.Contains(l.MayDependOn, target.Layer) {
				add(c.ID, systemFile, "layer %q may not depend on layer %q (dependency on %q)", c.Layer, target.Layer, target.ID)
			}
			switch {
			case d.Protocol == "sql" && target.Kind != "database":
				add(c.ID, systemFile, "sql dependency on %q, which is not a database", target.ID)
			case d.Protocol == "library" && target.Kind != "library":
				add(c.ID, systemFile, "library dependency on %q, which is not a library", target.ID)
			case d.Protocol == "grpc" && !slices.ContainsFunc(target.Contracts, func(ct spec.Contract) bool { return ct.Proto != "" }):
				add(c.ID, systemFile, "grpc dependency on %q, which declares no protobuf contracts", target.ID)
			case d.Protocol == "contract" && !slices.ContainsFunc(target.Contracts, func(ct spec.Contract) bool { return ct.Go != "" }):
				add(c.ID, systemFile, "contract dependency on %q, which declares no Go contract", target.ID)
			case d.Protocol == "contract" && c.Process() != target.Process():
				add(c.ID, systemFile, "contract dependency on %q, which runs in another process; call it over grpc", target.ID)
			case d.Schema != "" && d.Protocol != "sql":
				add(c.ID, systemFile, "%s dependency on %q names a schema; only sql dependencies do", d.Protocol, target.ID)
			}
		}
		if h := c.Runtime.Host; h != "" {
			host := s.Component(h)
			switch {
			case host == nil:
				add(c.ID, systemFile, "runtime.host is unknown component %q", h)
			case host.ID == c.ID:
				add(c.ID, systemFile, "runtime.host is the component itself")
			case host.Runtime.Cmd == "":
				add(c.ID, systemFile, "runtime.host %q declares no runtime.cmd to run in", h)
			case host.Runtime.Host != "":
				add(c.ID, systemFile, "runtime.host %q itself runs in %q; name that one", h, host.Runtime.Host)
			default:
				if l := s.Layer(host.Layer); l != nil && host.Layer != c.Layer && !slices.Contains(l.MayDependOn, c.Layer) {
					add(c.ID, systemFile, "layer %q of host %q may not depend on layer %q", host.Layer, h, c.Layer)
				}
			}
			if c.Runtime.Cmd != "" || c.Runtime.Compose != "" || c.Runtime.Image != "" {
				add(c.ID, systemFile, "runs in %q, so it declares no runtime.cmd, runtime.compose or runtime.image of its own", h)
			}
		}
		if c.Runtime.Image != "" && c.Runtime.Cmd != "" {
			add(c.ID, systemFile, "declares both runtime.cmd and runtime.image; a component runs one or the other")
		}
	}
	for _, d := range s.Domains.Domains {
		for _, id := range d.Components {
			if !components[id] {
				add("", spec.Dir+"/domains.yaml", "domain %q lists unknown component %q", d.ID, id)
			}
		}
	}
	for _, d := range s.Decisions {
		for _, id := range d.Affects {
			if !components[id] {
				add("", d.File, "affects unknown component %q", id)
			}
		}
		if d.Supersedes != "" && !decisions[d.Supersedes] {
			add("", d.File, "supersedes unknown decision %q", d.Supersedes)
		}
	}
	out = append(out, checkRoadmap(s, components, decisions)...)
	out = append(out, checkRequirements(s, components, layers)...)
	out = append(out, checkRisks(s, components, decisions)...)
	out = append(out, checkEvents(s)...)

	return out
}

// ids collects the ids of items, calling dup for each repeated one.
func ids[T any](items []T, id func(T) string, dup func(string)) map[string]bool {
	seen := map[string]bool{}
	for _, it := range items {
		k := id(it)
		if seen[k] {
			dup(k)
		}
		seen[k] = true
	}
	return seen
}

// checkRoadmap verifies milestone references, that milestone dependencies
// form no cycle, and that done milestones are really done.
func checkRoadmap(s *spec.Spec, components, decisions map[string]bool) []Finding {
	var out []Finding
	const file = spec.Dir + "/roadmap.yaml"
	add := func(format string, args ...any) {
		out = append(out, Finding{Message: fmt.Sprintf(format, args...), Path: file})
	}
	milestones := ids(s.Roadmap.Milestones, func(m spec.Milestone) string { return m.ID }, func(id string) {
		add("duplicate milestone %q", id)
	})
	for _, m := range s.Roadmap.Milestones {
		for _, id := range m.Components {
			if !components[id] {
				add("milestone %q lists unknown component %q", m.ID, id)
			}
		}
		for _, id := range m.Decisions {
			if !decisions[id] {
				add("milestone %q lists unknown decision %q", m.ID, id)
			}
		}
		for _, id := range m.DependsOn {
			if !milestones[id] {
				add("milestone %q depends on unknown milestone %q", m.ID, id)
			} else if id == m.ID {
				add("milestone %q depends on itself", m.ID)
			}
		}
		if m.Status == "done" {
			for _, c := range m.ExitCriteria {
				if !c.Done {
					add("milestone %q is done but exit criterion %q is not", m.ID, c.Text)
				}
			}
			for _, id := range m.Components {
				if c := s.Component(id); c != nil && c.Status == "planned" {
					add("milestone %q is done but component %q is still planned", m.ID, id)
				}
			}
			for _, id := range m.DependsOn {
				if d := s.Milestone(id); d != nil && d.Status != "done" && d.Status != "dropped" {
					add("milestone %q is done but depends on %q, which is %s", m.ID, id, d.Status)
				}
			}
		}
	}
	// Depth-first search for dependency cycles.
	const (
		unvisited = iota
		visiting
		visited
	)
	state := map[string]int{}
	var path []string
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case visiting:
			start := slices.Index(path, id)
			add("milestones depend on each other in a cycle: %s", strings.Join(append(path[start:], id), " → "))
			return true
		case visited:
			return false
		}
		state[id] = visiting
		path = append(path, id)
		if m := s.Milestone(id); m != nil {
			for _, dep := range m.DependsOn {
				if dep != id && visit(dep) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		state[id] = visited
		return false
	}
	for _, m := range s.Roadmap.Milestones {
		if visit(m.ID) {
			break
		}
	}
	return out
}

func checkRequirements(s *spec.Spec, components, layers map[string]bool) []Finding {
	var out []Finding
	const file = spec.Dir + "/requirements.yaml"
	add := func(format string, args ...any) {
		out = append(out, Finding{Message: fmt.Sprintf(format, args...), Path: file})
	}
	ids(s.Requirements.Requirements, func(r spec.Requirement) string { return r.ID }, func(id string) {
		add("duplicate requirement %q", id)
	})
	for _, r := range s.Requirements.Requirements {
		for _, id := range r.AppliesTo.Components {
			if !components[id] {
				add("requirement %q applies to unknown component %q", r.ID, id)
			}
		}
		for _, id := range r.AppliesTo.Layers {
			if !layers[id] {
				add("requirement %q applies to unknown layer %q", r.ID, id)
			}
		}
		if r.VerifiedBy == "" {
			continue
		}
		name, rule, hasRule := strings.Cut(r.VerifiedBy, "/")
		switch {
		case !slices.Contains(knownChecks, name):
			add("requirement %q is verified by unknown check %q (available: %s)", r.ID, name, strings.Join(knownChecks, ", "))
		case hasRule && name != "runtime":
			add("requirement %q: only the runtime check has rules", r.ID)
		case name == "runtime" && !hasRule:
			add("requirement %q: name a runtime rule, such as runtime/healthcheck", r.ID)
		case hasRule && runtimeRules[rule] == nil:
			add("requirement %q is verified by unknown runtime rule %q (available: %s)", r.ID, rule, strings.Join(RuntimeRules(), ", "))
		}
	}
	return out
}

func checkRisks(s *spec.Spec, components, decisions map[string]bool) []Finding {
	var out []Finding
	const file = spec.Dir + "/risks.yaml"
	add := func(format string, args ...any) {
		out = append(out, Finding{Message: fmt.Sprintf(format, args...), Path: file})
	}
	ids(s.Risks.Risks, func(r spec.Risk) string { return r.ID }, func(id string) {
		add("duplicate risk %q", id)
	})
	agents := map[string]bool{}
	for _, a := range s.Agents {
		agents[a.ID] = true
	}
	requirements := map[string]bool{}
	for _, r := range s.Requirements.Requirements {
		requirements[r.ID] = true
	}
	for _, r := range s.Risks.Risks {
		if !agents[r.Owner] && s.Charters != "" {
			add("risk %q is owned by unknown agent %q", r.ID, r.Owner)
		}
		for _, id := range r.Components {
			if !components[id] {
				add("risk %q lists unknown component %q", r.ID, id)
			}
		}
		for _, id := range r.Milestones {
			if s.Milestone(id) == nil {
				add("risk %q lists unknown milestone %q", r.ID, id)
			}
		}
		for _, id := range r.Requirements {
			if !requirements[id] {
				add("risk %q lists unknown requirement %q", r.ID, id)
			}
		}
		for _, id := range r.Decisions {
			if !decisions[id] {
				add("risk %q lists unknown decision %q", r.ID, id)
			}
		}
		if r.Severity() >= 6 && r.Status != "closed" && strings.TrimSpace(r.Mitigation) == "" {
			add("risk %q has severity %d (%s likelihood, %s impact) and needs a mitigation", r.ID, r.Severity(), r.Likelihood, r.Impact)
		}
	}
	return out
}

func checkEvents(s *spec.Spec) []Finding {
	var out []Finding
	const file = spec.Dir + "/events.yaml"
	add := func(component, format string, args ...any) {
		out = append(out, Finding{Message: fmt.Sprintf(format, args...), Component: component, Path: file})
	}
	ids(s.Events.Events, func(e spec.Event) string { return e.ID }, func(id string) {
		add("", "duplicate event %q", id)
	})
	runs := func(c *spec.Component) bool { return c.Kind == "service" || c.Kind == "gateway" }
	for _, e := range s.Events.Events {
		producer := s.Component(e.Producer)
		switch {
		case producer == nil:
			add("", "event %q is produced by unknown component %q", e.ID, e.Producer)
		case !runs(producer):
			add(producer.ID, "event %q is produced by %s %q; only services and gateways publish events", e.ID, producer.Kind, producer.ID)
		}
		for _, id := range e.Consumers {
			c := s.Component(id)
			switch {
			case c == nil:
				add("", "event %q is consumed by unknown component %q", e.ID, id)
			case id == e.Producer:
				add(id, "event %q is consumed by its own producer", e.ID)
			case !runs(c):
				add(id, "event %q is consumed by %s %q; only services and gateways consume events", e.ID, c.Kind, id)
			}
		}
		if e.Status != "active" || producer == nil {
			continue
		}
		if producer.Status != "active" {
			add(producer.ID, "event %q is active but its producer %q is %s", e.ID, producer.ID, producer.Status)
		}
		dir, _ := e.ProtoDir(s.Layout.ProtoRoot)
		if !slices.ContainsFunc(producer.Contracts, func(c spec.Contract) bool { return c.Proto == dir }) {
			add(producer.ID, "event %q is active but %s is not a contract of %q; declare it under contracts in system.yaml", e.ID, dir, producer.ID)
		}
	}
	return out
}
