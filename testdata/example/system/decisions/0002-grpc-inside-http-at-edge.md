---
id: "0002"
title: gRPC between services, HTTP/JSON at the edge
status: accepted
date: 2026-10-02
affects: [api, residents, web]
---

## Context

The React frontend needs a browser-friendly API, while internal services need
typed, versioned contracts.

## Decision

Domain services expose only gRPC, defined in `proto/habyta/<domain>/v1`. The
`api` gateway is the only public entry point. It serves HTTP/JSON with
hand-written types and calls domain services over gRPC.

## Consequences

The public API can change without breaking internal contracts, and vice versa.
Each new public endpoint needs a gateway handler.
