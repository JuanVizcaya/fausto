package agentdef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

func fixture() *spec.Spec {
	s := &spec.Spec{}
	s.System.Name = "habyta"
	s.System.Components = []spec.Component{
		{ID: "residents", Name: "Residents service", Kind: "service", Layer: "domain", Status: "active", Owner: "residents", Tech: []string{"go", "grpc"}},
		{ID: "api", Name: "API gateway", Kind: "gateway", Layer: "edge", Status: "active", Owner: "gateway"},
	}
	s.Principles.Principles = []spec.Principle{{ID: "service-owns-data", Title: "Each service owns its data"}}
	s.Agents = []spec.Agent{
		{ID: "residents", Name: "Residents agent", Kind: "agent", Scope: "Residents service: contract and database.", Owns: []string{"backend/internal/residents/"}, Rules: []string{"Never edit an applied migration."}, File: "system/agents/residents.yaml"},
		{ID: "orchestrator", Name: "Orchestrator", Kind: "human", Scope: "Owns the spec.", Owns: []string{"system/"}},
	}
	return s
}

func TestRender(t *testing.T) {
	s := fixture()
	got := string(Render(s, &s.Agents[0]))
	for _, want := range []string{
		"---\nname: residents\n",
		"description: 'Residents agent for habyta. Residents service: contract and database. Use it to work the fausto tasks assigned to agent `residents`.'",
		generatedBy + " from system/agents/residents.yaml",
		"- `backend/internal/residents/`",
		"- `residents` (Residents service): service in the domain layer, active; go, grpc.",
		"- Never edit an applied migration.",
		"- **Each service owns its data** (`service-owns-data`)",
		"fausto task pull residents",
		"fausto task done <id>",
		"fausto check",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("definition lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "`api`") {
		t.Error("definition lists a component the agent doesn't own")
	}
}

func TestSync(t *testing.T) {
	root := t.TempDir()
	s := fixture()

	files, err := Sync(root, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[Path("residents")] == nil {
		t.Fatalf("first sync = %v, want only the residents definition (humans get none)", keys(files))
	}
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	if files, _ := Sync(root, s); len(files) != 0 {
		t.Fatalf("sync after write = %v, want up to date", keys(files))
	}

	// A charter change regenerates; a removed charter deletes its generated
	// definition but leaves hand-written ones alone.
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	os.WriteFile(filepath.Join(dir, "old.md"), []byte("<!-- "+generatedBy+" from x -->"), 0o644)
	os.WriteFile(filepath.Join(dir, "mine.md"), []byte("hand-written"), 0o644)
	s.Agents[0].Rules = nil
	files, _ = Sync(root, s)
	if len(files) != 2 || files[Path("residents")] == nil {
		t.Fatalf("sync = %v, want residents rewritten and old removed", keys(files))
	}
	if data, ok := files[Dir+"/old.md"]; !ok || data != nil {
		t.Errorf("stale generated definition not removed")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
