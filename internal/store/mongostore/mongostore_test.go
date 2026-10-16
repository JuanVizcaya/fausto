package mongostore

import (
	"context"
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
	renamed, err := d.AdoptLegacy(ctx, "habyta")
	if err != nil || len(renamed) != 1 {
		t.Fatalf("renamed = %v, %v", renamed, err)
	}
	if got, err := d.Store("habyta").Task(ctx, "old"); err != nil || got.Agent != "x" {
		t.Errorf("adopted task = %+v, %v", got, err)
	}
	if renamed, err := d.AdoptLegacy(ctx, "habyta"); err != nil || len(renamed) != 0 {
		t.Errorf("second adopt = %v, %v", renamed, err)
	}
}
