---
id: "0001"
title: Use Postgres for now, behind store interfaces
status: accepted
date: 2026-10-03
affects: [postgres, platform]
---

## Context

The residents service needs durable storage. The long-term database is not
settled yet.

## Decision

Use Postgres 18, one database per service. Each service accesses it only
through a `store` interface (for residents, `backend/internal/residents/store.go`).
Schema changes are numbered goose migrations embedded in the service.

## Consequences

Switching databases means writing another `store` implementation, not
rewriting service logic. Each service must ship its own migrations.
