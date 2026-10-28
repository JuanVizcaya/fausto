// Package archtest checks the module boundaries from the architecture design:
// a module imports the platform, other modules' contract packages and its own
// packages, nothing else; contracts never import their module's internals;
// and the edge reaches modules only through contracts.
package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// requiredParts are the folders and files every module has.
var requiredParts = []string{"module.go", "contract", "service", "domain", "store", "migrations"}

// Check reports boundary violations in the Go module at root, whose module
// path is modulePath. Test files are checked too.
func Check(root, modulePath string) ([]string, error) {
	internal := modulePath + "/internal/"
	modulesDir := filepath.Join(root, "internal", "modules")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return nil, err
	}
	var violations []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, part := range requiredParts {
			if _, err := os.Stat(filepath.Join(modulesDir, e.Name(), part)); err != nil {
				violations = append(violations, fmt.Sprintf("module %s: missing %s", e.Name(), part))
			}
		}
	}
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		from := modulePath + "/" + path.Dir(filepath.ToSlash(rel))
		file, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			to, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(to, internal) {
				continue
			}
			if reason := forbidden(strings.TrimPrefix(from, internal), strings.TrimPrefix(to, internal)); reason != "" {
				violations = append(violations, fmt.Sprintf("%s imports %s: %s", filepath.ToSlash(rel), to, reason))
			}
		}
		return nil
	})
	return violations, err
}

// forbidden returns why package from (relative to internal/) may not import
// package to, or "" when it may.
func forbidden(from, to string) string {
	fromModule, fromRest, fromIsModule := splitModule(from)
	toModule, toRest, toIsModule := splitModule(to)
	isContract := func(rest string) bool { return rest == "contract" || strings.HasPrefix(rest, "contract/") }
	switch {
	case strings.HasPrefix(from, "platform"):
		if !strings.HasPrefix(to, "platform") {
			return "platform imports only platform"
		}
	case fromIsModule:
		if strings.HasPrefix(to, "platform") {
			return ""
		}
		if !toIsModule {
			return "a module imports only platform and contracts"
		}
		if toModule != fromModule {
			if !isContract(toRest) {
				return "another module is reachable only through its contract"
			}
			return ""
		}
		if isContract(fromRest) && !isContract(toRest) {
			return "a contract never exposes its module's internals"
		}
	case strings.HasPrefix(from, "edge"):
		if toIsModule && !isContract(toRest) {
			return "the edge reaches modules only through contracts"
		}
	}
	return ""
}

// splitModule splits "modules/finance/store" into ("finance", "store", true).
func splitModule(pkg string) (name, rest string, ok bool) {
	parts := strings.SplitN(pkg, "/", 3)
	if len(parts) < 2 || parts[0] != "modules" {
		return "", "", false
	}
	if len(parts) == 3 {
		rest = parts[2]
	}
	return parts[1], rest, true
}

// ModulePath reads the module path from root/go.mod.
func ModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(data)) {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("no module line in %s", filepath.Join(root, "go.mod"))
}
