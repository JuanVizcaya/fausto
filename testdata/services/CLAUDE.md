# Working on Habbyte

`system/` is the source of truth for Habbyte's architecture. Read it before
changing code:

- `system/system.yaml`: components, layers, dependencies, and technologies.
- `system/principles.yaml`: implementation rules every change follows.
- `system/agents/<you>.yaml`: your charter, the paths you own, and your rules.
  Don't change files that another agent owns; describe the change you need in
  your PR instead.
- `system/decisions/`: architecture decision records (ADRs).
- `system/roadmap.yaml`: milestones, their goals and exit criteria. A task
  with `milestone: <id>` counts toward that milestone.
- `system/requirements.yaml`: non-functional requirements (security,
  availability, performance, ...) and which components they apply to. Those
  with `verified_by` are enforced by `fausto check`; meet the rest by design.
- `system/events.yaml`: asynchronous events, their producer, consumers, and
  protobuf message. Publish or consume only events declared there.
- `system/risks.yaml`: known risks and their mitigations. If your work
  creates or removes a risk, say so in your PR so the orchestrator can
  update the register.

## Tasks

If you were started as an agent (`claude --agent <id>`), your definition in
`.claude/agents/<id>.md` is generated from your charter; don't edit it.

Your work arrives as fausto tasks, kept in fausto's database (see
`system/decisions/0004-workflow-state-in-mongodb.md`). From `fausto/`, run
`go run ./cmd/fausto task pull <you>`: it writes your open tasks to
`.fausto/tasks/` (git-ignored; never commit them). Each says which spec
changes affect your components and which checks to fix. Run
`go run ./cmd/fausto task start <id>` when you start and
`go run ./cmd/fausto task done <id>` when `fausto check` passes. This needs
`FAUSTO_MONGO_URI` in your environment.

## Changing the spec

If a change adds a service, contract, database, or cross-component dependency,
declare it in `system/` in the same PR. Changes to `system/` itself need the
orchestrator's approval: propose them in fausto (`fausto serve`).

## Before finishing

Run the drift checks from `fausto/`:

```sh
go run ./cmd/fausto check
```

Without a local Go toolchain:

```sh
docker run --rm -v "$PWD":/repo -w /repo/fausto golang:1.27.1-bookworm go run ./cmd/fausto check
```
