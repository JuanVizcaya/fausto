package memstore

import (
	"testing"

	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/storetest"
)

func TestContract(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return New() })
}

func TestProjects(t *testing.T) {
	storetest.RunProjects(t, func(*testing.T) store.Projects { return NewProjects() })
}
