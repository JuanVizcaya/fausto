// Package code describes how a component's code is put together: its Go
// packages and their imports, the types and functions in each package, the
// relations between types, and the calls between functions. It feeds the
// fausto's drill-down from the architecture diagram into the code.
//
// The analysis is syntactic (go/parser, no type checker), so it needs no Go
// toolchain or module downloads. Type references, interface satisfaction,
// and calls are resolved on a best-effort basis: what can't be resolved
// without types is left out rather than guessed.
package code

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/JuanVizcaya/fausto/internal/check"
)

// Model is the code of one component.
type Model struct {
	Component string    `json:"component"`
	Packages  []Package `json:"packages"`
	// Neighbors are the packages of other components that this
	// component's packages import.
	Neighbors []Neighbor `json:"neighbors"`
	// Dependents are the imports of this component's packages by other
	// components' packages.
	Dependents []Dependent `json:"dependents"`
	Contracts  []Contract  `json:"contracts"`
	// Stubs describe types outside the component that its types refer to,
	// for drawing them in a class diagram.
	Stubs []Stub `json:"stubs"`
	// Calls are the resolved calls from or to the component's functions
	// and methods.
	Calls []Call `json:"calls"`
	// Dirs names the packages of the calls' ends, including those of
	// other components.
	Dirs map[string]Dir `json:"dirs"`
}

// Package is one Go package (a directory) of the component.
type Package struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Doc  string `json:"doc,omitempty"`
	// Role is "cmd" for a main package, "generated" for generated code,
	// and "internal" otherwise.
	Role  string   `json:"role"`
	Files []string `json:"files"`
	// Imports are the imports of other repository packages.
	Imports []Import `json:"imports"`
	// External lists the imports of the standard library and third-party
	// modules.
	External []string `json:"external"`
	// Database reports whether the package uses a database driver.
	Database  bool       `json:"database"`
	Types     []Type     `json:"types"`
	Funcs     []Func     `json:"funcs"`
	Relations []Relation `json:"relations"`
}

// Import is an import of another repository package.
type Import struct {
	Dir       string `json:"dir"`
	Component string `json:"component,omitempty"`
	// Kind is "same" (the same component), "contract" (another
	// component's generated contract), "library" (a library component),
	// or "other".
	Kind string `json:"kind"`
}

// Neighbor is a package of another component.
type Neighbor struct {
	Dir       string `json:"dir"`
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	Kind      string `json:"kind"`
}

// Dependent is an import of one of the component's packages from another
// component.
type Dependent struct {
	From      string `json:"from"`
	Component string `json:"component,omitempty"`
	To        string `json:"to"`
}

// Type is a named type.
type Type struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // struct, interface, named, or alias
	Exported bool   `json:"exported"`
	Doc      string `json:"doc,omitempty"`
	// Underlying is the type expression of a named type or alias.
	Underlying string   `json:"underlying,omitempty"`
	Pos        Pos      `json:"pos"`
	Fields     []Field  `json:"fields"`
	Methods    []Method `json:"methods"`
	Source     string   `json:"source,omitempty"`
}

// Field is a struct field.
type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Embedded bool   `json:"embedded,omitempty"`
	Exported bool   `json:"exported"`
}

// Method is a method of a type, or of an interface.
type Method struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Sig      string `json:"sig"`
	Exported bool   `json:"exported"`
	Pointer  bool   `json:"pointer,omitempty"`
	Doc      string `json:"doc,omitempty"`
	Pos      Pos    `json:"pos"`
	Source   string `json:"source,omitempty"`
}

// Func is a package-level function.
type Func struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Sig      string `json:"sig"`
	Exported bool   `json:"exported"`
	Doc      string `json:"doc,omitempty"`
	Pos      Pos    `json:"pos"`
	Source   string `json:"source,omitempty"`
}

// Pos is a place in a file.
type Pos struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// Relation connects a type in the package to another type, in the terms
// of a UML class diagram.
type Relation struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is "implements" (realization), "embeds" (generalization), "has"
	// (an association through a field), or "uses" (a dependency through a
	// method signature).
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

// Stub is a type outside the component.
type Stub struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Package   string   `json:"package"` // package name
	Dir       string   `json:"dir"`
	Component string   `json:"component,omitempty"`
	Kind      string   `json:"kind"`
	Methods   []string `json:"methods,omitempty"`
}

// Call is a call from one function or method to another. Symbol IDs are
// "dir#Name" for types and functions and "dir#Type.Method" for methods.
type Call struct {
	From string `json:"from"`
	To   string `json:"to"`
	Line int    `json:"line,omitempty"`
	// Kind is "rpc" for a gRPC client method reaching the server method
	// that implements it, in another process; empty for a Go call.
	Kind string `json:"kind,omitempty"`
}

// Dir names a package directory that calls refer to.
type Dir struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
}

// Contract is a protobuf contract of the component.
type Contract struct {
	Proto     string    `json:"proto"`
	Generated string    `json:"generated,omitempty"`
	Services  []Service `json:"services"`
	Messages  []string  `json:"messages"`
}

// Service is a gRPC service in a contract.
type Service struct {
	Name string `json:"name"`
	RPCs []RPC  `json:"rpcs"`
}

// RPC is one method of a gRPC service.
type RPC struct {
	Name          string `json:"name"`
	Request       string `json:"request"`
	Response      string `json:"response"`
	ClientStreams bool   `json:"clientStreams,omitempty"`
	ServerStreams bool   `json:"serverStreams,omitempty"`
}

// ErrUnknownComponent is returned for a component the spec doesn't declare.
var ErrUnknownComponent = fmt.Errorf("unknown component")

// Analyzer analyzes the repository at Root. It parses the code again only
// when a Go or proto file changed.
type Analyzer struct {
	Root string

	mu    sync.Mutex
	stamp string
	index *index
}

// Analyze returns the model of a component's code.
func (a *Analyzer) Analyze(component string) (*Model, error) {
	repo, err := check.LoadRepo(a.Root)
	if err != nil {
		return nil, err
	}
	c := repo.Spec.Component(component)
	if c == nil {
		return nil, fmt.Errorf("%w %q", ErrUnknownComponent, component)
	}
	ix, err := a.load(repo)
	if err != nil {
		return nil, err
	}
	m := ix.model(component)
	for _, ct := range c.Contracts {
		m.Contracts = append(m.Contracts, readContract(repo, ct.Proto, ct.Generated))
	}
	return m, nil
}

// load returns the index of the repository's code, rebuilding it when a
// source file changed.
func (a *Analyzer) load(repo *check.Repo) (*index, error) {
	h := sha256.New()
	for _, f := range repo.Files {
		if !strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, ".proto") && !strings.HasSuffix(f, "go.mod") {
			continue
		}
		fi, err := os.Stat(filepath.Join(repo.Root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", f, fi.Size(), fi.ModTime().UnixNano())
	}
	// Ownership changes with the spec, so it's part of the stamp too.
	for _, c := range repo.Spec.System.Components {
		fmt.Fprintf(h, "%s %s %v %v\n", c.ID, c.Kind, c.Paths, c.Contracts)
	}
	stamp := hex.EncodeToString(h.Sum(nil))

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.index != nil && a.stamp == stamp {
		return a.index, nil
	}
	ix, err := build(repo)
	if err != nil {
		return nil, err
	}
	a.stamp, a.index = stamp, ix
	return ix, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
