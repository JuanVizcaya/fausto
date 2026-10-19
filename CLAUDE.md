# Working on fausto

Fausto drift-checks a project's `system/` spec against its code and serves
a web UI for proposing changes to the spec. It is its own project: it works
on a target project (first Habyta) only through that project's repository.
See `README.md` for the full picture (what each subcommand does, the
proposal workflow, running agents, the web UI build). This file is just the
fast-start version.

## Quick commands (run from this repository's root)

- `go vet ./... && go test ./...` — sanity check
- `go run ./cmd/fausto check -root ../habyta` (add `-json`, or a check name
  to run just one); without `-root`, against a project, it finds the nearest
  parent with `system/system.yaml`
- `docker compose -f compose.yaml -f compose.dev.yaml up -d mongo`, then
  `go run ./cmd/fausto serve -root ../habyta` — web UI at `127.0.0.1:7070`
- `go run ./cmd/fausto agents -root <project>` — regenerate the project's
  `.claude/agents/*.md` and `.github/CODEOWNERS` from its
  `system/agents/*.yaml` charters
- `go run ./cmd/fausto jira sync [-dry-run]` — mirror tasks as Jira issues
  (needs the `FAUSTO_JIRA_*` variables; see `README.md`)
- `go run ./cmd/fausto project add|list|rm`, `charters import|export|list`
  and `task pull|start|done <…>` — projects, the agent charters fausto
  keeps, and tasks in the database (needs `FAUSTO_MONGO_URI`)
- `cd web && npm run dev` — live-reload dev server for the web UI

## Where things live

- `internal/spec` — parses all of a project's `system/` into typed Go structs
- `internal/schema` — the embedded JSON Schemas for the spec files
- `internal/check` — the 8 drift checks (`schema`, `services`, `layers`,
  `db-ownership`, `contracts`, `ownership`, `runtime`, `agents`)
- `internal/code` — read-only Go/proto analysis (`go/parser`, no
  compilation) powering the Architecture drill-down views
- `internal/edit` — applies structured, diff-minimizing edits to spec YAML
- `internal/store` — the `Store` interface for one project's proposals and
  tasks, and `Projects`, the registry of projects; `memstore` (tests) and
  `mongostore` (MongoDB), both held to `storetest`
- `internal/proposal` — proposals as stored edits: review against the
  current base, accept onto a branch (or pull request) of their own, sync
  to applied or withdrawn once merged or closed, reject;
  `charters.go` lays the charters fausto keeps over `system/agents/`
- `internal/source` — a project's repository: a local clone, or GitHub
  (fausto's own clone, branches pushed and pull requests opened with
  `FAUSTO_GITHUB_TOKEN`)
- `internal/agentdef` — agent definitions (as files, or the description and
  prompt `claude --agents` takes) and `.github/CODEOWNERS` from charters
- `internal/client` — the agent side of fausto's API, and a clone's
  connection in `.fausto/config.json`
- `internal/mcp` — `fausto mcp`, the stdio MCP server agents use
- `plugin/`, `.claude-plugin/` — the Claude Code plugin and its marketplace;
  check them with `claude plugin validate .` and `claude plugin validate plugin`
- `internal/jira` — Jira Cloud client and the task → issue sync
- `internal/server` — HTTP API + embeds the built web UI (`dist/`); the
  agent API (`/api/agent/…`, token-authenticated) is in `agentapi.go`
- `web/` — React + Vite source for the UI; **not** built automatically, see
  below

## Before finishing (mirrors CI in `.github/workflows/ci.yml`)

1. `go vet ./... && go test ./...` (set `FAUSTO_TEST_MONGO_URI` to also run
   the MongoDB store tests; CI does). The tests run against the project
   snapshot in `testdata/example`; when a change alters generated agent
   definitions, refresh it with `go run ./cmd/fausto agents -root testdata/example`.
2. If you touched `web/src`: `cd web && npm run build`, then commit the
   regenerated `internal/server/dist` — CI diffs this and fails if it's
   stale.
