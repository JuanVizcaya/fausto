package main

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/server"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

// Import, then export into another clone, gives back the same files.
func TestSpecImportExport(t *testing.T) {
	uri := os.Getenv("FAUSTO_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("FAUSTO_TEST_MONGO_URI not set")
	}
	t.Setenv("FAUSTO_MONGO_URI", uri)
	t.Setenv("FAUSTO_MONGO_DB", fmt.Sprintf("fausto_test_%d", time.Now().UnixNano()))
	root := exampleRepo(t)
	if code := runProject([]string{"add", "example", "-path", root}, io.Discard, os.Stderr); code != 0 {
		t.Fatalf("project add: exit %d", code)
	}
	if code := runSpec([]string{"import", "-root", root, "-project", "example"}, io.Discard, os.Stderr); code != 0 {
		t.Fatalf("import: exit %d", code)
	}
	if code := runSpec([]string{"import", "-root", root, "-project", "example"}, io.Discard, io.Discard); code == 0 {
		t.Error("a second import without -force succeeded")
	}
	other := t.TempDir()
	if code := runSpec([]string{"export", "-root", other, "-project", "example"}, io.Discard, os.Stderr); code != 0 {
		t.Fatalf("export: exit %d", code)
	}
	want, _ := spec.ReadDir(root)
	got, _ := spec.ReadDir(other)
	if len(got) != len(want) || len(want) < 10 {
		t.Fatalf("exported %d files, imported %d", len(got), len(want))
	}
	for p, data := range want {
		if string(got[p]) != string(data) {
			t.Errorf("%s differs after the round trip", p)
		}
	}
}

// fausto spec pull writes the kept spec into an agent's clone, which git
// then doesn't see.
func TestSpecPull(t *testing.T) {
	ctx := context.Background()
	root := exampleRepo(t)
	reg := memstore.NewProjects()
	db := reg.Store("example")
	files, _ := spec.ReadDir(root)
	db.AddSpecRevision(ctx, &store.SpecRevision{N: 1, Files: store.SpecFiles(files)})
	secret, tok, _ := store.NewToken("example", "platform", "")
	reg.CreateToken(ctx, tok)
	ps := &proposal.Store{Src: &source.Local{Path: root, Branch: "main"}, DB: db}
	api := httptest.NewServer(server.Multi([]server.Project{{ID: "example",
		Handler: server.New(server.Options{Project: "example", Tokens: reg, Root: root, Proposals: ps})}}, reg))
	defer api.Close()

	// An agent's clone of a project that no longer keeps system/.
	agent := exampleRepo(t)
	gitIn(t, agent, "rm", "-rq", "system")
	gitIn(t, agent, "commit", "-qm", "the spec lives in fausto")
	c := &client.Client{URL: api.URL, Token: secret}
	if code, err := pullSpecWith(ctx, c, agent, io.Discard); code != 0 || err != nil {
		t.Fatalf("pull: %d, %v", code, err)
	}
	if data, err := os.ReadFile(filepath.Join(agent, "system", "system.yaml")); err != nil || !strings.Contains(string(data), "components:") {
		t.Errorf("system.yaml after pull: %v", err)
	}
	if st := gitIn(t, agent, "status", "--porcelain"); st != "" {
		t.Errorf("git sees the pulled spec:\n%s", st)
	}
	// Over a repository that keeps its own system/, it refuses.
	if code, _ := pullSpecWith(ctx, c, root, io.Discard); code == 0 {
		t.Error("pulled over the repository's own system/")
	}
}
