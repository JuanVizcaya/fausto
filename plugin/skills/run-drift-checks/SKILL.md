---
name: run-drift-checks
description: Use when checking that code matches a project's architecture spec in system/ (fausto's drift checks), before finishing a change in such a project, or when the user asks why `fausto check` fails.
---

# Running fausto's drift checks

Run `fausto_check` (all checks, or name some), or `fausto check` from the
repository root. Without fausto installed:
`docker run --rm -v "$PWD":/repo ghcr.io/juanvizcaya/fausto check`.

| Check | What it compares |
|---|---|
| `schema` | spec files against their schemas; every reference resolves |
| `services` | declared services against their commands and the Compose file |
| `layers` | Go imports against declared dependencies and layer rules |
| `db-ownership` | each database used by exactly one owner |
| `contracts` | declared protobuf contracts exist, are versioned and generated |
| `ownership` | every file has one owning agent, matching its component's owner |
| `runtime` | Compose services meet the runtime rules requirements ask for |
| `agents` | files generated from the charters (CODEOWNERS) are current |

Reading the results:

- Each finding names a component and a path; fix the ones in paths you own.
- A finding means the code and the spec disagree. Change the code to match
  the spec; if the spec is wrong, don't edit `system/`: say what should
  change, and the orchestrator proposes it in fausto.
- `ownership` and `agents` are skipped where fausto's charters can't be
  read (no connection, as in CI); that is expected.
- A project laid out unlike fausto's defaults (`backend/cmd`, `compose.yaml`,
  `proto/`) says so in `.fausto.yaml`; don't add one to silence a finding.
