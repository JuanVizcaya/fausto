# Habbyte

Condominium management for Mexico. Go backend, with a React frontend planned.

## System spec

The architecture spec (components and layers, implementation principles, the
technology radar, domains, roadmap, requirements, events, risks, the agents'
charters, and decisions) lives in
[fausto](https://github.com/JuanVizcaya/fausto), an external tool, not in
this repository (ADR 0006). Read and change it in fausto's UI; fausto checks
the code against it on every pull request, as `fausto/check`. `CLAUDE.md`
says how agents get it.

To leave fausto, `fausto spec export -root .` writes the spec to `system/`;
commit it.

## Naming and layout

- Repository/project: `habbyte`.
- Go module: `github.com/Ceferinto/habbyte/backend` (the current GitHub repository).
- The backend is a modular monolith: one binary, `habbyte`, with 14 modules.
  Each module can later become its own service without rewriting its domain
  code (see the architecture design in fausto).

```text
compose.yaml
buf.yaml, buf.gen.yaml              # Protobuf lint/breaking rules and code generation
proto/habbyte/<module>/events/v1/   # Event messages; the envelope is in platform/events/v1
backend/
  Dockerfile                        # Builds a command: --build-arg SERVICE=<name> (default habbyte)
  cmd/habbyte/                      # MVP binary: registers every module, mounts the API
  gen/                              # Generated Go code (committed; do not edit)
  internal/edge/api/                # Public HTTP/JSON API under /v1
  internal/modules/<module>/
    module.go                       # Registers the module's contract with the Registry
    contract/                       # Public Go interface: the only package other modules import
    service/                        # Implements the contract
    domain/                         # Entities and rules, no I/O
    store/                          # Store interface and its Postgres implementation
    migrations/                     # goose SQL files for the module's schema
    adapters/<provider>/            # Provider integrations, where the module has them
  internal/platform/app/            # Process setup, env config, healthcheck subcommand
  internal/platform/httpserver/     # HTTP transport, middleware, health, shutdown
  internal/platform/module/         # Module interface and Registry
  internal/platform/postgres/       # Connection pools and goose migrations
  internal/platform/requestid/      # Request IDs in contexts and headers
  internal/archtest/                # Module boundary checks, run with go test
deploy/caddy/Caddyfile              # Edge proxy: TLS, api. to the backend, auth. to Keycloak
deploy/postgres/init.sh             # Creates the habbyte, habbyte_test and keycloak databases
```

The modules: `iam`, `developments`, `control` (the core); `finance`,
`payments`, `invoicing`, `community`, `amenities`, `operations`,
`accesscontrol`, `devices` (products); `notifications`, `files`, `audit`
(shared capabilities).

### Module boundaries

A module imports `internal/platform`, other modules' `contract` packages, and
its own packages, nothing else. A contract never imports its module's other
packages, and the edge reaches modules only through contracts.
`internal/archtest` enforces this in `go test`, and also checks that every
module has `module.go`, `contract`, `service`, `domain`, `store` and
`migrations`. `cmd/habbyte` tests that every folder under `internal/modules`
is wired into the binary.

To add a module, copy an existing one, rename the package and `Name`, and add
`<module>.New()` to `modules()` in `cmd/habbyte/main.go`.

### Data

One Postgres 18 container holds the `habbyte` database, with one schema per
module, and the `keycloak` database. A module never reads another module's
schema. Schemas, per-module roles and row-level security arrive with the M0
platform work.

Local data lives in the `postgres` volume, and `docker compose down -v` deletes
it. Passwords default to the values in `.env.example`; set them in `.env`
before the volume is first created.

### API

Errors are `{"error": {"code", "message", "requestId", "details"}}`. No
endpoints exist yet beyond `/healthz` and `/readyz`; unknown `/v1` paths answer
404 in that format.

### Protobuf

Edit files under `proto/`, then lint and regenerate from the repository root:

```sh
docker run --rm -v "$PWD":/workspace -w /workspace bufbuild/buf:1.73.0 lint
docker run --rm -v "$PWD":/workspace -w /workspace bufbuild/buf:1.73.0 generate
```

Commit the regenerated `backend/gen` with the proto change. Never change the
meaning of an existing field number; add a new `v2` package for breaking changes.

Events are wrapped in `habbyte.platform.events.v1.Envelope`. Declare each event
in `system/events.yaml` with its producer, consumers, and message, such as
`habbyte.developments.events.v1.OccupancyEnded`.

## Run with Docker

Requires Docker with the Compose plugin; a host Go installation is optional.

```sh
docker compose up --build -d --wait
docker compose logs -f backend caddy
docker compose down
```

Caddy (`deploy/caddy/Caddyfile`) is the only published service, on ports 80
and 443. It terminates TLS and routes by host:

| Host | Goes to | Notes |
| --- | --- | --- |
| `https://api.localhost` | `backend:8080` | `/healthz`, `/readyz`, `/v1/...` |
| `https://auth.localhost` | `keycloak:8080` | Sign-in and OIDC; `/admin`, `/metrics` and `/health` answer 404 |
| `http://localhost:9000/admin` | Keycloak directly | Admin console, loopback only (`admin` / `admin_password`) |

`*.localhost` resolves to 127.0.0.1 with no `/etc/hosts` changes. Caddy issues
those certificates from its own local CA; copy its root to trust it:

```sh
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./caddy-root.crt
curl --cacert caddy-root.crt https://api.localhost/healthz
# Browsers on macOS:
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain caddy-root.crt
```

The root is kept in the `caddy-data` volume, so this is needed once per
volume. In production, `API_HOST` and `AUTH_HOST` are real domains and Caddy
gets Let's Encrypt certificates for them; set `ACME_EMAIL` there. Keycloak's
issuer follows `AUTH_HOST` (`KC_HOSTNAME`).

The backend is not published. `/healthz` reports process liveness, and
`/readyz` reports whether the backend accepts work; Caddy also uses `/readyz`
for its health checks. To reach the backend without Caddy, add a port in a
`compose.override.yaml`.

## Development and verification

The Docker builder uses Go 1.27.1 and the module requires Go 1.27 or newer.
The runtime is a minimal image with a static binary and CA certificates, runs
as a non-root numeric user, and uses a read-only filesystem in Compose.

Run static analysis and tests, including the race detector and the Postgres
integration tests (against `habbyte_test`), inside Docker:

```sh
docker compose --profile test run --rm --build test
```

With Go installed locally:

```sh
cd backend
go run ./cmd/habbyte
go vet ./...
go test -race ./...
# With a reachable Postgres, also run the integration tests:
TEST_DATABASE_URL=postgres://... go test -race ./...
```

The backend accepts `HTTP_ADDR` (default `:8080`) and `SHUTDOWN_TIMEOUT`
(default `10s`). It logs JSON to stdout and drains requests on SIGINT or
SIGTERM, allowing up to `SHUTDOWN_TIMEOUT` before forcing shutdown. Compose
allows 15 seconds before killing the container, so keep `SHUTDOWN_TIMEOUT`
below that.

HTTP requests get an `X-Request-ID` (an incoming valid one is kept), an access
log line, panic recovery, and a 1 MiB default body limit. Time zone data is
embedded in the binary because the runtime image has no zoneinfo.
