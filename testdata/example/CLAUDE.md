# Working on Habbyte

Habbyte's architecture spec (components and layers, principles, decisions,
domains, roadmap, requirements, events, risks, and the agents' charters)
lives in [fausto](https://github.com/JuanVizcaya/fausto), not in this
repository (see ADR 0006). Read the parts your change touches before
changing code:

- Started by `fausto claude <agent>`, or after `fausto spec pull`, you have
  it in `system/`, which git ignores here: never commit it. The
  `fausto_spec` tool reads it too.
- People read it, and propose changes to it, in fausto's UI.

What it says:

- `system/system.yaml`: components, layers, dependencies, technologies, and
  each component's owner.
- `system/principles.yaml`: implementation rules every change follows.
- `system/agents/<you>.yaml`: your charter, the paths you own, and your
  rules. Don't change files that another agent owns; describe the change you
  need in your PR instead.
- `system/decisions/`: architecture decision records (ADRs).
- `system/requirements.yaml`, `system/events.yaml`, `system/risks.yaml`:
  non-functional requirements, asynchronous events (publish or consume only
  those declared), and known risks. If your work creates or removes a risk,
  say so in your PR.

## Changing the spec

If a change adds a service, contract, database, or cross-component
dependency, the spec has to declare it: say so in your PR, and the
orchestrator proposes it in fausto. Don't edit `system/` here; it is only a
copy.

## Before finishing

Run the drift checks (`fausto check`, or the `fausto_check` tool) and fix what
they report in your area; fausto also checks every pull request and reports
it as `fausto/check`. Then run the tests, with the Postgres integration
tests:

```sh
docker compose --profile test run --rm --build test
```

and, if you changed `proto/`, lint the contracts:

```sh
docker run --rm -v "$PWD":/workspace -w /workspace bufbuild/buf:1.73.0 lint
```
