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
		s, err := Open(context.Background(), uri, db)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = s.client.Database(db).Drop(context.Background())
			_ = s.Close(context.Background())
		})
		return s
	})
}
