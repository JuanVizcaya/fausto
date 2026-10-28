package edit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/spec"
	"gopkg.in/yaml.v3"
)

const repoRoot = "../../testdata/example"

// copyRepo copies the real system/ directory into a temporary root.
func copyRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir(filepath.Join(repoRoot, spec.Dir), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lineDiff returns lines only in a and lines only in b (as multisets).
func lineDiff(a, b string) (removed, added []string) {
	count := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		count[l]++
	}
	for _, l := range strings.Split(b, "\n") {
		count[l]--
	}
	for _, l := range strings.Split(a, "\n") {
		if count[l] > 0 {
			removed = append(removed, l)
			count[l]--
		}
	}
	for l, n := range count {
		for ; n < 0; n++ {
			added = append(added, l)
		}
	}
	return removed, added
}

func applyOne(t *testing.T, root string, c Change) Files {
	t.Helper()
	files, err := Apply(root, []Change{c}, "2026-10-06")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func loadSpec(t *testing.T, root string) *spec.Spec {
	t.Helper()
	s, err := spec.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUnchangedItemsRoundTripExactly(t *testing.T) {
	root := copyRepo(t)
	s := loadSpec(t, root)
	var changes []Change
	for _, c := range s.System.Components {
		changes = append(changes, Change{Kind: "component", ID: c.ID, Value: mustJSON(t, c)})
	}
	for _, l := range s.System.Layers {
		changes = append(changes, Change{Kind: "layer", ID: l.ID, Value: mustJSON(t, l)})
	}
	for _, tech := range s.Tech.Technologies {
		changes = append(changes, Change{Kind: "technology", ID: tech.ID, Value: mustJSON(t, tech)})
	}
	for _, a := range s.Agents {
		changes = append(changes, Change{Kind: "agent", ID: a.ID, Value: mustJSON(t, a)})
	}
	for _, d := range s.Decisions {
		changes = append(changes, Change{Kind: "decision", ID: d.ID, Value: mustJSON(t, d), Body: d.Body})
	}
	for _, m := range s.Roadmap.Milestones {
		changes = append(changes, Change{Kind: "milestone", ID: m.ID, Value: mustJSON(t, m)})
	}
	for _, r := range s.Requirements.Requirements {
		changes = append(changes, Change{Kind: "requirement", ID: r.ID, Value: mustJSON(t, r)})
	}
	for _, r := range s.Risks.Risks {
		changes = append(changes, Change{Kind: "risk", ID: r.ID, Value: mustJSON(t, r)})
	}
	for _, e := range s.Events.Events {
		changes = append(changes, Change{Kind: "event", ID: e.ID, Value: mustJSON(t, e)})
	}
	if len(s.Roadmap.Milestones) == 0 || len(s.Requirements.Requirements) == 0 || len(s.Risks.Risks) == 0 {
		t.Fatal("no milestones loaded")
	}
	files, err := Apply(root, changes, "2026-10-06")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range files {
		if want := read(t, root, path); string(data) != want {
			removed, added := lineDiff(want, string(data))
			t.Errorf("%s changed: removed %q, added %q", path, removed, added)
		}
	}
}

func TestEditField(t *testing.T) {
	root := copyRepo(t)
	web := *loadSpec(t, root).Component("web")
	web.Status = "active"
	web.Tech = append(web.Tech, "go")
	files := applyOne(t, root, Change{Kind: "component", ID: "web", Value: mustJSON(t, web)})
	removed, added := lineDiff(read(t, root, "system/system.yaml"), string(files["system/system.yaml"]))
	if strings.Join(removed, "|") != "    status: planned|    tech: [react]" ||
		len(added) != 2 || !strings.Contains(strings.Join(added, "|"), "    status: active") ||
		!strings.Contains(strings.Join(added, "|"), "    tech: [react, go]") {
		t.Errorf("removed %q, added %q", removed, added)
	}
}

func TestAddAndDeleteComponent(t *testing.T) {
	root := copyRepo(t)
	billing := json.RawMessage(`{"id":"billing","name":"Billing service","kind":"service","layer":"domain",
		"status":"planned","owner":"platform","tech":["go","grpc"],"description":"","runtime":{"cmd":""}}`)
	files, err := Apply(root, []Change{
		{Kind: "component", Value: billing},
		{Kind: "component", ID: "web", Value: json.RawMessage("null")},
	}, "2026-10-06")
	if err != nil {
		t.Fatal(err)
	}
	got := string(files["system/system.yaml"])
	want := "\n  - id: billing\n    name: Billing service\n    kind: service\n    layer: domain\n    status: planned\n    owner: platform\n    tech: [go, grpc]\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("billing not appended as expected:\n%s", got[len(got)-300:])
	}
	if strings.Contains(got, "id: web") || strings.Contains(got, "\n\n\n") {
		t.Errorf("web not cleanly removed:\n%s", got)
	}
	// The result is still a valid spec.
	if err := os.WriteFile(filepath.Join(root, "system/system.yaml"), files["system/system.yaml"], 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadSpec(t, root)
	if s.Component("billing") == nil || s.Component("web") != nil {
		t.Errorf("components after edit: %+v", s.System.Components)
	}
}

func TestAppendUnseparatedList(t *testing.T) {
	root := copyRepo(t)
	files := applyOne(t, root, Change{Kind: "layer", Value: json.RawMessage(`{"id":"jobs","may_depend_on":["platform"]}`)})
	got := string(files["system/system.yaml"])
	// Right after the last layer, with no blank line, as the list is written.
	want := "]\n  - id: jobs\n    may_depend_on: [platform]\n\ncomponents:"
	if !strings.Contains(got, want) {
		t.Errorf("layer not appended after the last layer:\n%s", got[:900])
	}
}

func TestDuplicateID(t *testing.T) {
	root := copyRepo(t)
	_, err := Apply(root, []Change{{Kind: "technology", Value: json.RawMessage(`{"id":"go","name":"Go","category":"language","status":"adopt"}`)}}, "2026-10-06")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want already exists", err)
	}
}

func TestNewDecision(t *testing.T) {
	root := copyRepo(t)
	files := applyOne(t, root, Change{
		Kind:  "decision",
		Value: json.RawMessage(`{"title":"Use NATS for events","status":"proposed","affects":["residents"]}`),
		Body:  "## Context\n\nWe need events.\n",
	})
	// The next id after the decisions in the copied repository.
	entries, err := os.ReadDir(filepath.Join(root, "system", "decisions"))
	if err != nil {
		t.Fatal(err)
	}
	next := 0
	for _, e := range entries {
		if n, err := strconv.Atoi(strings.SplitN(e.Name(), "-", 2)[0]); err == nil && n > next {
			next = n
		}
	}
	id := fmt.Sprintf("%04d", next+1)
	data, ok := files["system/decisions/"+id+"-use-nats-for-events.md"]
	if !ok {
		t.Fatalf("files = %v, want decision %s", keys(files), id)
	}
	want := "---\nid: \"" + id + "\"\ntitle: Use NATS for events\nstatus: proposed\naffects: [residents]\ndate: 2026-10-06\n---\n\n## Context\n\nWe need events.\n"
	if string(data) != want {
		t.Errorf("got:\n%s\nwant:\n%s", data, want)
	}
}

func TestEditDecisionKeepsID(t *testing.T) {
	root := copyRepo(t)
	d := loadSpec(t, root).Decisions[0]
	d.Status = "superseded"
	d.ID = "9999" // ignored
	path := "system/decisions/0001-postgres-behind-store-interfaces.md"
	files := applyOne(t, root, Change{Kind: "decision", ID: "0001", Value: mustJSON(t, d), Body: d.Body})
	removed, added := lineDiff(read(t, root, path), string(files[path]))
	if strings.Join(removed, "|") != "status: accepted" || strings.Join(added, "|") != "status: superseded" {
		t.Errorf("removed %q, added %q", removed, added)
	}
}

func TestRenameAgent(t *testing.T) {
	root := copyRepo(t)
	a := loadSpec(t, root).Agents[0]
	old := a.ID
	a.ID = "renamed"
	files := applyOne(t, root, Change{Kind: "agent", ID: old, Value: mustJSON(t, a)})
	if data, ok := files["system/agents/"+old+".yaml"]; !ok || data != nil {
		t.Errorf("old charter not deleted")
	}
	if !strings.HasPrefix(string(files["system/agents/renamed.yaml"]), "id: renamed\n") {
		t.Errorf("new charter = %q", files["system/agents/renamed.yaml"])
	}
}

func keys(f Files) []string {
	var out []string
	for k := range f {
		out = append(out, k)
	}
	return out
}

func TestReorderLayers(t *testing.T) {
	root := copyRepo(t)
	before := loadSpec(t, root)
	var ids []string
	for _, l := range before.System.Layers {
		ids = append(ids, l.ID)
	}
	if len(ids) < 3 {
		t.Fatalf("need three layers, have %v", ids)
	}
	// Move the last layer to the top; "gone" names no layer and is ignored.
	order := append([]string{ids[len(ids)-1], "gone"}, ids[:len(ids)-1]...)
	files := applyOne(t, root, Change{Kind: "layer", Order: order})
	path := spec.Dir + "/system.yaml"
	original := read(t, root, path)
	if err := os.WriteFile(filepath.Join(root, path), files[path], 0o644); err != nil {
		t.Fatal(err)
	}
	after := loadSpec(t, root)
	var got []string
	for _, l := range after.System.Layers {
		got = append(got, l.ID)
	}
	want := append([]string{ids[len(ids)-1]}, ids[:len(ids)-1]...)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("layers = %v, want %v", got, want)
	}
	// Only the order changes: the same lines, and the rest of the spec intact.
	removed, added := lineDiff(original, string(files[path]))
	if len(removed) > 0 || len(added) > 0 {
		t.Errorf("reordering changed lines: -%q +%q", removed, added)
	}
	if len(after.System.Components) != len(before.System.Components) {
		t.Errorf("components = %d, want %d", len(after.System.Components), len(before.System.Components))
	}
}

func TestReorderKeepsUnnamedItemsInPlace(t *testing.T) {
	data := []byte("x: 1\nitems:\n  # first\n  - id: a\n    n: 1\n\n  - id: b\n  - id: c\n    n: 3\n\nafter: true\n")
	out, err := reorderItems(data, "items", "id", []string{"c", "a"})
	if err != nil {
		t.Fatal(err)
	}
	want := "x: 1\nitems:\n  - id: c\n    n: 3\n\n  - id: b\n  # first\n  - id: a\n    n: 1\n\nafter: true\n"
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestFirstItemCreatesOptionalFile(t *testing.T) {
	root := copyRepo(t)
	if _, err := os.Stat(filepath.Join(root, "system/checks.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the example already has checks.yaml: %v", err)
	}
	files, err := Apply(root, []Change{
		{Kind: "check", Value: json.RawMessage(`{"id":"go-tests","title":"Go tests","run":"go test ./..."}`)},
	}, "2026-10-28")
	if err != nil {
		t.Fatal(err)
	}
	var got spec.Checks
	if err := yaml.Unmarshal(files["system/checks.yaml"], &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Checks) != 1 || got.Checks[0].ID != "go-tests" || got.Checks[0].Run != "go test ./..." {
		t.Fatalf("checks.yaml = %q", files["system/checks.yaml"])
	}
	// Deleting from a file that doesn't exist is still an error.
	if _, err := Apply(root, []Change{{Kind: "check", ID: "go-tests", Value: json.RawMessage("null")}}, "2026-10-28"); err == nil {
		t.Fatal("deleting from a missing checks.yaml succeeded")
	}
}
