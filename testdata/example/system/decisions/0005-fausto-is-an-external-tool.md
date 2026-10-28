---
id: "0005"
title: Fausto is an external tool
status: accepted
supersedes: "0004"
date: 2026-10-20
---

## Context

fausto, the tool that drift-checks `system/` against the code, runs the
proposal workflow and hands work to agents, lived in this repository under
`fausto/`. Habbyte's CI ran it from source, `CLAUDE.md` and the generated
`.claude/agents/` told agents to run it from `fausto/`, the agent charters
were files in `system/agents/`, and `compose.yaml` had a `fausto` service.
Habbyte could not be built or worked on without fausto, and fausto could not
be developed, released or used for another project on its own.

## Decision

fausto is an external tool, at https://github.com/JuanVizcaya/fausto, and
reaches Habbyte only through its repository:

- Habbyte keeps its code and its architecture spec in `system/` as plain
  YAML and Markdown, readable and editable without fausto. The spec's JSON
  Schemas are fausto's.
- fausto keeps the agent charters, proposals and tasks in its own database,
  deployed with it. Agents get their definition and tasks from fausto's API
  with a token per agent (`fausto claude <agent>`); nothing about agents is
  committed here.
- An accepted proposal reaches Habbyte as a branch or pull request that
  people merge; fausto never commits to Habbyte's branches. `CODEOWNERS`
  stays here as a plain file, which fausto updates through those changes.
- CI runs the drift checks through fausto's reusable workflow, from one
  file, `.github/workflows/fausto.yml`. Without fausto's database, CI skips
  the checks that need the charters (ownership and generated files).

This supersedes 0004: proposals and tasks still live in MongoDB, now as
part of fausto rather than of Habbyte.

## Consequences

- Removing `.github/workflows/fausto.yml` disconnects Habbyte from fausto;
  everything else here works without it.
- Ownership is enforced where fausto runs, not in Habbyte's CI.
- fausto's own risks (its database, its access control) belong to fausto,
  not to this register.
