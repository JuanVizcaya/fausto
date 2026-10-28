package check

import (
	"strings"
	"testing"
)

// monolithFixture is a minimal modular monolith: one binary running an API
// and two modules, which meet through a Go contract and each own a schema
// in one Postgres server, behind a proxy run from an upstream image.
func monolithFixture(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"system/system.yaml": `
name: test
layers:
  - {id: edge, may_depend_on: [modules, platform]}
  - {id: modules, may_depend_on: [data, platform]}
  - {id: data, may_depend_on: []}
  - {id: platform, may_depend_on: []}
components:
  - id: proxy
    name: Proxy
    kind: gateway
    layer: edge
    status: active
    owner: plat
    runtime: {image: caddy, compose: proxy}
    depends_on:
      - {target: app, protocol: http}
  - id: app
    name: App
    kind: service
    layer: edge
    status: active
    owner: plat
    paths: [backend/cmd/app]
    runtime: {cmd: backend/cmd/app, compose: app}
    depends_on:
      - {target: platform, protocol: library}
  - id: api
    name: API
    kind: gateway
    layer: edge
    status: active
    owner: plat
    paths: [backend/internal/api]
    runtime: {host: app}
    depends_on:
      - {target: billing, protocol: contract}
  - id: billing
    name: Billing
    kind: service
    layer: modules
    status: active
    owner: mods
    paths: [backend/internal/modules/billing]
    runtime: {host: app}
    contracts:
      - {go: backend/internal/modules/billing/contract}
    depends_on:
      - {target: people, protocol: contract}
      - {target: db, protocol: sql, schema: billing}
      - {target: platform, protocol: library}
  - id: people
    name: People
    kind: service
    layer: modules
    status: active
    owner: mods
    paths: [backend/internal/modules/people]
    runtime: {host: app}
    contracts:
      - {go: backend/internal/modules/people/contract}
    depends_on:
      - {target: db, protocol: sql, schema: people}
  - id: db
    name: Postgres
    kind: database
    layer: data
    status: active
    owner: plat
    runtime: {image: postgres, compose: db}
  - id: platform
    name: Platform
    kind: library
    layer: platform
    status: active
    owner: plat
    paths: [backend/internal/platform]
    contracts:
      - {proto: proto/test/platform/events/v1, generated: backend/gen/test/platform/events/v1}
`,
		"system/tech.yaml":       "technologies: []\n",
		"system/principles.yaml": "principles: []\n",
		"system/requirements.yaml": `requirements:
  - {id: expo, title: E, category: security, statement: S, verified_by: runtime/exposure}
`,
		"system/agents/plat.yaml":  "id: plat\nname: Platform\nkind: agent\nscope: Platform.\nowns: [backend/, proto/, compose.yaml]\n",
		"system/agents/mods.yaml":  "id: mods\nname: Modules\nkind: agent\nscope: Modules.\nowns: [backend/internal/modules/]\n",
		"system/agents/human.yaml": "id: human\nname: Orchestrator\nkind: human\nscope: Spec.\nowns: [system/, .claude/]\n",
		"compose.yaml": `
services:
  proxy:
    image: caddy:2.11.6-alpine
    ports: ["443:443"]
  app:
    build: ./backend
    environment:
      DATABASE_URL: "postgres://u:p@db:5432/app"
  db:
    image: postgres:18
    ports: ["127.0.0.1:5432:5432"]
`,
		"backend/go.mod": "module example.com/backend\n\ngo 1.27.0\n",
		"backend/cmd/app/main.go": goFile("main", "example.com/backend/internal/api", "example.com/backend/internal/modules/billing",
			"example.com/backend/internal/modules/people", "example.com/backend/internal/platform"),
		"backend/internal/api/api.go":                           goFile("api", "example.com/backend/internal/modules/billing/contract"),
		"backend/internal/modules/billing/module.go":            goFile("billing", "example.com/backend/internal/modules/billing/service"),
		"backend/internal/modules/billing/contract/contract.go": goFile("contract"),
		"backend/internal/modules/billing/service/service.go": goFile("service", "example.com/backend/internal/modules/people/contract",
			"example.com/backend/gen/test/platform/events/v1"),
		"backend/internal/modules/people/module.go":            goFile("people"),
		"backend/internal/modules/people/contract/contract.go": goFile("contract"),
		"backend/internal/platform/platform.go":                goFile("platform"),
		"backend/gen/test/platform/events/v1/envelope.pb.go":   goFile("eventsv1"),
		"proto/test/platform/events/v1/envelope.proto":         "syntax = \"proto3\";\n\npackage test.platform.events.v1;\n",
	}
}

func TestMonolithPasses(t *testing.T) {
	report := runFixture(t, monolithFixture(t))
	for _, r := range report.Results {
		if r.Status != Pass {
			t.Errorf("%s: status %s, findings %+v", r.Check, r.Status, r.Findings)
		}
	}
}

func TestMonolithViolations(t *testing.T) {
	replace := func(file, old, new string) func(map[string]string) {
		return func(f map[string]string) {
			if !strings.Contains(f[file], old) {
				t.Fatalf("%s has no %q", file, old)
			}
			f[file] = strings.Replace(f[file], old, new, 1)
		}
	}
	tests := []struct {
		name   string
		check  string
		mutate func(files map[string]string)
		want   string
	}{
		{
			name:   "binary doesn't register a module",
			check:  "services",
			mutate: replace("backend/cmd/app/main.go", "import _ \"example.com/backend/internal/modules/people\"\n", ""),
			want:   `runs in "app", but backend/cmd/app doesn't import its package backend/internal/modules/people`,
		},
		{
			name:   "image differs from compose",
			check:  "services",
			mutate: replace("compose.yaml", "image: caddy:2.11.6-alpine", "image: nginx:1.29"),
			want:   `declares image caddy, but compose service "proxy" runs nginx:1.29`,
		},
		{
			name:   "service without cmd, image or host",
			check:  "services",
			mutate: replace("system/system.yaml", "runtime: {image: caddy, compose: proxy}", "runtime: {compose: proxy}"),
			want:   "must declare runtime.cmd, runtime.image or runtime.host",
		},
		{
			name:   "contract imported without contract dependency",
			check:  "layers",
			mutate: replace("system/system.yaml", "      - {target: people, protocol: contract}\n", ""),
			want:   `imports the "people" contract (example.com/backend/internal/modules/people/contract) without declaring a contract dependency`,
		},
		{
			name:  "module imports another module's internals",
			check: "layers",
			mutate: func(f map[string]string) {
				f["backend/internal/modules/people/bad.go"] = goFile("people", "example.com/backend/internal/modules/billing/service")
			},
			want: `imports internal code of service "billing"`,
		},
		{
			name:  "host imports a module's internals",
			check: "layers",
			mutate: func(f map[string]string) {
				f["backend/cmd/app/bad.go"] = goFile("main", "example.com/backend/internal/modules/billing/service")
			},
			want: `imports internal code of service "billing"`,
		},
		{
			name:   "event envelope without library dependency",
			check:  "layers",
			mutate: replace("system/system.yaml", "      - {target: platform, protocol: library}\n  - id: people", "  - id: people"),
			want:   `without declaring a grpc dependency on "platform"`,
		},
		{
			name:   "two modules own one schema",
			check:  "db-ownership",
			mutate: replace("system/system.yaml", "schema: people}", "schema: billing}"),
			want:   `schema "billing" is used by 2 components (billing, people)`,
		},
		{
			name:   "database used whole and through schemas",
			check:  "db-ownership",
			mutate: replace("system/system.yaml", "{target: db, protocol: sql, schema: people}", "{target: db, protocol: sql}"),
			want:   "database is used as a whole by people and through schemas by others",
		},
		{
			name:  "host connects to a database its modules don't use",
			check: "db-ownership",
			mutate: func(f map[string]string) {
				replace("system/system.yaml", "      - {target: db, protocol: sql, schema: billing}\n", "")(f)
				replace("system/system.yaml", "    depends_on:\n      - {target: db, protocol: sql, schema: people}\n", "")(f)
			},
			want: `connects to database "db" without declaring a sql dependency`,
		},
		{
			name:   "Go contract without Go files",
			check:  "contracts",
			mutate: func(f map[string]string) { delete(f, "backend/internal/modules/people/contract/contract.go") },
			want:   "declared Go contract has no Go files",
		},
		{
			name:   "contract dependency across processes",
			check:  "schema",
			mutate: replace("system/system.yaml", "      - {target: app, protocol: http}\n", "      - {target: billing, protocol: contract}\n"),
			want:   `contract dependency on "billing", which runs in another process`,
		},
		{
			name:   "contract dependency on a component without a Go contract",
			check:  "schema",
			mutate: replace("system/system.yaml", "      - {target: billing, protocol: contract}\n", "      - {target: platform, protocol: contract}\n"),
			want:   `contract dependency on "platform", which declares no Go contract`,
		},
		{
			name:   "unknown host",
			check:  "schema",
			mutate: replace("system/system.yaml", "    runtime: {host: app}\n    depends_on:\n      - {target: billing", "    runtime: {host: nope}\n    depends_on:\n      - {target: billing"),
			want:   `runtime.host is unknown component "nope"`,
		},
		{
			name:   "hosted component with its own command",
			check:  "schema",
			mutate: replace("system/system.yaml", "runtime: {host: app}\n    depends_on:\n      - {target: billing", "runtime: {host: app, cmd: backend/cmd/api}\n    depends_on:\n      - {target: billing"),
			want:   `runs in "app", so it declares no runtime.cmd`,
		},
		{
			name:   "host's layer may not depend on the hosted component's",
			check:  "schema",
			mutate: replace("system/system.yaml", "{id: edge, may_depend_on: [modules, platform]}", "{id: edge, may_depend_on: [platform]}"),
			want:   `layer "edge" of host "app" may not depend on layer "modules"`,
		},
		{
			name:   "schema on a non-sql dependency",
			check:  "schema",
			mutate: replace("system/system.yaml", "{target: app, protocol: http}", "{target: app, protocol: http, schema: app}"),
			want:   `http dependency on "app" names a schema`,
		},
		{
			name:   "database published beyond loopback",
			check:  "runtime",
			mutate: replace("compose.yaml", `ports: ["127.0.0.1:5432:5432"]`, `ports: ["5432:5432"]`),
			want:   `compose service "db" publishes ports beyond loopback`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := monolithFixture(t)
			tt.mutate(files)
			r := result(t, runFixture(t, files), tt.check)
			for _, f := range r.Findings {
				if strings.Contains(f.Message, tt.want) {
					return
				}
			}
			t.Errorf("%s: no finding contains %q; got %+v", tt.check, tt.want, r.Findings)
		})
	}
}

func TestImageRepo(t *testing.T) {
	for ref, want := range map[string]string{
		"postgres":                             "postgres",
		"postgres:18":                          "postgres",
		"docker.io/library/postgres:18":        "postgres",
		"quay.io/keycloak/keycloak:26.8.0":     "quay.io/keycloak/keycloak",
		"localhost:5000/app":                   "localhost:5000/app",
		"caddy@sha256:0123":                    "caddy",
		"ghcr.io/juanvizcaya/fausto:v0@sha256": "ghcr.io/juanvizcaya/fausto",
	} {
		if got := imageRepo(ref); got != want {
			t.Errorf("imageRepo(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestPublicPort(t *testing.T) {
	for _, tt := range []struct {
		port any
		want bool
	}{
		{"8080", true},
		{"80:80", true},
		{"443:443/udp", true},
		{"0.0.0.0:5432:5432", true},
		{"127.0.0.1:5432:5432", false},
		{"[::1]:5432:5432", false},
		{"127.0.0.1:9000:8080/tcp", false},
		{8080, true},
		{map[string]any{"target": 5432, "host_ip": "127.0.0.1"}, false},
		{map[string]any{"target": 5432, "published": "5432"}, true},
	} {
		if got := publicPort(tt.port); got != tt.want {
			t.Errorf("publicPort(%v) = %v, want %v", tt.port, got, tt.want)
		}
	}
}
