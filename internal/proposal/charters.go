package proposal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// Agent charters live in fausto's database once a project's are imported
// (see `fausto charters import`); until then, in the repository. Fausto
// lays the charters it keeps over system/agents/ wherever it reads a spec,
// so edits, checks and task plans treat them like any spec file; a
// proposal's charter changes go to the database when it is applied, never
// into a commit.

// charters returns the charters fausto keeps for the project, by agent id,
// or nil when the project keeps its own.
func (s *Store) charters(ctx context.Context) (map[string][]byte, error) {
	list, err := s.DB.Charters(ctx)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	out := make(map[string][]byte, len(list))
	for _, c := range list {
		out[c.ID] = []byte(c.YAML)
	}
	return out, nil
}

// SpecOptions are the options to read the project's spec with: the
// charters fausto keeps, if it keeps them.
func (s *Store) SpecOptions(ctx context.Context) (spec.Options, error) {
	ch, err := s.charters(ctx)
	return spec.Options{Charters: ch}, err
}

// overlay returns the files that make the tree at rev hold the charters
// fausto keeps instead of its own; nil when fausto keeps none.
func (s *Store) overlay(rev string, ch map[string][]byte) (edit.Files, error) {
	if ch == nil {
		return nil, nil
	}
	names, err := s.git("", nil, nil, "ls-tree", "--name-only", rev, "--", spec.Dir+"/agents/")
	if err != nil {
		return nil, err
	}
	files := edit.Files{}
	for _, n := range strings.Fields(names) {
		if strings.HasSuffix(n, ".yaml") {
			files[n] = nil
		}
	}
	for id, data := range ch {
		files[spec.AgentFile(id)] = data
	}
	return files, nil
}

// dirCharters reads the charters in a materialized tree, by agent id.
func dirCharters(dir string) (map[string][]byte, error) {
	paths, err := filepath.Glob(filepath.Join(dir, spec.Dir, "agents", "*.yaml"))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(filepath.Base(p), ".yaml")] = data
	}
	return out, nil
}

// charterID returns the agent id of a charter file path, or "".
func charterID(path string) string {
	name, ok := strings.CutPrefix(path, spec.Dir+"/agents/")
	if !ok || strings.Contains(name, "/") || !strings.HasSuffix(name, ".yaml") {
		return ""
	}
	return strings.TrimSuffix(name, ".yaml")
}

// hashCharters fingerprints charters, by agent id, to notice later changes.
func hashCharters(ch map[string][]byte) map[string]string {
	if ch == nil {
		return nil
	}
	out := make(map[string]string, len(ch))
	for id, data := range ch {
		sum := sha256.Sum256(data)
		out[id] = hex.EncodeToString(sum[:8])
	}
	return out
}

// putCharters writes a proposal's charter changes ("" removes one).
func (s *Store) putCharters(ctx context.Context, pending map[string]string) error {
	if len(pending) == 0 {
		return nil
	}
	var put []store.Charter
	var del []string
	for _, id := range slices.Sorted(maps.Keys(pending)) {
		if pending[id] == "" {
			del = append(del, id)
		} else {
			put = append(put, store.Charter{ID: id, YAML: pending[id]})
		}
	}
	return s.DB.PutCharters(ctx, put, del)
}

// charterChanges describes a proposal's pending charter changes as item
// changes, for proposals whose charter changes are in no commit.
func charterChanges(p *store.Proposal) []ItemChange {
	var out []ItemChange
	for _, id := range slices.Sorted(maps.Keys(p.PendingCharters)) {
		c := ItemChange{Kind: "agent", ID: id, Action: "modified", Fields: []string{"charter"}}
		switch {
		case p.PendingCharters[id] == "":
			c.Action = "removed"
		case p.BaseCharters[id] == "":
			c.Action = "added"
		}
		out = append(out, c)
	}
	return out
}
