package mongostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/storetest"
)

// TestContract runs against FAUSTO_TEST_MONGO_URI, in a throwaway database
// per subtest; it is skipped without one.
func TestContract(t *testing.T) {
	uri := os.Getenv("FAUSTO_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("FAUSTO_TEST_MONGO_URI not set")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		db := fmt.Sprintf("fausto_test_%d", time.Now().UnixNano())
		d, err := Open(context.Background(), uri, db)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = d.db.Drop(context.Background())
			_ = d.Close(context.Background())
		})
		return d.Store("example")
	})
	storetest.RunProjects(t, func(t *testing.T) store.Projects {
		db := fmt.Sprintf("fausto_test_%d", time.Now().UnixNano())
		d, err := Open(context.Background(), uri, db)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = d.db.Drop(context.Background())
			_ = d.Close(context.Background())
		})
		return d
	})
}

// TestAdoptLegacy moves the collections from before projects to a project.
func TestAdoptLegacy(t *testing.T) {
	uri := os.Getenv("FAUSTO_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("FAUSTO_TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	name := fmt.Sprintf("fausto_test_%d", time.Now().UnixNano())
	d, err := Open(ctx, uri, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.db.Drop(context.Background())
		_ = d.Close(context.Background())
	})
	if _, err := d.db.Collection("tasks").InsertOne(ctx, store.Task{ID: "old", Agent: "x", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	renamed, err := d.AdoptLegacy(ctx, "habbyte")
	if err != nil || len(renamed) != 1 {
		t.Fatalf("renamed = %v, %v", renamed, err)
	}
	if got, err := d.Store("habbyte").Task(ctx, "old"); err != nil || got.Agent != "x" {
		t.Errorf("adopted task = %+v, %v", got, err)
	}
	if renamed, err := d.AdoptLegacy(ctx, "habbyte"); err != nil || len(renamed) != 0 {
		t.Errorf("second adopt = %v, %v", renamed, err)
	}
}

// TestRenameProject moves a project's collections, record and tokens to a
// new id.
func TestRenameProject(t *testing.T) {
	uri := os.Getenv("FAUSTO_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("FAUSTO_TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	name := fmt.Sprintf("fausto_test_%d", time.Now().UnixNano())
	d, err := Open(ctx, uri, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.db.Drop(context.Background())
		_ = d.Close(context.Background())
	})
	if err := d.PutProject(ctx, &store.Project{ID: "old", Name: "old", Path: "/src/old"}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutProject(ctx, &store.Project{ID: "taken", Name: "Taken", Path: "/src/taken"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Store("old").CreateTasks(ctx, []store.Task{{ID: "t1", Agent: "x", Status: "open"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Store("old").AddSpecRevision(ctx, &store.SpecRevision{N: 1, Files: []store.SpecFile{{Path: "system/system.yaml", Content: "name: old\n"}}}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateToken(ctx, &store.Token{Hash: "h", Label: "fausto_ab", Project: "old", Agent: "x"}); err != nil {
		t.Fatal(err)
	}

	if _, err := d.RenameProject(ctx, "old", "taken"); !errors.Is(err, store.ErrExists) {
		t.Errorf("rename onto an existing project: %v", err)
	}
	if _, err := d.RenameProject(ctx, "missing", "new"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("rename a missing project: %v", err)
	}
	renamed, err := d.RenameProject(ctx, "old", "new")
	if err != nil || len(renamed) < 2 {
		t.Fatalf("renamed = %v, %v", renamed, err)
	}
	if p, err := d.Project(ctx, "new"); err != nil || p.Name != "new" || p.Path != "/src/old" {
		t.Errorf("new project = %+v, %v", p, err)
	}
	if _, err := d.Project(ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old project still there: %v", err)
	}
	if got, err := d.Store("new").Task(ctx, "t1"); err != nil || got.Agent != "x" {
		t.Errorf("renamed task = %+v, %v", got, err)
	}
	if r, err := d.Store("new").SpecRevision(ctx, 0); err != nil || r.N != 1 {
		t.Errorf("renamed spec = %+v, %v", r, err)
	}
	if tok, err := d.TokenByHash(ctx, "h"); err != nil || tok.Project != "new" {
		t.Errorf("token = %+v, %v", tok, err)
	}
}
