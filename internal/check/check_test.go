package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/agentdef"
	"github.com/JuanVizcaya/fausto/internal/spec"
)

// repoRoot is a snapshot of a real project (Habyta), relative to this package.
const repoRoot = "../../testdata/example"

// fixture is a minimal valid repository: a gateway calling one service over
// gRPC, the service's database, and a shared library.
func fixture(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{
		"system/system.yaml": `
name: test
layers:
  - {id: edge, may_depend_on: [domain, platform]}
  - {id: domain, may_depend_on: [data, platform]}
  - {id: data, may_depend_on: []}
  - {id: platform, may_depend_on: []}
components:
  - id: api
    name: API
    kind: gateway
    layer: edge
    status: active
    owner: gw
    tech: [go]
    paths: [backend/cmd/api, backend/internal/api]
    runtime: {cmd: backend/cmd/api, compose: api}
    depends_on:
      - {target: residents, protocol: grpc}
      - {target: platform, protocol: library}
  - id: residents
    name: Residents
    kind: service
    layer: domain
    status: active
    owner: res
    paths: [backend/cmd/residents, backend/internal/residents]
    runtime: {cmd: backend/cmd/residents, compose: residents}
    contracts:
      - {proto: proto/habyta/residents/v1, generated: backend/gen/habyta/residents/v1}
    depends_on:
      - {target: residents-db, protocol: sql}
      - {target: platform, protocol: library}
  - id: residents-db
    name: Residents DB
    kind: database
    layer: data
    status: active
    owner: res
    runtime: {compose: residents-db}
  - id: platform
    name: Platform
    kind: library
    layer: platform
    status: active
    owner: plat
    paths: [backend/internal/platform]
`,
		"system/tech.yaml": `
technologies:
  - {id: go, name: Go, category: language, status: adopt}
`,
		"system/principles.yaml": `
principles:
  - {id: layered, title: Layers, rationale: Because., checks: [layers]}
`,
		"system/agents/gw.yaml": `
id: gw
name: Gateway
kind: agent
scope: Gateway.
owns: [backend/cmd/api/, backend/internal/api/]
`,
		"system/agents/res.yaml": `
id: res
name: Residents
kind: agent
scope: Residents.
owns: [backend/cmd/residents/, backend/internal/residents/, backend/gen/habyta/residents/, proto/habyta/residents/]
`,
		"system/agents/plat.yaml": `
id: plat
name: Platform
kind: agent
scope: Platform.
owns: [backend/, compose.yaml]
`,
		"system/agents/human.yaml": `
id: human
name: Orchestrator
kind: human
scope: Spec.
owns: [system/, .claude/]
`,
		"system/decisions/0001-test.md": "---\nid: \"0001\"\ntitle: Test\nstatus: accepted\ndate: 2026-10-05\naffects: [residents]\n---\n\nBody.\n",
		"compose.yaml": `
x-common: &common
  read_only: true
services:
  api:
    <<: *common
    environment:
      RESIDENTS_ADDR: "dns:///residents:9090"
  residents:
    <<: *common
    environment:
      DATABASE_URL: "postgres://u:p@residents-db:5432/residents"
  residents-db:
    image: postgres:18
  test:
    profiles: [test]
    environment:
      TEST_DATABASE_URL: "postgres://u:p@residents-db:5432/residents_test"
`,
		"backend/go.mod":                                  "module example.com/backend\n\ngo 1.27.0\n",
		"backend/cmd/api/main.go":                         goFile("main", "example.com/backend/internal/api", "example.com/backend/internal/platform/service"),
		"backend/cmd/residents/main.go":                   goFile("main", "example.com/backend/internal/residents"),
		"backend/internal/api/api.go":                     goFile("api", "example.com/backend/gen/habyta/residents/v1", "net/http"),
		"backend/internal/residents/service.go":           goFile("residents", "example.com/backend/gen/habyta/residents/v1", "example.com/backend/internal/platform/service"),
		"backend/internal/platform/service/service.go":    goFile("service", "context"),
		"backend/gen/habyta/residents/v1/residents.pb.go": goFile("residentsv1"),
		"proto/habyta/residents/v1/residents.proto":       "syntax = \"proto3\";\n\npackage habyta.residents.v1;\n",
	}
	return files
}

func goFile(pkg string, imports ...string) string {
	var b strings.Builder
	b.WriteString("package " + pkg + "\n\n")
	for _, imp := range imports {
		b.WriteString("import _ \"" + imp + "\"\n")
	}
	return b.String()
}

func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runFixture writes files, generates the agent definitions their charters
// call for, and runs every check.
func runFixture(t *testing.T, files map[string]string) *Report {
	t.Helper()
	root := write(t, files)
	if s, err := spec.Load(root); err == nil {
		defs, err := agentdef.Sync(root, s)
		if err == nil {
			err = agentdef.Write(root, defs)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := Run(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func result(t *testing.T, report *Report, name string) Result {
	t.Helper()
	for _, r := range report.Results {
		if r.Check == name {
			return r
		}
	}
	t.Fatalf("no result for check %q", name)
	return Result{}
}

func TestFixturePasses(t *testing.T) {
	report := runFixture(t, fixture(t))
	for _, r := range report.Results {
		if r.Status != Pass {
			t.Errorf("%s: status %s, findings %+v", r.Check, r.Status, r.Findings)
		}
	}
	if !report.OK {
		t.Error("report not OK")
	}
	if got := result(t, report, "layers").Principles; len(got) != 1 || got[0] != "layered" {
		t.Errorf("layers principles = %v, want [layered]", got)
	}
}

// The spec itself must always be valid and the agent definitions current.
// Whether the code matches it can lag while agents work their tasks; CI's
// `fausto check` enforces that.
func TestRepositorySpecIsValid(t *testing.T) {
	report, err := Run(repoRoot, []string{"schema", "agents"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range report.Results {
		if r.Status != Pass {
			t.Errorf("%s: status %s, findings %+v", r.Check, r.Status, r.Findings)
		}
	}
}

func TestStaleAgentDefinition(t *testing.T) {
	root := write(t, fixture(t))
	s, err := spec.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	defs, _ := agentdef.Sync(root, s)
	if err := agentdef.Write(root, defs); err != nil {
		t.Fatal(err)
	}
	var some string
	for p := range defs {
		some = p
		break
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(some)), []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Run(root, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	r := result(t, report, "agents")
	if r.Status != Fail || len(r.Findings) != 1 || r.Findings[0].Path != some || !strings.Contains(r.Findings[0].Message, "out of date") {
		t.Errorf("agents = %+v", r)
	}
}

func TestViolations(t *testing.T) {
	tests := []struct {
		name   string
		check  string
		mutate func(files map[string]string)
		want   string // substring of a finding
	}{
		{
			name:  "domain service imports gateway code",
			check: "layers",
			mutate: func(f map[string]string) {
				f["backend/internal/residents/bad.go"] = goFile("residents", "example.com/backend/internal/api")
			},
			want: `imports internal code of gateway "api"`,
		},
		{
			name:  "contract imported without grpc dependency",
			check: "layers",
			mutate: func(f map[string]string) {
				f["system/system.yaml"] = strings.Replace(f["system/system.yaml"], "      - {target: residents, protocol: grpc}\n", "", 1)
			},
			want: `without declaring a grpc dependency on "residents"`,
		},
		{
			name:  "library imported without dependency",
			check: "layers",
			mutate: func(f map[string]string) {
				f["system/system.yaml"] = strings.Replace(f["system/system.yaml"], "      - {target: platform, protocol: library}\n", "", 1)
			},
			want: `without declaring a library dependency`,
		},
		{
			name:  "undeclared compose service",
			check: "services",
			mutate: func(f map[string]string) {
				f["compose.yaml"] += "  billing:\n    image: billing\n"
			},
			want: `compose service "billing" is not declared`,
		},
		{
			name:  "undeclared command",
			check: "services",
			mutate: func(f map[string]string) {
				f["backend/cmd/billing/main.go"] = goFile("main")
			},
			want: "command backend/cmd/billing is not declared",
		},
		{
			name:  "another service connects to the database",
			check: "db-ownership",
			mutate: func(f map[string]string) {
				f["compose.yaml"] = strings.Replace(f["compose.yaml"],
					`RESIDENTS_ADDR: "dns:///residents:9090"`,
					`RESIDENTS_ADDR: "dns:///residents:9090"
      DATABASE_URL: "postgres://u:p@residents-db:5432/residents"`, 1)
			},
			want: `connects to database "residents-db" without declaring a sql dependency`,
		},
		{
			name:  "undeclared proto package",
			check: "contracts",
			mutate: func(f map[string]string) {
				f["proto/habyta/billing/v1/billing.proto"] = "syntax = \"proto3\";\npackage habyta.billing.v1;\n"
			},
			want: "proto package proto/habyta/billing/v1 is not declared",
		},
		{
			name:  "proto package does not match directory",
			check: "contracts",
			mutate: func(f map[string]string) {
				f["proto/habyta/residents/v1/residents.proto"] = "syntax = \"proto3\";\npackage habyta.people.v1;\n"
			},
			want: `does not match its directory (want "habyta.residents.v1")`,
		},
		{
			name:  "file without owner",
			check: "ownership",
			mutate: func(f map[string]string) {
				f["notes.txt"] = "hi"
			},
			want: "no agent owns this file",
		},
		{
			name:  "file owned by the wrong agent",
			check: "ownership",
			mutate: func(f map[string]string) {
				f["system/agents/gw.yaml"] = strings.Replace(f["system/agents/gw.yaml"], "backend/internal/api/]", "backend/internal/api/, backend/internal/residents/service.go]", 1)
			},
			want: `owned by agent "gw", but component "residents" is owned by "res"`,
		},
		{
			name:  "milestone with unknown component",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/roadmap.yaml"] = "milestones:\n  - {id: m1, title: M1, goal: G, status: planned, components: [billing]}\n"
			},
			want: `milestone "m1" lists unknown component "billing"`,
		},
		{
			name:  "milestone dependency cycle",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/roadmap.yaml"] = "milestones:\n" +
					"  - {id: a, title: A, goal: G, status: planned, depends_on: [b]}\n" +
					"  - {id: b, title: B, goal: G, status: planned, depends_on: [a]}\n"
			},
			want: "cycle: a → b → a",
		},
		{
			name:  "done milestone with open criterion",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/roadmap.yaml"] = "milestones:\n  - id: m1\n    title: M1\n    goal: G\n    status: done\n" +
					"    exit_criteria:\n      - {text: Shipped, done: true}\n      - {text: Documented}\n"
			},
			want: `milestone "m1" is done but exit criterion "Documented" is not`,
		},
		{
			name:  "bad milestone target",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/roadmap.yaml"] = "milestones:\n  - {id: m1, title: M1, goal: G, status: planned, target: next-year}\n"
			},
			want: "target",
		},
		{
			name:  "unknown runtime rule",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/requirements.yaml"] = "requirements:\n  - {id: r1, title: R, category: security, statement: S, verified_by: runtime/tls}\n"
			},
			want: `unknown runtime rule "tls"`,
		},
		{
			name:  "requirement for unknown component",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/requirements.yaml"] = "requirements:\n  - {id: r1, title: R, category: security, statement: S, applies_to: {components: [billing]}}\n"
			},
			want: `requirement "r1" applies to unknown component "billing"`,
		},
		{
			name:  "severe risk without mitigation",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/risks.yaml"] = "risks:\n  - {id: r1, title: R, description: D, category: security, likelihood: high, impact: medium, status: open, owner: gw}\n"
			},
			want: `risk "r1" has severity 6 (high likelihood, medium impact) and needs a mitigation`,
		},
		{
			name:  "risk with unknown references",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/risks.yaml"] = "risks:\n  - {id: r1, title: R, description: D, category: security, likelihood: low, impact: low, status: open, owner: nobody, milestones: [m9]}\n"
			},
			want: `risk "r1" is owned by unknown agent "nobody"`,
		},
		{
			name:  "unknown owner",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/system.yaml"] = strings.Replace(f["system/system.yaml"], "owner: plat", "owner: nobody", 1)
			},
			want: `owner "nobody" is not an agent`,
		},
		{
			name:  "missing required field",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/tech.yaml"] = "technologies:\n  - {id: go, name: Go, category: language}\n"
			},
			want: "status",
		},
		{
			name:  "layer rule broken by declared dependency",
			check: "schema",
			mutate: func(f map[string]string) {
				f["system/system.yaml"] = strings.Replace(f["system/system.yaml"],
					"    runtime: {compose: residents-db}\n",
					"    runtime: {compose: residents-db}\n    depends_on:\n      - {target: api, protocol: http}\n", 1)
			},
			want: `layer "data" may not depend on layer "edge"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := fixture(t)
			tt.mutate(files)
			report := runFixture(t, files)
			if report.OK {
				t.Fatal("report OK, want failure")
			}
			r := result(t, report, tt.check)
			if r.Status != Fail {
				t.Fatalf("%s: status %s, want fail", tt.check, r.Status)
			}
			for _, f := range r.Findings {
				if strings.Contains(f.Message, tt.want) {
					return
				}
			}
			t.Errorf("%s: no finding contains %q; got %+v", tt.check, tt.want, r.Findings)
		})
	}
}

func TestUnparseableSpecSkipsOtherChecks(t *testing.T) {
	files := fixture(t)
	files["system/tech.yaml"] = "technologies: [\n"
	report := runFixture(t, files)
	if r := result(t, report, "schema"); r.Status != Fail || !strings.Contains(r.Findings[0].Message, "system/tech.yaml") {
		t.Errorf("schema = %+v, want failure naming tech.yaml", r)
	}
	if r := result(t, report, "layers"); r.Status != Skipped {
		t.Errorf("layers status = %s, want skipped", r.Status)
	}
}

// withActiveEvent adds an events contract to residents, generated code,
// and an active event; api imports the generated events package.
func withActiveEvent(f map[string]string, consumers, protoBody string) {
	f["system/system.yaml"] = strings.Replace(f["system/system.yaml"],
		"      - {proto: proto/habyta/residents/v1, generated: backend/gen/habyta/residents/v1}\n",
		"      - {proto: proto/habyta/residents/v1, generated: backend/gen/habyta/residents/v1}\n"+
			"      - {proto: proto/habyta/residents/events/v1, generated: backend/gen/habyta/residents/events/v1}\n", 1)
	f["proto/habyta/residents/events/v1/events.proto"] = "syntax = \"proto3\";\npackage habyta.residents.events.v1;\n" + protoBody
	f["backend/gen/habyta/residents/events/v1/events.pb.go"] = goFile("eventsv1")
	f["backend/internal/api/events.go"] = goFile("api", "example.com/backend/gen/habyta/residents/events/v1")
	f["system/events.yaml"] = "events:\n  - id: residents.registered\n    title: T\n    description: D\n    status: active\n" +
		"    producer: residents\n    message: habyta.residents.events.v1.ResidentRegistered\n" + consumers
}

func TestActiveEvent(t *testing.T) {
	files := fixture(t)
	withActiveEvent(files, "    consumers: [api]\n", "message ResidentRegistered {\n  string id = 1;\n}\n")
	report := runFixture(t, files)
	for _, r := range report.Results {
		if r.Status != Pass {
			t.Errorf("%s: %+v", r.Check, r.Findings)
		}
	}
}

func TestEventViolations(t *testing.T) {
	tests := []struct {
		name, check, want string
		mutate            func(map[string]string)
	}{
		{"consumer not declared", "layers", "without consuming one of its events", func(f map[string]string) {
			withActiveEvent(f, "", "message ResidentRegistered {}\n")
		}},
		{"message missing", "contracts", "message ResidentRegistered is not defined", func(f map[string]string) {
			withActiveEvent(f, "    consumers: [api]\n", "message Other {}\n")
		}},
		{"package not a contract", "schema", "is not a contract of \"residents\"", func(f map[string]string) {
			f["system/events.yaml"] = "events:\n  - {id: residents.x, title: T, description: D, status: active, producer: residents, message: habyta.residents.events.v1.X}\n"
		}},
		{"library producer", "schema", "only services and gateways publish events", func(f map[string]string) {
			f["system/events.yaml"] = "events:\n  - {id: platform.x, title: T, description: D, status: planned, producer: platform, message: habyta.platform.v1.X}\n"
		}},
		{"self consumer", "schema", "consumed by its own producer", func(f map[string]string) {
			f["system/events.yaml"] = "events:\n  - {id: residents.x, title: T, description: D, status: planned, producer: residents, consumers: [residents], message: habyta.residents.events.v1.X}\n"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := fixture(t)
			tt.mutate(files)
			r := result(t, runFixture(t, files), tt.check)
			for _, f := range r.Findings {
				if strings.Contains(f.Message, tt.want) {
					return
				}
			}
			t.Errorf("%s findings %+v lack %q", tt.check, r.Findings, tt.want)
		})
	}
}

func TestKnownChecksMatchAll(t *testing.T) {
	if strings.Join(knownChecks, ",") != strings.Join(Names(), ",") {
		t.Errorf("knownChecks = %v, want %v", knownChecks, Names())
	}
}

func TestRuntimeRules(t *testing.T) {
	requirements := `requirements:
  - {id: hc, title: H, category: operability, statement: S, applies_to: {kinds: [service, gateway]}, verified_by: runtime/healthcheck}
  - {id: mem, title: M, category: operability, statement: S, verified_by: runtime/memory-limit}
  - {id: hard, title: L, category: security, statement: S, applies_to: {components: [api]}, verified_by: runtime/hardening}
  - {id: expo, title: E, category: security, statement: S, verified_by: runtime/exposure}
  - {id: manual, title: T, category: security, statement: S}
`
	files := fixture(t)
	files["system/requirements.yaml"] = requirements
	files["compose.yaml"] = strings.Replace(files["compose.yaml"], "  residents-db:\n    image: postgres:18\n",
		"  residents-db:\n    image: postgres:18\n    ports: [\"5432:5432\"]\n"+
			"    healthcheck: {test: [CMD, pg_isready]}\n    deploy: {resources: {limits: {memory: 512M}}}\n", 1)
	r := result(t, runFixture(t, files), "runtime")
	got := map[string]bool{}
	for _, f := range r.Findings {
		got[f.Component+"/"+f.Rule] = true
		if f.Path != "compose.yaml" {
			t.Errorf("finding path = %q", f.Path)
		}
	}
	want := []string{"api/healthcheck", "residents/healthcheck", "api/memory-limit", "residents/memory-limit", "api/hardening", "residents-db/exposure"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing finding %s; got %v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d findings %v, want %d", len(got), got, len(want))
	}
}

func TestUnknownCheck(t *testing.T) {
	if _, err := Run(write(t, fixture(t)), []string{"nope"}); err == nil {
		t.Error("want error for unknown check")
	}
}

// A component's compose services can live in an included file its owner
// owns; checks follow the include and point findings at that file.
func TestComposeInclude(t *testing.T) {
	files := fixture(t)
	files["compose.yaml"] = strings.Replace(files["compose.yaml"], "  residents-db:\n    image: postgres:18\n", "", 1)
	files["compose.yaml"] = "include:\n  - deploy/compose/residents.yaml\n" + files["compose.yaml"]
	files["deploy/compose/residents.yaml"] = "services:\n  residents-db:\n    image: postgres:18\n"
	files["system/agents/res.yaml"] = strings.Replace(files["system/agents/res.yaml"], "proto/habyta/residents/]", "proto/habyta/residents/, deploy/compose/residents.yaml]", 1)
	report := runFixture(t, files)
	for _, r := range report.Results {
		if r.Status != Pass {
			t.Errorf("%s: %+v", r.Check, r.Findings)
		}
	}

	// A service in the included file that no component declares.
	files["deploy/compose/residents.yaml"] += "  stray:\n    image: alpine\n"
	r := result(t, runFixture(t, files), "services")
	if len(r.Findings) != 1 || r.Findings[0].Path != "deploy/compose/residents.yaml" {
		t.Errorf("services findings = %+v, want one in the included file", r.Findings)
	}

	// The same service in two files is an error.
	files["deploy/compose/residents.yaml"] = "services:\n  residents-db:\n    image: postgres:18\n  api:\n    image: alpine\n"
	r = result(t, runFixture(t, files), "services")
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Message, `"api" is defined in more than one compose file`) {
		t.Errorf("services findings = %+v", r.Findings)
	}
}

// A project whose code isn't where DefaultLayout expects says so in
// .fausto.yaml.
func TestLayoutFile(t *testing.T) {
	files := fixture(t)
	files["deploy/compose.yaml"] = files["compose.yaml"]
	delete(files, "compose.yaml")
	if r := result(t, runFixture(t, files), "services"); r.Status != Fail {
		t.Errorf("services without compose.yaml: %+v", r)
	}
	files[spec.LayoutFile] = "compose_file: deploy/compose.yaml\n"
	for _, name := range []string{"services", "runtime"} {
		if r := result(t, runFixture(t, files), name); r.Status != Pass {
			t.Errorf("%s with the layout file: %+v", name, r.Findings)
		}
	}
	files[spec.LayoutFile] = "compose: deploy/compose.yaml\n"
	if r := result(t, runFixture(t, files), "schema"); r.Status != Fail || !strings.Contains(r.Findings[0].Message, spec.LayoutFile) {
		t.Errorf("schema with an unknown layout key: %+v", r)
	}
}

// A spec kept in fausto: given to RunWith, it replaces the repository's
// system/ (here there is none), and its files count as the project's for
// ownership. Only CODEOWNERS has to match the charters in the repository.
func TestSpecFromFausto(t *testing.T) {
	files := fixture(t)
	// A project whose charters fausto keeps has no agent definitions.
	files["system/agents/human.yaml"] = strings.Replace(files["system/agents/human.yaml"], "[system/, .claude/]", "[system/]", 1)
	kept := map[string][]byte{}
	for name, content := range files {
		if strings.HasPrefix(name, "system/") {
			kept[name] = []byte(content)
			delete(files, name)
		}
	}
	root := write(t, files)
	report, err := RunWith(root, nil, spec.Options{Files: kept})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range report.Results {
		if r.Status != Pass {
			t.Errorf("%s: status %s, findings %+v", r.Check, r.Status, r.Findings)
		}
	}

	// Without it, there is no spec to check against.
	report, err = Run(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := result(t, report, "schema"); r.Status != Fail || !strings.Contains(r.Findings[0].Message, "no spec") {
		t.Errorf("without a spec: %+v", r)
	}
}

// With the spec in the repository but no charters (as in CI for a project
// whose charters fausto keeps), ownership can't be known: those checks are
// skipped, and owners aren't checked against charters.
func TestNoCharters(t *testing.T) {
	files := fixture(t)
	files["system/agents/human.yaml"] = strings.Replace(files["system/agents/human.yaml"], "[system/, .claude/]", "[system/]", 1)
	for name := range files {
		if strings.HasPrefix(name, "system/agents/") {
			delete(files, name)
		}
	}
	report, err := Run(write(t, files), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range report.Results {
		want := Pass
		if r.Check == "ownership" || r.Check == "agents" {
			want = Skipped
		}
		if r.Status != want {
			t.Errorf("%s: status %s (want %s), findings %+v", r.Check, r.Status, want, r.Findings)
		}
	}
}

func TestGitHubOutput(t *testing.T) {
	report := &Report{Results: []Result{
		{Check: "services", Status: Fail, Findings: []Finding{{Component: "api", Path: "compose.yaml", Message: "50% off, a\nb"}}},
		{Check: "layers", Status: Pass, Findings: []Finding{}},
	}}
	var b strings.Builder
	report.WriteGitHub(&b)
	if want := "::error title=fausto services,file=compose.yaml::api: 50%25 off, a%0Ab\n"; b.String() != want {
		t.Errorf("annotations = %q, want %q", b.String(), want)
	}
	b.Reset()
	report.WriteMarkdown(&b)
	if !strings.Contains(b.String(), "1 of 2 checks found drift") || !strings.Contains(b.String(), "**api** `compose.yaml`") {
		t.Errorf("summary:\n%s", b.String())
	}
}
