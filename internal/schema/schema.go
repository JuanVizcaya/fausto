// Package schema embeds the JSON Schemas that describe the spec format, one
// per kind of spec file (system.schema.json for system.yaml, and so on).
// They belong to fausto, not to the project it checks.
package schema

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed *.schema.json
var files embed.FS

// Read returns the schema named name, such as "system.schema.json".
func Read(name string) ([]byte, error) { return files.ReadFile(name) }

// All returns every schema by its name without ".schema.json".
func All() map[string][]byte {
	out := map[string][]byte{}
	entries, _ := fs.ReadDir(files, ".")
	for _, e := range entries {
		data, err := files.ReadFile(e.Name())
		if err == nil {
			out[strings.TrimSuffix(e.Name(), ".schema.json")] = data
		}
	}
	return out
}
