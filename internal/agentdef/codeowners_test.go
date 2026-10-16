package agentdef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

func TestRenderCodeOwners(t *testing.T) {
	s := fixture()
	if got := RenderCodeOwners(s); got != nil {
		t.Fatalf("without reviewers = %q, want none", got)
	}

	s.Agents = []spec.Agent{
		{ID: "platform", Kind: "agent", Owns: []string{"backend/", "compose.yaml"}},
		{ID: "residents", Kind: "agent", Owns: []string{"backend/internal/residents/"}, Reviewers: []string{"@acme/residents"}},
		{ID: "orchestrator", Kind: "human", Owns: []string{"system/", "CLAUDE.md"}, Reviewers: []string{"@ana"}},
		{ID: "ops", Kind: "human", Owns: []string{"deploy/"}, Reviewers: []string{"@ana", "@bo"}},
	}
	got := string(RenderCodeOwners(s))
	// Directories shortest first, then files, so GitHub's last match is
	// fausto's most specific entry; agents without reviewers go to the
	// humans'.
	want := "/deploy/ @ana @bo\n" +
		"/system/ @ana\n" +
		"/backend/ @ana @bo\n" +
		"/backend/internal/residents/ @acme/residents\n" +
		"/CLAUDE.md @ana\n" +
		"/compose.yaml @ana @bo\n"
	if !strings.HasPrefix(got, "# "+generatedBy) || !strings.HasSuffix(got, "\n\n"+want) {
		t.Errorf("CODEOWNERS =\n%s\nwant the header, then\n%s", got, want)
	}
}

func TestSyncCodeOwners(t *testing.T) {
	root := t.TempDir()
	s := fixture()
	co := filepath.Join(root, filepath.FromSlash(CodeOwners))
	os.MkdirAll(filepath.Dir(co), 0o755)

	// A hand-written CODEOWNERS is left alone while no charter names
	// reviewers, and replaced once one does.
	os.WriteFile(co, []byte("* @someone\n"), 0o644)
	if files, _ := Sync(root, s); files[CodeOwners] != nil || hasKey(files, CodeOwners) {
		t.Fatalf("sync touched a hand-written CODEOWNERS: %v", keys(files))
	}
	s.Agents[1].Reviewers = []string{"@ana"}
	files, err := Sync(root, s)
	if err != nil || files[CodeOwners] == nil {
		t.Fatalf("sync = %v, %v; want CODEOWNERS written", keys(files), err)
	}
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}

	// Removing the last reviewer removes the generated file.
	s.Agents[1].Reviewers = nil
	files, _ = Sync(root, s)
	if data, ok := files[CodeOwners]; !ok || data != nil {
		t.Fatalf("sync = %v, want CODEOWNERS removed", keys(files))
	}
}

func hasKey(m map[string][]byte, k string) bool {
	_, ok := m[k]
	return ok
}
