package main

import (
	"os"
	"slices"
	"testing"

	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// TestEveryModuleIsWired fails when a folder under internal/modules is
// missing from modules(), or a module fails to register.
func TestEveryModuleIsWired(t *testing.T) {
	entries, err := os.ReadDir("../../internal/modules")
	if err != nil {
		t.Fatal(err)
	}
	var folders []string
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
		}
	}
	registry := module.NewRegistry()
	for _, m := range modules() {
		if err := registry.Add(m); err != nil {
			t.Fatal(err)
		}
	}
	wired := registry.Names()
	slices.Sort(wired)
	if !slices.Equal(wired, folders) {
		t.Fatalf("wired modules %v, want the folders %v", wired, folders)
	}
}
