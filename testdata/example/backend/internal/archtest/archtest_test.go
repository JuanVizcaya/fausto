package archtest

import (
	"slices"
	"strings"
	"testing"
)

// TestBoundaries checks this repository's backend.
func TestBoundaries(t *testing.T) {
	const root = "../.."
	modulePath, err := ModulePath(root)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := Check(root, modulePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// TestCheckFindsViolations runs Check on a fixture with one violation of
// each rule.
func TestCheckFindsViolations(t *testing.T) {
	const root = "testdata/bad"
	modulePath, err := ModulePath(root)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := Check(root, modulePath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"module b: missing module.go",
		"internal/modules/a/service/service.go imports example.com/bad/internal/modules/b/store",
		"internal/modules/a/contract/contract.go imports example.com/bad/internal/modules/a/domain",
		"internal/modules/a/domain/domain.go imports example.com/bad/internal/edge/api",
		"internal/edge/api/api.go imports example.com/bad/internal/modules/a:",
		"internal/platform/x/x.go imports example.com/bad/internal/modules/b/contract",
	}
	for _, w := range want {
		if !slices.ContainsFunc(violations, func(v string) bool { return strings.HasPrefix(v, w) }) {
			t.Errorf("missing violation %q", w)
		}
	}
	// Module b also lacks service, domain, store and migrations; the allowed
	// imports in the fixture add nothing.
	if len(violations) != len(want)+4 {
		t.Errorf("got %d violations, want %d:\n%s", len(violations), len(want)+4, strings.Join(violations, "\n"))
	}
}
