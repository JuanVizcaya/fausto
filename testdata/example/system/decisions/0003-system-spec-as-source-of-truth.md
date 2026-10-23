---
id: "0003"
title: system/ is the source of truth for architecture
status: accepted
date: 2026-10-05
---

## Context

Several agents implement parts of Habbyte in parallel, orchestrated by a human
who reviews design at a high level.

## Decision

Components, layers, principles, technology choices, and agent ownership are
declared in structured files under `system/`. Changes to them go through pull
requests. `fausto check` runs in CI and fails when code drifts from the spec.

## Consequences

New services, contracts, and cross-component dependencies must be declared in
`system/` before or with the code that adds them.
