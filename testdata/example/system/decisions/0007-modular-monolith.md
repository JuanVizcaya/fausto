---
id: "0007"
title: A modular monolith for the MVP
status: accepted
date: 2026-10-22
supersedes: "0002"
affects: [habbyte, api, platform, postgres, keycloak, caddy]
---

## Context

The architecture design (22 October 2026) replaces the separate residents
and administrators services. Habbyte has no revenue yet, so infrastructure
cost comes first; a team of three plus agents can't operate a dozen
deployments; and modules must still be extractable into services later
without a rewrite.

## Decision

The MVP is one Go binary, `habbyte`, that runs the public API and 14
modules in one process: the core (`iam`, `developments`, `control`),
products (`finance`, `payments`, `invoicing`, `community`, `amenities`,
`operations`, `accesscontrol`, `devices`), and shared capabilities
(`notifications`, `files`, `audit`).

- A module is a bounded context. It exposes a Go `contract` package, the
  only package other modules import, and calls are in-process function
  calls. Once a module is extracted, the same interface is a gRPC client.
- Each module owns one schema in a single Postgres 18 server, which also
  holds Keycloak's database. No joins or foreign keys across schemas
  (amends ADR 0001's one database per service).
- Side effects travel as protobuf events through a per-module outbox.
- One public HTTP/JSON API at the edge, behind Caddy, with Keycloak for
  sign-in only; iam owns roles and permissions.

In the spec, the modules and the API declare `runtime.host: habbyte`, each
module declares its Go contract, module-to-module calls are `contract`
dependencies, and each module's sql dependency names its schema.

This supersedes ADR 0002: HTTP/JSON at the edge stands, and gRPC between
services waits for the first extraction.

## Consequences

- One deployment and one database to run and back up; a module extraction
  changes wiring and deployment, not domain code.
- Boundaries are kept by code, not the network: `internal/archtest` in Go
  tests, and fausto's layers check on contract imports.
- The modules share one process, so a crash or a slow query in one affects
  all of them until they are extracted.
