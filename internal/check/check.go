// Package check verifies that the repository matches its system spec.
// Each check maps to one or more principles in system/principles.yaml.
package check

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// Finding is one way the repository differs from the spec.
type Finding struct {
	Message   string `json:"message"`
	Component string `json:"component,omitempty"`
	Path      string `json:"path,omitempty"`
	// Rule names the runtime rule that failed, for runtime findings.
	Rule string `json:"rule,omitempty"`
}

// Status is the outcome of a check.
type Status string

const (
	Pass    Status = "pass"
	Fail    Status = "fail"
	Skipped Status = "skipped"
)

// Result is the outcome of one check.
type Result struct {
	Check       string    `json:"check"`
	Description string    `json:"description"`
	Principles  []string  `json:"principles"`
	Status      Status    `json:"status"`
	Findings    []Finding `json:"findings"`
}

// Report is the outcome of a run.
type Report struct {
	OK      bool     `json:"ok"`
	Results []Result `json:"results"`
}

// Repo is what checks inspect.
type Repo struct {
	Root  string
	Spec  *spec.Spec
	Files []string // repository files, slash-separated and relative to Root
}

// Check is one named verification.
type Check struct {
	Name        string
	Description string
	Run         func(*Repo) []Finding
}

// All lists every check in the order they run.
var All = []Check{
	{"schema", "Spec files match their schemas and every reference resolves.", checkSchema},
	{"services", "Declared services match backend/cmd and compose.yaml.", checkServices},
	{"layers", "Go imports follow declared dependencies and layers.", checkLayers},
	{"db-ownership", "Each database is used by exactly one declared owner.", checkDBOwnership},
	{"contracts", "Declared protobuf contracts exist, are versioned, and are generated.", checkContracts},
	{"ownership", "Every file has one owning agent, matching its component's owner.", checkOwnership},
	{"runtime", "Compose services meet the runtime rules their requirements ask for.", checkRuntime},
	{"agents", "Files generated from the agent charters, the Claude Code agent definitions in .claude/agents and .github/CODEOWNERS, match the charters.", checkAgents},
}

// knownChecks is the list of check names, kept separate from All so the
// schema check can use it without an initialization cycle.
var knownChecks = []string{"schema", "services", "layers", "db-ownership", "contracts", "ownership", "runtime", "agents"}

// Names returns the names of all checks.
func Names() []string {
	names := make([]string, len(All))
	for i, c := range All {
		names[i] = c.Name
	}
	return names
}

// Run loads the spec under root and runs the named checks, or all checks if
// names is empty.
func Run(root string, names []string) (*Report, error) {
	for _, n := range names {
		if !slices.Contains(Names(), n) {
			return nil, fmt.Errorf("unknown check %q (available: %s)", n, strings.Join(Names(), ", "))
		}
	}
	files, err := ListFiles(root)
	if err != nil {
		return nil, err
	}
	s, loadErr := spec.Load(root)
	repo := &Repo{Root: root, Spec: s, Files: files}
	return run(repo, loadErr, names), nil
}

func run(repo *Repo, loadErr error, names []string) *Report {
	principles := map[string][]string{}
	if repo.Spec != nil {
		for _, p := range repo.Spec.Principles.Principles {
			for _, c := range p.Checks {
				principles[c] = append(principles[c], p.ID)
			}
		}
	}
	report := &Report{OK: true}
	for _, c := range All {
		if len(names) > 0 && !slices.Contains(names, c.Name) {
			continue
		}
		r := Result{Check: c.Name, Description: c.Description, Principles: principles[c.Name], Findings: []Finding{}}
		if r.Principles == nil {
			r.Principles = []string{}
		}
		switch {
		case loadErr != nil && c.Name == "schema":
			for _, e := range splitErrors(loadErr) {
				r.Findings = append(r.Findings, Finding{Message: e})
			}
		case loadErr != nil:
			r.Status = Skipped
		default:
			r.Findings = append(r.Findings, c.Run(repo)...)
		}
		if r.Status == "" {
			r.Status = Pass
			if len(r.Findings) > 0 {
				r.Status = Fail
				report.OK = false
			}
		}
		report.Results = append(report.Results, r)
	}
	return report
}

func splitErrors(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// ListFiles returns the repository's tracked and untracked-but-not-ignored
// files. Outside a git work tree it walks root, skipping .git and
// node_modules.
func ListFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-c", "safe.directory=*", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err == nil {
		var files []string
		for _, f := range bytes.Split(out, []byte{0}) {
			if len(f) == 0 {
				continue
			}
			// ls-files still lists deleted-but-unstaged files.
			if _, err := os.Stat(filepath.Join(root, string(f))); err == nil {
				files = append(files, string(f))
			}
		}
		sort.Strings(files)
		return slices.Compact(files), nil
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, err
}

// within reports whether file is dir itself or inside it.
func within(file, dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return file == dir || strings.HasPrefix(file, dir+"/")
}

// filesUnder returns the files inside dir.
func (r *Repo) filesUnder(dir string) []string {
	var out []string
	for _, f := range r.Files {
		if within(f, dir) {
			out = append(out, f)
		}
	}
	return out
}

func (r *Repo) read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(rel)))
}

// componentOf returns the component that owns the code at path: the one
// with the longest matching path, generated contract, or proto contract.
func (r *Repo) componentOf(path string) *spec.Component {
	var best *spec.Component
	bestLen := -1
	consider := func(c *spec.Component, dir string) {
		if dir != "" && within(path, dir) && len(dir) > bestLen {
			best, bestLen = c, len(dir)
		}
	}
	for i := range r.Spec.System.Components {
		c := &r.Spec.System.Components[i]
		for _, p := range c.Paths {
			consider(c, p)
		}
		for _, ct := range c.Contracts {
			consider(c, ct.Proto)
			consider(c, ct.Generated)
		}
	}
	return best
}

// contractOwner returns the component whose generated contract contains
// path and that contract, or nil.
func (r *Repo) contractOwner(path string) (*spec.Component, *spec.Contract) {
	for i := range r.Spec.System.Components {
		c := &r.Spec.System.Components[i]
		for j := range c.Contracts {
			if ct := &c.Contracts[j]; ct.Generated != "" && within(path, ct.Generated) {
				return c, ct
			}
		}
	}
	return nil, nil
}

// isEventContract reports whether a contract defines events of its owner.
func (r *Repo) isEventContract(owner *spec.Component, ct *spec.Contract) bool {
	for _, e := range r.Spec.Events.Events {
		if dir, _ := e.ProtoDir(); e.Producer == owner.ID && dir == ct.Proto {
			return true
		}
	}
	return false
}

// LoadRepo lists the repository's files and loads its spec.
func LoadRepo(root string) (*Repo, error) {
	files, err := ListFiles(root)
	if err != nil {
		return nil, err
	}
	s, err := spec.Load(root)
	if err != nil {
		return nil, err
	}
	return &Repo{Root: root, Spec: s, Files: files}, nil
}

// Read returns the contents of a repository file.
func (r *Repo) Read(rel string) ([]byte, error) { return r.read(rel) }

// ComponentOf returns the component that owns the code at path, or nil.
func (r *Repo) ComponentOf(path string) *spec.Component { return r.componentOf(path) }

// GoImportResolver returns a function that maps an import path in one of
// the repository's Go modules to its directory; other imports (standard
// library, third party) don't resolve. The resolver works even when some
// go.mod files couldn't be read, which the error reports.
func (r *Repo) GoImportResolver() (func(imp string) (dir string, ok bool), error) {
	mods, err := r.goModules()
	// Longest module path first, so nested modules win.
	modPaths := sortedKeys(mods)
	sort.Slice(modPaths, func(i, j int) bool { return len(modPaths[i]) > len(modPaths[j]) })
	return func(imp string) (string, bool) {
		for _, m := range modPaths {
			if imp == m || strings.HasPrefix(imp, m+"/") {
				return strings.TrimPrefix(path.Join(mods[m], strings.TrimPrefix(imp, m)), "/"), true
			}
		}
		return "", false
	}, err
}

// goModules maps each Go module path in the repository to its directory.
func (r *Repo) goModules() (map[string]string, error) {
	mods := map[string]string{}
	var errs []error
	for _, f := range r.Files {
		if f != "go.mod" && !strings.HasSuffix(f, "/go.mod") {
			continue
		}
		data, err := r.read(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			if mod, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
				mods[strings.Trim(strings.TrimSpace(mod), `"`)] = strings.TrimSuffix(strings.TrimSuffix(f, "go.mod"), "/")
				break
			}
		}
	}
	return mods, errors.Join(errs...)
}
