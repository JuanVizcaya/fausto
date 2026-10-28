---
id: "0006"
title: The spec lives in fausto
status: accepted
supersedes: "0003"
date: 2026-10-20
---

## Context

ADR 0003 made `system/` in the repository the source of truth for the
architecture, and ADR 0005 kept it there when fausto became an external
tool. That left fausto reading the spec from whatever was checked out in a
working copy, while proposals were built from a branch, so what people saw
and what proposals changed could disagree, and every spec change still went
through a commit to Habbyte.

## Decision

The spec lives in fausto's database, not in this repository:

- fausto keeps it as numbered revisions; every applied proposal adds one,
  and any revision can be read, compared, exported or restored.
- People read and change the spec in fausto's UI, through proposals.
  Agents get it from fausto: `fausto claude` writes it to `system/` in the
  agent's work tree, which git ignores there, and the `fausto_spec` tool
  reads it.
- Pull requests are checked against the current revision by fausto itself,
  which reports a `fausto/check` status and a comment on each; the check
  workflow is no longer used here.

This supersedes 0003. The rest of 0005 stands: fausto is an external tool,
and Habbyte reaches it only through this repository.

## Consequences

- This repository holds code only; it can't be understood without fausto,
  until `fausto spec export` writes `system/` back.
- Drift checks run only while fausto runs, and need a GitHub token for
  CMOT/habyta in fausto's settings.
