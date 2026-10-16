package check

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

const ()

type composeService struct {
	// File is the compose file that defines the service: compose.yaml or
	// a file it includes.
	File        string   `yaml:"-"`
	Profiles    []string `yaml:"profiles"`
	Environment any      `yaml:"environment"` // map or list of KEY=VALUE
	Healthcheck *struct {
		Test    stringList `yaml:"test"`
		Disable bool       `yaml:"disable"`
	} `yaml:"healthcheck"`
	ReadOnly    bool     `yaml:"read_only"`
	CapDrop     []string `yaml:"cap_drop"`
	SecurityOpt []string `yaml:"security_opt"`
	Ports       []any    `yaml:"ports"`
	Deploy      struct {
		Resources struct {
			Limits struct {
				Memory string `yaml:"memory"`
			} `yaml:"limits"`
		} `yaml:"resources"`
	} `yaml:"deploy"`
}

// stringList accepts a YAML string or list of strings.
type stringList []string

func (l *stringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*l = []string{n.Value}
		return nil
	}
	var s []string
	err := n.Decode(&s)
	*l = s
	return err
}

// composeInclude is one entry of a compose file's include list: a path, or
// an object whose path is a string or list.
type composeInclude []string

func (c *composeInclude) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*c = []string{n.Value}
		return nil
	}
	var obj struct {
		Path stringList `yaml:"path"`
	}
	err := n.Decode(&obj)
	*c = composeInclude(obj.Path)
	return err
}

// loadCompose returns the compose services that run by default, skipping
// those behind a profile (such as the test runner). It follows include, so
// a component's services can live in a file its owner owns.
func (r *Repo) loadCompose() (map[string]composeService, error) {
	services := map[string]composeService{}
	seen := map[string]bool{}
	var load func(file string) error
	load = func(file string) error {
		if seen[file] {
			return nil
		}
		seen[file] = true
		data, err := r.read(file)
		if err != nil {
			return err
		}
		var f struct {
			Include  []composeInclude          `yaml:"include"`
			Services map[string]composeService `yaml:"services"`
		}
		if err := yaml.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		for name, s := range f.Services {
			if _, dup := services[name]; dup {
				return fmt.Errorf("%s: service %q is defined in more than one compose file", file, name)
			}
			s.File = file
			services[name] = s
		}
		// Included paths are relative to the including file.
		for _, inc := range f.Include {
			for _, p := range inc {
				if err := load(path.Clean(path.Join(path.Dir(file), p))); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := load(r.Spec.Layout.ComposeFile); err != nil {
		return nil, err
	}
	for name, s := range services {
		if len(s.Profiles) > 0 {
			delete(services, name)
		}
	}
	return services, nil
}

// envValues returns a compose service's environment variable values.
func (s composeService) envValues() []string {
	var out []string
	switch env := s.Environment.(type) {
	case map[string]any:
		for _, v := range env {
			out = append(out, fmt.Sprint(v))
		}
	case []any:
		for _, kv := range env {
			if _, v, ok := strings.Cut(fmt.Sprint(kv), "="); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

// commandDirs returns the directories under the layout's commands
// directory that contain Go files.
func (r *Repo) commandDirs() map[string]bool {
	dirs := map[string]bool{}
	commandsDir := r.Spec.Layout.CommandsDir
	for _, f := range r.filesUnder(commandsDir) {
		if strings.HasSuffix(f, ".go") && path.Dir(path.Dir(f)) == commandsDir {
			dirs[path.Dir(f)] = true
		}
	}
	return dirs
}

func checkServices(r *Repo) []Finding {
	var out []Finding
	compose, err := r.loadCompose()
	if err != nil {
		return []Finding{{Message: err.Error(), Path: r.Spec.Layout.ComposeFile}}
	}
	cmds := r.commandDirs()
	claimedCmd := map[string]string{}
	claimedCompose := map[string]string{}
	for _, c := range r.Spec.System.Components {
		active := c.Status == "active"
		runs := c.Kind == "service" || c.Kind == "gateway" || c.Kind == "database"
		if cmd := c.Runtime.Cmd; cmd != "" {
			if other, dup := claimedCmd[cmd]; dup {
				out = append(out, Finding{Message: fmt.Sprintf("command %s is also claimed by %q", cmd, other), Component: c.ID})
			}
			claimedCmd[cmd] = c.ID
			if active && !cmds[cmd] {
				out = append(out, Finding{Message: fmt.Sprintf("declared command %s has no Go files", cmd), Component: c.ID, Path: cmd})
			}
		} else if active && (c.Kind == "service" || c.Kind == "gateway") {
			out = append(out, Finding{Message: "active " + c.Kind + " must declare runtime.cmd", Component: c.ID})
		}
		if name := c.Runtime.Compose; name != "" {
			if other, dup := claimedCompose[name]; dup {
				out = append(out, Finding{Message: fmt.Sprintf("compose service %q is also claimed by %q", name, other), Component: c.ID})
			}
			claimedCompose[name] = c.ID
			if _, ok := compose[name]; active && !ok {
				out = append(out, Finding{Message: fmt.Sprintf("declared compose service %q is missing from %s and the files it includes", name, r.Spec.Layout.ComposeFile), Component: c.ID, Path: r.Spec.Layout.ComposeFile})
			}
		} else if active && runs {
			out = append(out, Finding{Message: "active " + c.Kind + " must declare runtime.compose", Component: c.ID})
		}
	}
	for _, cmd := range sortedKeys(cmds) {
		if _, ok := claimedCmd[cmd]; !ok {
			out = append(out, Finding{Message: fmt.Sprintf("command %s is not declared by any component in %s/system.yaml", cmd, spec.Dir), Path: cmd})
		}
	}
	for _, name := range sortedKeys(compose) {
		if _, ok := claimedCompose[name]; !ok {
			out = append(out, Finding{Message: fmt.Sprintf("compose service %q is not declared by any component in %s/system.yaml", name, spec.Dir), Path: compose[name].File})
		}
	}
	return out
}

func checkDBOwnership(r *Repo) []Finding {
	var out []Finding
	s := r.Spec
	// Spec level: each database has exactly one sql dependent.
	users := map[string][]string{}
	for _, c := range s.System.Components {
		for _, d := range c.DependsOn {
			if d.Protocol == "sql" {
				users[d.Target] = append(users[d.Target], c.ID)
			}
		}
	}
	for _, db := range s.System.Components {
		if db.Kind != "database" {
			continue
		}
		switch u := users[db.ID]; {
		case len(u) > 1:
			out = append(out, Finding{Message: fmt.Sprintf("database is used by %d components (%s); each service owns its database", len(u), strings.Join(u, ", ")), Component: db.ID})
		case len(u) == 0 && db.Status == "active":
			out = append(out, Finding{Message: "active database has no owning service (no sql dependency on it)", Component: db.ID})
		}
	}

	// Runtime level: only the owner's compose service connects to the database.
	compose, err := r.loadCompose()
	if err != nil {
		return append(out, Finding{Message: err.Error(), Path: r.Spec.Layout.ComposeFile})
	}
	byCompose := map[string]*spec.Component{}
	for i := range s.System.Components {
		if name := s.System.Components[i].Runtime.Compose; name != "" {
			byCompose[name] = &s.System.Components[i]
		}
	}
	for _, db := range s.System.Components {
		if db.Kind != "database" || db.Runtime.Compose == "" {
			continue
		}
		host := regexp.MustCompile(`(^|[@/=,])` + regexp.QuoteMeta(db.Runtime.Compose) + `([:/?,]|$)`)
		for _, name := range sortedKeys(compose) {
			if name == db.Runtime.Compose {
				continue
			}
			connects := false
			for _, v := range compose[name].envValues() {
				if host.MatchString(v) {
					connects = true
					break
				}
			}
			if !connects {
				continue
			}
			c := byCompose[name]
			switch {
			case c == nil:
				out = append(out, Finding{Message: fmt.Sprintf("compose service %q connects to database %q but is not a declared component", name, db.ID), Component: db.ID, Path: compose[name].File})
			case !c.Uses(db.ID, "sql"):
				out = append(out, Finding{Message: fmt.Sprintf("connects to database %q without declaring a sql dependency on it", db.ID), Component: c.ID, Path: compose[name].File})
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
