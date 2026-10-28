# Habbyte

Apartment complex management. Go backend, with a React frontend planned.

## System spec

`system/` is the source of truth for the architecture: components and layers
(`system.yaml`), implementation principles, the technology radar, domains,
agent charters with file ownership, and decisions (ADRs).

`fausto`, in `fausto/`, keeps the code in sync with the spec: `fausto check`
verifies it (CI runs it on every pull request), `fausto serve` opens a web UI
for browsing the spec and proposing changes to it, and `fausto agents`
regenerates agent definitions from charters. See `fausto/README.md` for how
to run it, and `CLAUDE.md` for how agents work with the spec.

## Naming and layout

- Repository/project: `habbyte`.
- Go module: `github.com/CMOT/habyta/backend`.
- Services: `api` (HTTP/JSON gateway for React), and the `residents` and
  `administrators` domain services (gRPC).
- Future domain services should use business names such as `properties`,
  `billing`, and `maintenance` when their boundaries are defined.

```text
system/                          # Architecture spec: source of truth (see above)
tasks/                           # Agent tasks created by accepted proposals
fausto/                          # Supervising tool: `fausto check` drift checks
compose.yaml
buf.yaml, buf.gen.yaml           # Protobuf lint/breaking rules and code generation
proto/habbyte/<domain>/v1/        # Versioned gRPC contracts
backend/
  Dockerfile                     # Builds any service: --build-arg SERVICE=<name>
  cmd/<service>/                 # Entry points: service.Main(name, register)
  gen/                           # Generated Go code (committed; do not edit)
  internal/api/                  # Gateway HTTP handlers calling gRPC services
  internal/residents/            # residents gRPC service, Postgres store, migrations
  internal/administrators/       # administrators gRPC service, Postgres store, migrations
  internal/platform/server/      # HTTP/gRPC transports, middleware, health, shutdown
  internal/platform/service/     # Process setup, env config, healthcheck subcommand
  internal/platform/grpcclient/  # Service-to-service clients (round robin, request IDs)
  internal/platform/requestid/   # Request ID propagation across HTTP and gRPC
  internal/platform/postgres/    # Connection pools and embedded goose migrations
deploy/compose/<service>.yaml    # Compose services kept by their owning agent, included from compose.yaml
deploy/postgres/                 # Database init scripts for local Compose
```

Each service's `main` calls `service.Main(name, register)`; `register` adds HTTP
routes with `Server.Handle` and gRPC services with generated
`Register<Name>Server(s, impl)` functions, since `*server.Server` is a
`grpc.ServiceRegistrar`.

React calls HTTP/JSON endpoints on `api`, which translates them into gRPC calls
to domain services. Browser JSON types are hand-written in the gateway so the
public API can evolve independently of internal contracts. Domain services are
reachable only on the Compose network. Every request carries an `X-Request-ID`
that is forwarded as `x-request-id` gRPC metadata and logged by each service.

### Data

Each service owns its database; services never read another service's tables.
`residents` uses Postgres 18 (`residents-db` in Compose) through `DATABASE_URL`.
On startup it waits up to 30 seconds for the database, then applies the SQL
migrations embedded from `internal/residents/migrations` with goose. A Postgres
advisory lock lets several replicas start at once safely. Readiness (`/readyz`
and gRPC health) fails while the database is unreachable; liveness does not.

Persistence sits behind the `store` interface in `internal/residents/store.go`,
so changing databases only means adding another implementation. Add schema
changes as new numbered files in the migrations directory; never edit one that
has been applied.

`administrators` works the same way with its own Postgres 18 database
(`administrators-db`, defined in `deploy/compose/administrators.yaml`), its
migrations in `internal/administrators/migrations`, and its `store` interface
in `internal/administrators/store.go`.

Local data lives in the `residents-db` and `administrators-db` volumes.
`docker compose down -v` deletes them. The database passwords default to
`residents` and `administrators`; set `RESIDENTS_DB_PASSWORD` and
`ADMINISTRATORS_DB_PASSWORD` in `.env` to change them before the volumes are
first created.

### API

All IDs are UUIDs. Errors are `{"error": {"code", "message", "requestId"}}`.

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/v1/tenants/{tenantId}/residents` | Body: `unitId`, `fullName`, `email`, optional `phone`, `keycloakSub` |
| `GET` | `/v1/tenants/{tenantId}/residents/{id}` | |
| `GET` | `/v1/tenants/{tenantId}/residents` | Query: `unitId`, `pageSize` (≤200), `pageToken` |

### Protobuf

Edit files under `proto/`, then lint and regenerate from the repository root:

```sh
docker run --rm -v "$PWD":/workspace -w /workspace bufbuild/buf:1.73.0 lint
docker run --rm -v "$PWD":/workspace -w /workspace bufbuild/buf:1.73.0 generate
```

Commit the regenerated `backend/gen` with the proto change. Never change the
meaning of an existing field number; add a new `v2` package for breaking changes.

Events follow the same rules. Declare each event in `system/events.yaml` with
its producer, consumers, and message, such as
`habbyte.residents.events.v1.ResidentRegistered`. Before an event goes active,
add its package (`proto/habbyte/residents/events/v1`) to the producer's
`contracts` in `system/system.yaml`; consumers may import the generated code
only for events they consume.

## Run with Docker

Requires Docker with the Compose plugin; a host Go installation is optional.

```sh
docker compose up --build -d --wait
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
docker compose logs -f api residents administrators
```

Both endpoints return JSON. `/healthz` reports process liveness; `/readyz` reports
whether the service accepts work. The container healthcheck tests both HTTP
readiness and the standard gRPC health RPC. Readiness will need to include
critical dependencies when those are added.

```sh
docker compose down
```

HTTP is published on localhost only. To change the host port, copy `.env.example`
to `.env` and set `HTTP_PORT`. Services reach each other over gRPC on the
Compose network, for example `residents:9090`; gRPC ports are not published to
the host. The gateway resolves `RESIDENTS_ADDR` (default
`dns:///residents:9090`) and balances calls across replicas
(`docker compose up --scale residents=2`). The local setup uses plaintext gRPC. Configure transport security and service authentication
before deployment across untrusted networks.

## Development and verification

The Docker builder uses Go 1.27.1 and the module requires Go 1.27 or newer.
The runtime is a minimal image with a static binary and CA certificates, runs
as a non-root numeric user, and uses a read-only filesystem in Compose.

Run static analysis and tests, including the race detector, inside Docker.
The build target uses an in-memory store; the Compose `test` profile also runs
the Postgres integration tests, each service against its own test database
(`residents_test`, `administrators_test`):

```sh
docker build --target test -t habbyte-test ./backend
docker compose --profile test run --rm --build test                   # residents
docker compose --profile test run --rm --build administrators-test    # administrators
```

With Go installed locally:

```sh
cd backend
go run ./cmd/api
go vet ./...
go test -race ./...
# With a reachable Postgres, also run the integration tests:
TEST_DATABASE_URL=postgres://... go test -race ./...                 # residents
ADMINISTRATORS_TEST_DATABASE_URL=postgres://... go test -race ./...  # administrators
```

The service accepts `HTTP_ADDR` (default `:8080`), `GRPC_ADDR` (default
`:9090`), and `SHUTDOWN_TIMEOUT` (default `10s`). It logs JSON to stdout and
drains HTTP and gRPC requests on SIGINT or SIGTERM, allowing up to
`SHUTDOWN_TIMEOUT` before forcing shutdown. Compose allows 15 seconds before
killing the container, so keep `SHUTDOWN_TIMEOUT` below that.

HTTP requests get an `X-Request-ID` (an incoming valid one is kept), an access
log line, panic recovery, and a 1 MiB default body limit. gRPC calls get panic
recovery, logging, and keepalive limits; connections are recycled every five
minutes so clients rebalance across replicas. Time zone data is embedded in the
binary because the runtime image has no zoneinfo.

Add future services with their own entry points, container images, and Compose
entries. Keep domain logic within its owning service and avoid cross-service
database access. Separate Go modules can be introduced if release ownership
requires them; one backend module keeps this initial scaffold simple.
