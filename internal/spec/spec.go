// Package spec loads the system specification under system/: components,
// layers, technologies, principles, domains, agent charters, and decisions.
package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Dir is the spec directory, relative to the repository root.
const Dir = "system"

// Spec is the parsed contents of system/.
type Spec struct {
	System       System
	Tech         Tech
	Principles   Principles
	Domains      Domains
	Roadmap      Roadmap
	Requirements Requirements
	Risks        Risks
	Events       Events
	Agents       []Agent
	Decisions    []Decision
	// Layout is where the project keeps its code (see LayoutFile).
	Layout Layout
	// Charters says where the agent charters came from: ChartersInRepo,
	// ChartersInFausto, or "" when there are none, so ownership is unknown.
	Charters string
	// Documents holds every file in JSON-compatible form, for schema validation.
	Documents []Document
}

// Document is one spec file and the schema it must satisfy.
type Document struct {
	Path   string // relative to the repository root
	Schema string // file name under system/schema/
	Value  any
}

type System struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Layers      []Layer     `json:"layers"`
	Components  []Component `json:"components"`
}

type Layer struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	MayDependOn []string `json:"may_depend_on"`
}

type Component struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Kind        string       `json:"kind"`
	Layer       string       `json:"layer"`
	Status      string       `json:"status"`
	Owner       string       `json:"owner"`
	Tech        []string     `json:"tech,omitempty"`
	Paths       []string     `json:"paths,omitempty"`
	Runtime     Runtime      `json:"runtime,omitzero"`
	Contracts   []Contract   `json:"contracts,omitempty"`
	Data        []Table      `json:"data,omitempty"`
	DependsOn   []Dependency `json:"depends_on,omitempty"`
}

type Runtime struct {
	Cmd     string `json:"cmd,omitempty"`
	Compose string `json:"compose,omitempty"`
}

type Contract struct {
	Proto     string `json:"proto"`
	Generated string `json:"generated,omitempty"`
}

type Table struct {
	Table string   `json:"table"`
	PII   []string `json:"pii,omitempty"`
}

type Dependency struct {
	Target      string `json:"target"`
	Protocol    string `json:"protocol"`
	Description string `json:"description,omitempty"`
}

type Tech struct {
	Technologies []Technology `json:"technologies"`
}

type Technology struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Notes    string `json:"notes,omitempty"`
}

type Principles struct {
	Principles []Principle `json:"principles"`
}

type Principle struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Rationale string   `json:"rationale"`
	Checks    []string `json:"checks,omitempty"`
}

type Domains struct {
	Domains  []Domain `json:"domains"`
	Glossary []Term   `json:"glossary,omitempty"`
}

type Domain struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Components  []string `json:"components,omitempty"`
}

type Term struct {
	Term       string `json:"term"`
	Definition string `json:"definition"`
}

// Roadmap is the ordered list of milestones.
type Roadmap struct {
	Milestones []Milestone `json:"milestones"`
}

// Milestone is an outcome the system is working toward.
type Milestone struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	Goal         string      `json:"goal"`
	Status       string      `json:"status"`
	Target       string      `json:"target,omitempty"`
	DependsOn    []string    `json:"depends_on,omitempty"`
	Components   []string    `json:"components,omitempty"`
	Decisions    []string    `json:"decisions,omitempty"`
	ExitCriteria []Criterion `json:"exit_criteria,omitempty"`
}

// Criterion is one condition for finishing a milestone.
type Criterion struct {
	Text string `json:"text"`
	Done bool   `json:"done,omitempty"`
}

// Milestone returns the milestone with the given id, or nil.
func (s *Spec) Milestone(id string) *Milestone {
	for i := range s.Roadmap.Milestones {
		if s.Roadmap.Milestones[i].ID == id {
			return &s.Roadmap.Milestones[i]
		}
	}
	return nil
}

// Requirements lists the system's non-functional requirements.
type Requirements struct {
	Requirements []Requirement `json:"requirements"`
}

// Requirement is a non-functional requirement: a quality the system must
// have, where it applies, and how it is verified.
type Requirement struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Category  string        `json:"category"`
	Statement string        `json:"statement"`
	Target    string        `json:"target,omitempty"`
	AppliesTo Applicability `json:"applies_to,omitzero"`
	// VerifiedBy names a check ("layers") or a runtime rule
	// ("runtime/healthcheck"); empty means reviewed by hand.
	VerifiedBy string `json:"verified_by,omitempty"`
}

// Applicability selects components by kind, layer, or id. A component
// matches if it matches any listed selector; no selectors match all.
type Applicability struct {
	Kinds      []string `json:"kinds,omitempty"`
	Layers     []string `json:"layers,omitempty"`
	Components []string `json:"components,omitempty"`
}

// Applies reports whether r applies to c.
func (r *Requirement) Applies(c *Component) bool {
	a := r.AppliesTo
	if len(a.Kinds) == 0 && len(a.Layers) == 0 && len(a.Components) == 0 {
		return true
	}
	return slices.Contains(a.Kinds, c.Kind) || slices.Contains(a.Layers, c.Layer) || slices.Contains(a.Components, c.ID)
}

// Events lists the asynchronous messages services publish.
type Events struct {
	Events []Event `json:"events"`
}

// Event is an asynchronous message: who publishes it, who consumes it, and
// the protobuf message that defines it.
type Event struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Producer    string   `json:"producer"`
	Consumers   []string `json:"consumers,omitempty"`
	// Message is the fully qualified protobuf message, such as
	// habyta.residents.events.v1.ResidentRegistered.
	Message  string `json:"message"`
	Key      string `json:"key,omitempty"`
	Delivery string `json:"delivery,omitempty"`
}

// ProtoDir returns the directory of the event's protobuf package under
// protoRoot and the message name: proto/habyta/residents/events/v1 and
// ResidentRegistered.
func (e *Event) ProtoDir(protoRoot string) (dir, message string) {
	i := strings.LastIndex(e.Message, ".")
	if i < 0 {
		return "", e.Message
	}
	return protoRoot + "/" + strings.ReplaceAll(e.Message[:i], ".", "/"), e.Message[i+1:]
}

// Consumes reports whether component id consumes an event from producer.
func (s *Spec) Consumes(id, producer string) bool {
	for _, e := range s.Events.Events {
		if e.Producer == producer && slices.Contains(e.Consumers, id) {
			return true
		}
	}
	return false
}

// Risks is the risk register.
type Risks struct {
	Risks []Risk `json:"risks"`
}

// Risk is something that could go wrong, how likely and how bad it is, and
// what is being done about it.
type Risk struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Category     string   `json:"category"`
	Likelihood   string   `json:"likelihood"`
	Impact       string   `json:"impact"`
	Status       string   `json:"status"`
	Owner        string   `json:"owner"`
	Mitigation   string   `json:"mitigation,omitempty"`
	ReviewBy     string   `json:"review_by,omitempty"`
	Components   []string `json:"components,omitempty"`
	Milestones   []string `json:"milestones,omitempty"`
	Requirements []string `json:"requirements,omitempty"`
	Decisions    []string `json:"decisions,omitempty"`
}

var levels = map[string]int{"low": 1, "medium": 2, "high": 3}

// Severity is likelihood times impact, from 1 to 9.
func (r *Risk) Severity() int {
	return levels[r.Likelihood] * levels[r.Impact]
}

// Agent is a charter: what an agent (or human) is responsible for.
type Agent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	// Owns lists files, or directories ending in "/".
	Owns  []string `json:"owns"`
	Rules []string `json:"rules,omitempty"`
	// Reviewers are the GitHub users (@name) or teams (@org/team) who review
	// changes to Owns; see agentdef.RenderCodeOwners.
	Reviewers []string `json:"reviewers,omitempty"`
	// File is the charter's path relative to the repository root.
	File string `json:"-"`
}

// Decision is an architecture decision record: front matter plus a
// Markdown body.
type Decision struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Date       string   `json:"date"`
	Supersedes string   `json:"supersedes,omitempty"`
	Affects    []string `json:"affects,omitempty"`
	Body       string   `json:"-"`
	File       string   `json:"-"`
}

// Component returns the component with the given id, or nil.
func (s *Spec) Component(id string) *Component {
	for i := range s.System.Components {
		if s.System.Components[i].ID == id {
			return &s.System.Components[i]
		}
	}
	return nil
}

// Layer returns the layer with the given id, or nil.
func (s *Spec) Layer(id string) *Layer {
	for i := range s.System.Layers {
		if s.System.Layers[i].ID == id {
			return &s.System.Layers[i]
		}
	}
	return nil
}

// Uses reports whether c declares a dependency on target, using one of
// protocols if any are given.
func (c *Component) Uses(target string, protocols ...string) bool {
	for _, d := range c.DependsOn {
		if d.Target != target {
			continue
		}
		if len(protocols) == 0 {
			return true
		}
		for _, p := range protocols {
			if d.Protocol == p {
				return true
			}
		}
	}
	return false
}

// Where agent charters come from.
const (
	ChartersInRepo   = "repo"   // system/agents/*.yaml in the repository
	ChartersInFausto = "fausto" // fausto's database
)

// Options change how Load reads a spec.
type Options struct {
	// Charters, when not nil, are the agent charters (YAML, by agent id)
	// fausto keeps for the project; they replace any under system/agents/.
	Charters map[string][]byte
	// Layout, when not nil, overrides the fields of the project's layout
	// that it sets.
	Layout *Layout
}

// AgentFile is where agent id's charter lives in a project that keeps its
// charters, and where fausto shows it in one that doesn't.
func AgentFile(id string) string { return Dir + "/agents/" + id + ".yaml" }

// Load reads the spec under root/system. It returns an error only for files
// that are missing or cannot be parsed; content problems are left to
// validation.
func Load(root string) (*Spec, error) { return LoadWith(root, Options{}) }

// LoadWith is Load with options.
func LoadWith(root string, o Options) (*Spec, error) {
	s := &Spec{}
	var errs []error
	layout, err := LoadLayout(root)
	if o.Layout != nil {
		layout = layout.With(*o.Layout)
	}
	s.Layout = layout
	errs = append(errs, err)
	single := []struct {
		name, schema string
		out          any
		optional     bool
	}{
		{"system.yaml", "system.schema.json", &s.System, false},
		{"tech.yaml", "tech.schema.json", &s.Tech, false},
		{"principles.yaml", "principles.schema.json", &s.Principles, false},
		{"domains.yaml", "domains.schema.json", &s.Domains, true},
		{"roadmap.yaml", "roadmap.schema.json", &s.Roadmap, true},
		{"requirements.yaml", "requirements.schema.json", &s.Requirements, true},
		{"risks.yaml", "risks.schema.json", &s.Risks, true},
		{"events.yaml", "events.schema.json", &s.Events, true},
	}
	for _, f := range single {
		rel := filepath.ToSlash(filepath.Join(Dir, f.name))
		data, err := os.ReadFile(filepath.Join(root, rel))
		if f.optional && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		v, err := decode(rel, data, f.out)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.Documents = append(s.Documents, Document{Path: rel, Schema: f.schema, Value: v})
	}

	charters := map[string][]byte{}
	if o.Charters != nil {
		for id, data := range o.Charters {
			charters[AgentFile(id)] = data
		}
		s.Charters = ChartersInFausto
	} else {
		agentFiles, err := listFiles(root, "agents", ".yaml")
		errs = append(errs, err)
		for _, rel := range agentFiles {
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				errs = append(errs, err)
				continue
			}
			charters[rel] = data
		}
		if len(charters) > 0 {
			s.Charters = ChartersInRepo
		}
	}
	agentFiles := make([]string, 0, len(charters))
	for rel := range charters {
		agentFiles = append(agentFiles, rel)
	}
	sort.Strings(agentFiles)
	for _, rel := range agentFiles {
		data := charters[rel]
		var a Agent
		v, err := decode(rel, data, &a)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		a.File = rel
		s.Agents = append(s.Agents, a)
		s.Documents = append(s.Documents, Document{Path: rel, Schema: "agent.schema.json", Value: v})
	}

	decisionFiles, err := listFiles(root, "decisions", ".md")
	errs = append(errs, err)
	for _, rel := range decisionFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		front, body, err := splitFrontMatter(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		var d Decision
		v, err := decode(rel, front, &d)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		d.Body, d.File = body, rel
		s.Decisions = append(s.Decisions, d)
		s.Documents = append(s.Documents, Document{Path: rel, Schema: "decision.schema.json", Value: v})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return s, nil
}

// listFiles returns the files in system/<sub> with the given extension,
// sorted, as paths relative to root. A missing directory is not an error.
func listFiles(root, sub, ext string) ([]string, error) {
	return listDir(root, Dir+"/"+sub, ext)
}

// listDir returns the files in dir (relative to root) with the given
// extension, sorted. A missing directory is not an error.
func listDir(root, dir, ext string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			out = append(out, dir+"/"+e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// SplitFrontMatter splits a Markdown file into its YAML front matter and
// body.
func SplitFrontMatter(data []byte) (front []byte, body string, err error) {
	return splitFrontMatter(data)
}

func splitFrontMatter(data []byte) (front []byte, body string, err error) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, "", errors.New("missing front matter: file must start with ---")
	}
	front, after, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return nil, "", errors.New("unterminated front matter")
	}
	return front, strings.TrimSpace(string(after)), nil
}

// decode parses YAML into a JSON-compatible value and also fills out from
// it. Scalars keep their YAML type except timestamps, which stay strings so
// dates validate as written.
func decode(path string, data []byte, out any) (any, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	v, err := nodeValue(&n)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Type mismatches are reported by schema validation, so a best-effort
	// decode is enough here.
	_ = json.Unmarshal(raw, out)
	return v, nil
}

func nodeValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case 0:
		return nil, nil
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return nodeValue(n.Content[0])
	case yaml.AliasNode:
		return nodeValue(n.Alias)
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := nodeValue(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := nodeValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[n.Content[i].Value] = v
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			return n.Value == "true" || n.Value == "True" || n.Value == "TRUE", nil
		case "!!int", "!!float":
			f, err := strconv.ParseFloat(n.Value, 64)
			if err != nil {
				return n.Value, nil
			}
			return f, nil
		default:
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}
