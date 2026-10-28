package check

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// withProjectChecks adds project checks to the fixture, and a principle
// that names a built-in check, a project check and review.
func withProjectChecks(t *testing.T) map[string]string {
	t.Helper()
	f := fixture(t)
	f["system/checks.yaml"] = `
checks:
  - {id: passes, title: Passes, run: "true"}
  - {id: fails, title: Fails, run: "echo boom; exit 3"}
  - {id: in-dir, title: Runs in its dir, run: "test -f marker.txt", dir: backend}
  - {id: slow, title: Slow, run: "sleep 5", timeout: 1s}
`
	f["system/principles.yaml"] = `
principles:
  - {id: layered, title: Layers, rationale: Because., checks: [layers, passes, review]}
`
	f["backend/marker.txt"] = "x\n"
	return f
}

func TestProjectChecksSkippedWithoutCommands(t *testing.T) {
	root := write(t, withProjectChecks(t))
	report, err := Run(root, []string{"schema", "passes", "fails"})
	if err != nil {
		t.Fatal(err)
	}
	if r := result(t, report, "schema"); r.Status != Pass {
		t.Fatalf("schema: %s %+v", r.Status, r.Findings)
	}
	r := result(t, report, "passes")
	if r.Status != Skipped || r.Command != "true" || !slices.Equal(r.Principles, []string{"layered"}) {
		t.Fatalf("passes = %+v", r)
	}
	if result(t, report, "fails").Status != Skipped || !report.OK {
		t.Fatalf("a skipped project check must not fail the report: %+v", report)
	}
}

func TestProjectChecksRunWithCommands(t *testing.T) {
	root := write(t, withProjectChecks(t))
	var out strings.Builder
	report, err := RunCommands(root, []string{"passes", "fails", "in-dir", "slow"}, spec.Options{}, Commands{Ctx: context.Background(), Output: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 4 || report.OK {
		t.Fatalf("report = %+v", report)
	}
	for name, want := range map[string]Status{"passes": Pass, "fails": Fail, "in-dir": Pass, "slow": Fail} {
		if got := result(t, report, name).Status; got != want {
			t.Errorf("%s: status %s, want %s (%+v)", name, got, want, result(t, report, name).Findings)
		}
	}
	if f := result(t, report, "fails").Findings; len(f) != 1 || !strings.Contains(f[0].Message, "boom") {
		t.Errorf("fails findings = %+v, want the command's output", f)
	}
	if f := result(t, report, "slow").Findings; len(f) != 1 || !strings.Contains(f[0].Message, "longer than 1s") {
		t.Errorf("slow findings = %+v", f)
	}
	if !strings.Contains(out.String(), "==> fails: echo boom; exit 3\nboom\n") {
		t.Errorf("output = %q", out.String())
	}
}

func TestUnknownProjectCheck(t *testing.T) {
	root := write(t, withProjectChecks(t))
	if _, err := Run(root, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "passes") {
		t.Fatalf("err = %v, want the available checks, project checks included", err)
	}
}

func TestProjectCheckViolations(t *testing.T) {
	f := withProjectChecks(t)
	f["system/checks.yaml"] = `
checks:
  - {id: passes, title: Passes, run: "true"}
  - {id: passes, title: Again, run: "true"}
  - {id: layers, title: Shadows a built-in, run: "true"}
  - {id: outside, title: Outside, run: "true", dir: ../elsewhere}
`
	f["system/principles.yaml"] = `
principles:
  - {id: layered, title: Layers, rationale: Because., checks: [layers, missing]}
`
	root := write(t, f)
	report, err := Run(root, []string{"schema"})
	if err != nil {
		t.Fatal(err)
	}
	r := result(t, report, "schema")
	for _, want := range []string{
		`duplicate check "passes"`,
		`check "layers" has the name of a built-in check`,
		`check "outside": dir "../elsewhere" must be inside the repository`,
		`principle "layered" names unknown check "missing"`,
	} {
		if !slices.ContainsFunc(r.Findings, func(f Finding) bool { return strings.Contains(f.Message, want) }) {
			t.Errorf("no finding contains %q; got %+v", want, r.Findings)
		}
	}
}

func TestAuditorCharterPasses(t *testing.T) {
	f := fixture(t)
	f["system/agents/aud.yaml"] = "id: aud\nname: Auditor\nkind: auditor\nscope: Reviews pull requests against the spec.\n"
	report := runFixture(t, f)
	for _, r := range report.Results {
		if r.Status != Pass {
			t.Errorf("%s: status %s, findings %+v", r.Check, r.Status, r.Findings)
		}
	}
}
