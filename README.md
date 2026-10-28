# Fausto

Fausto keeps a project's architecture spec in sync with its code. The spec
is a `system/` directory in the project's repository: components and layers
(`system.yaml`), implementation principles, the technology radar, domains,
agent charters with file ownership, and decisions (ADRs). Fausto checks the
code against it, shows it in a web UI, turns edits into reviewed proposals,
and hands the resulting work to agents as tasks.

Fausto is a separate tool: a project doesn't depend on it to build or run,
and fausto works on a project only through its repository. It was first
built for [Habbyte](https://github.com/Ceferinto/habbyte); `testdata/example` is a
snapshot of that project, a modular monolith, used by the tests, and
`testdata/services` an earlier one, as separate gRPC services.

Module `github.com/JuanVizcaya/fausto`, Go 1.27. Install the CLI, then run it
from anywhere inside a project's clone (it finds the nearest parent
directory with `system/system.yaml`; `-root` or `FAUSTO_ROOT` overrides):

```sh
go install github.com/JuanVizcaya/fausto/cmd/fausto@latest
fausto check    # drift checks
fausto serve    # web UI
fausto agents   # regenerate .claude/agents/*.md and CODEOWNERS from charters
```

Or without Go, from the project's root:

```sh
docker run --rm -v "$PWD":/repo ghcr.io/juanvizcaya/fausto check
```

The JSON Schemas for the spec files are part of fausto (`internal/schema`),
not of the project.

## `fausto check`

Verifies that the code matches the spec:

```sh
fausto check          # or: -json, -github, or name checks
```

Checks: `schema`, `services`, `layers`, `db-ownership`, `contracts`,
`ownership`, `runtime`, which enforces the Compose rules (health checks,
memory limits, hardening, exposure) that `system/requirements.yaml` asks for,
and `agents`, which keeps the files generated from the charters current.

The checks expect Go commands in `backend/cmd/<name>`, the Compose file at
`compose.yaml` and protobuf packages under `proto/`. A project laid out
differently says so in an optional `.fausto.yaml` at its root:

```yaml
commands_dir: cmd
compose_file: deploy/compose.yaml
proto_root: api/proto
```

or passes `-commands-dir`, `-compose-file` and `-proto-root`.

### Project checks

A project's own rules are often enforced by its own tests and linters. It
declares those commands in `system/checks.yaml`, and its principles name them
next to the built-in checks, or name `review` when people enforce them:

```yaml
# system/checks.yaml
checks:
  - id: module-boundaries
    title: Module boundaries hold (internal/archtest).
    run: go test ./internal/archtest/
    dir: backend        # relative to the repository root; default: the root
    timeout: 5m         # default: 10m

# system/principles.yaml
principles:
  - id: contracts-only
    title: Modules meet only through contracts
    rationale: ...
    checks: [layers, module-boundaries]
```

A command is the project's code, so `fausto check` runs it only with
`-commands`: in CI, and in an agent's clone (the `fausto_check` tool's
`commands` argument). Without it, and always on fausto's server (the web UI
and `fausto/check` on pull requests), project checks are listed as skipped.
A failed command's finding keeps the end of its output; with `-commands`, the
whole output also goes to stderr.

### A modular monolith

A component can run inside another one's process, as a module does in a
modular monolith's binary:

```yaml
- id: payments
  kind: service
  layer: modules
  paths: [backend/internal/modules/payments]   # the first path is its package
  runtime: {host: habbyte}                     # no cmd or compose of its own
  contracts:
    - go: backend/internal/modules/payments/contract
  depends_on:
    - {target: files, protocol: contract}      # an in-process call through files' Go contract
    - {target: postgres, protocol: sql, schema: payments}
```

The host's command must import each active module's package; other
components may import only its `go` contract, with a `contract` dependency,
which works only within one process. On a database server that several
components share, each sql dependency names the `schema` it owns, and each
schema has one owner. A component run from an upstream image, such as a
proxy, declares `runtime.image` (`caddy`, or `quay.io/keycloak/keycloak`),
and its Compose service must run that image. Ports bound to loopback don't
count as published.

### In a project's CI

A project runs the checks on its pull requests with one workflow file, and
disconnects from fausto by deleting it:

```yaml
# .github/workflows/fausto.yml
name: fausto
on:
  pull_request:
  push:
    branches: [main]
jobs:
  check:
    uses: JuanVizcaya/fausto/.github/workflows/check.yml@v0
    # with:
    #   checks: schema layers        # default: every check
    #   working-directory: .         # where system/ is
    #   version: v0.1.0              # a fausto release; default: the latest
```

It installs the fausto release, runs `fausto check -github`, which annotates
each finding on its file in the pull request, and writes a summary on the
run. CI has no access to fausto's database, so for charters fausto keeps the
`ownership` and `agents` checks are skipped; `fausto serve` runs them. `@v0`
follows the latest v0 release; pin a version for repeatable runs.

## `fausto serve`

Opens fausto at http://127.0.0.1:7070: architecture and layer diagrams, the
roadmap (`system/roadmap.yaml`: milestones, dependencies, exit criteria, and
linked tasks), quality requirements by component (`system/requirements.yaml`),
the risk register (`system/risks.yaml`), event contracts
(`system/events.yaml`), the technology radar, principles, decisions, agents,
domains, proposals, tasks, and live check results. It rereads `system/` on
every refresh.

Agents set their own tasks' status with `fausto task`. A person sets the
status of a task assigned to a `kind: human` charter in the Tasks tab.

To run fausto and its database in Docker (no Go install needed), from this
repository:

```sh
cp .env.example .env    # once; set FAUSTO_MONGO_PASSWORD, FAUSTO_AUTHOR (and FAUSTO_UID/GID on Linux)
docker compose up -d --build    # http://127.0.0.1:7070
docker compose exec fausto fausto project add habbyte -path /home/you/src/habbyte    # its path on your machine; see Projects
docker compose restart fausto
```

Fausto only listens on localhost and has no login for people; its agent API takes a token per agent (see [Tasks](#tasks)). Without the database, or
without `FAUSTO_AUTHOR` when git has no identity, it starts read-only; a
banner at the top says why and how to fix it.

The Architecture tab draws the components in their layers, with each layer's
dependency rule under its name and the bounded contexts as coloured dashed
outlines; contexts with no components yet are small tags beside them. Click a
component, a layer name or a context for its details in the side panel, which
otherwise shows an overview. "Highlight" colours components by context or
fades those that don't use a chosen technology. The Technology and Glossary
sub-views hold the technology radar and the shared vocabulary.

The Architecture tab also drills down into the code, read-only, in the
spirit of the C4 model: click a component's ⤵ (or "Open the code") to see its
Go packages, their imports, its gRPC contract and its database; click a
package for a UML class diagram of its types (implements, embeds, field and
signature relations); click a function or method for its callers and calls,
followed through interfaces to their implementations, with its source. Fausto
reads the code with `go/parser`, without compiling, so relations and calls
are matched by name and shape and are a guide, not a proof.

## Projects

Fausto works on any number of projects, each a git repository with a
`system/` spec, and works on each only through its repository: nothing in
a project depends on fausto, and removing fausto leaves it as it was.
Register them once:

```sh
fausto project add habbyte -path ../habbyte         # a local clone
fausto project add habbyte -github owner/habbyte    # a repository on GitHub
fausto project list
fausto project rename old-id new-id                 # its proposals, tasks, spec and tokens follow
fausto project rm habbyte                           # keeps its proposals and tasks
```

`fausto serve` serves every registered project, with a picker in the top
bar; `fausto serve -root <clone>` serves just that clone, registered or not.
Commands that work on one project (`fausto task`, `fausto jira sync`) take
`-project`, or work it out from the clone they run in.

For a local clone, accepting a proposal creates a branch in the clone. For
GitHub, fausto keeps its own clone (in `FAUSTO_DATA`, by default the user
cache directory), pushes the branch and opens a pull request; it needs
`FAUSTO_GITHUB_TOKEN`, a fine-grained token with read and write access to
the repository's contents and pull requests. The token is only read from the
environment and is never written to the clone or logged.

## Storage

A project's git holds only its spec and code. Projects, proposals and the
tasks they create live in fausto's own MongoDB database, deployed with
fausto by `compose.yaml` and reachable only by it; each project's proposals
and tasks are in collections of their own (`<project>.proposals`,
`<project>.tasks`). Creating a proposal or a task, or changing a task's
status, touches no repository.

To develop fausto, run only the database, published on localhost, and fausto
from source:

```sh
docker compose -f compose.yaml -f compose.dev.yaml up -d mongo
export FAUSTO_MONGO_URI='mongodb://fausto:<FAUSTO_MONGO_PASSWORD>@localhost:27017/?authSource=admin'
go run ./cmd/fausto serve -root ../your-project
```

A database from before projects keeps its proposals and tasks in
`proposals` and `tasks`; `fausto project add <id> ... -adopt` gives them to
that project.

`fausto check` and `fausto agents` don't need the database, so CI doesn't
either. The connection string is read only from the environment.
## Tasks

An agent works from its tasks as Markdown files:

```sh
fausto task pull             # writes .fausto/tasks/<id>.md (git ignores it)
fausto task start <id>       # open → in-progress
fausto task done <id>        # → done (or `drop`)
fausto task list -status open,in-progress
```

`pull` also removes the files of the agent's tasks that are no longer open.
Each status change records who made it, and the Tasks tab shows that history.

Agents reach their tasks through fausto's API, never the database. Each agent
gets a token, good for that agent of that project only; fausto keeps just
its hash:

```sh
fausto token create residents   # in your clone (needs the database); prints the token once
fausto token list
fausto token rm <label>         # revoke
fausto connect -token <token>   # in a clone: work as that agent from it
```

`fausto connect` keeps the connection in `.fausto/config.json` (or use
`FAUSTO_URL` and `FAUSTO_TOKEN`), and git ignores `.fausto/` through
`.git/info/exclude`, so the project's `.gitignore` never mentions fausto.
With docker compose, run the token commands where the database is:
`docker compose exec fausto fausto token create -project habbyte residents`.


## Jira

Fausto can mirror every task as a Jira Cloud issue. The database stays the
source of truth: fausto creates the issue, attaches the task's `.md` (what the
agent works from), records the issue's key on the task, and moves the issue
to the Jira status mapped from the task's status. Changes made in Jira are
never copied back.

Set these (in `.env` when using docker compose), then restart fausto:

```sh
FAUSTO_JIRA_URL=https://your-site.atlassian.net
FAUSTO_JIRA_EMAIL=you@example.com
FAUSTO_JIRA_TOKEN=...             # https://id.atlassian.com/manage-profile/security/api-tokens
FAUSTO_JIRA_PROJECT=HAB
# FAUSTO_JIRA_ISSUE_TYPE=Task
# FAUSTO_JIRA_STATUSES="open=To Do,in-progress=In Progress,done=Done,dropped=Done"
```

The token is read only from the environment and is never logged or sent to
the browser. Without `FAUSTO_JIRA_URL` the integration is off.

A sync runs when a proposal is applied (for its new tasks), when a task's
status changes (in the Tasks tab, or with `fausto task start|done|drop` when
Jira is configured there), and on demand with "Sync with Jira" in the Tasks
tab or from the command line:

```sh
fausto jira sync -dry-run    # show what it would do
fausto jira sync             # all tasks, or name task ids
```

Each issue carries the label `fausto-task-<task id>`: a sync finds an issue by
that label before creating one, so retrying after a failure never creates
duplicates. A sync also attaches `<task id>.md` to any issue missing it; the
attachment leaves out the status, which Jira shows live. Applying a proposal
still succeeds when Jira is unreachable; the next sync catches up.

## Changing the spec

Changes to the spec go through proposals, kept in the database:

1. Edit items in fausto (Edit / Add buttons). Edits collect in a draft
   in your browser. The Architecture diagram is itself an editor: drag a
   component to another layer, drag its + handle onto another component to
   add a dependency, click an arrow to change or remove it, double-click a
   component to edit it or empty layer space to add one, and click a layer's
   name to edit the layer. "Add database" on a service adds a database it
   owns. The diagram marks draft changes. Renaming or deleting a component or
   layer offers to update everything that refers to it.
2. "Review and propose" records the edits as a proposal, after checking they
   apply and pass the schema check. Nothing is committed; your work tree is
   untouched.
3. The proposal page applies the edits to the base branch as it is now and
   shows the spec changes, the diff, the checks on the result, and the task
   each affected agent will get. If an item it changes was changed on the
   base branch since, it says so and can't be accepted: propose it again.
4. Accept puts the spec change, as one commit on top of the base branch, on
   a branch of its own, `fausto-proposal/<id>`, and on GitHub opens a pull
   request for it. Fausto never commits to the base branch: people review
   and merge the change like any other. The proposal is then accepted,
   waiting for the merge. Reject marks it rejected; it stays listed and
   reviewable.
5. Once the change is in the base branch (merged, squashed or rebased), the
   proposal is applied and each affected agent gets its task. Fausto checks
   accepted proposals every minute (`-poll`), when you open the Proposals
   tab, and on "Check for merge". A pull request closed without merging
   withdraws the proposal, and its tasks are never created.

The proposal commit uses git's identity unless you pass `-author "Name <email>"`.

## Where the spec lives

A project starts with its spec in its repository, `system/`. Fausto can keep
it instead, in its database, so the project's repository holds only code:

```sh
fausto spec import              # in the project's clone: system/ becomes revision 1
fausto spec log                 # the revisions: who, when, which proposal
fausto spec export -root <dir>  # write the latest (or -revision n) back to system/
```

Once fausto keeps the spec it reads only its own; `system/` can be removed
from the repository. The spec is still changed through proposals, and a spec
change is in no commit: applying a proposal re-applies its edits to the
latest revision and adds the next one, right away when nothing in the
repository (such as CODEOWNERS) needs merging. Because edits are re-applied,
proposals that change different items of one file all land; one whose items
changed since it was made is stale. The Health tab shows the history, each
revision's diff, and restores an earlier revision as a new one.

Agents read the spec as files: `fausto spec pull`, which `fausto claude` and
`fausto task pull` run, writes it to `system/` in the agent's work tree,
which git ignores there. The `fausto_spec` MCP tool reads it too. With
`FAUSTO_MONGO_URI` set, or in a connected clone, `fausto check` and
`fausto agents` use the spec fausto keeps.

To leave fausto, `fausto spec export` and commit `system/`.

### Pull request checks

A project's CI can't read fausto's database, so for a spec fausto keeps,
fausto checks the pull requests itself. While `fausto serve` runs, every
poll (`-poll`, a minute by default), it checks each open pull request not
yet checked at the latest spec revision, in its own clone (the project's is
never touched), and reports a `fausto/check` commit status and one comment
on the pull request, which it updates. A new revision re-checks every open
pull request.

It needs the project's GitHub repository (`-github owner/repo` on
`fausto project add`, or the clone's `origin`) and `FAUSTO_GITHUB_TOKEN`: a
fine-grained token on that repository with commit statuses and pull requests
(read and write) and contents (read). For a spec in the repository, use the
check workflow instead (see [In a project's CI](#in-a-projects-ci)).

### Handing pull requests to the project's workflows

fausto's server never runs a pull request's code, so project checks, and
reviewers that read the code against the spec, run in the project's CI. That
CI can't read fausto's database either, so with `FAUSTO_PR_HANDOFF=true`,
before reporting on a pull request fausto also:

1. publishes the spec revision it checked against as a hidden ref,
   `refs/fausto/spec/r<N>`: a commit holding only `system/`, never a branch
   and never merged, the same commit every time for the same revision;
2. sends a `repository_dispatch` event, `fausto-spec`, with
   `{pr, head_sha, revision, spec_ref}`.

A workflow on that event checks out `head_sha`, fetches the ref into
`system/`, and reports its own commit status (see the check workflow's
`spec-ref` input). The token also needs contents (read and write). If the
hand-off fails, fausto reports nothing and tries the pull request again on
the next poll.

## Running agents

Each agent charter of kind `agent` has a generated Claude Code agent
definition in `.claude/agents/<id>.md`: its scope, owned paths and
components, rules, the principles, and how to work its tasks. For charters
in the repository, proposals regenerate them when charters change, and the
`agents` check fails when they are stale; don't edit them. For charters
fausto keeps, the definitions aren't part of the project: fausto serves them.

### An auditor

A charter of kind `auditor` owns no paths and no components: it reviews.
Its generated definition lists the principles marked `review` (or with no
checks) in full, the others with the checks that cover them, and has it
review a diff against them, the accepted decisions, charter ownership, and
the declared components, events and risks. Each finding cites the rule it
breaks and is marked blocking or advisory; it never changes files.

```yaml
# system/agents/auditor.yaml
id: auditor
name: Auditor
kind: auditor
scope: Reviews every pull request against the spec before people do.
```

`fausto agents -print auditor` writes the definition to stdout, so a pull
request's workflow can hand it to Claude Code as the reviewer's prompt; with
`-spec-from`, it reads the handed-off spec ref (see
[Handing pull requests to the project's workflows](#handing-pull-requests-to-the-projects-workflows)).

To work an agent's open tasks, start it from your clone:

```sh
fausto token create residents   # once
fausto claude residents         # work tree ../<project>-residents, branch agent/residents
# then review and merge:
git diff main...agent/residents && git merge agent/residents
```

`fausto claude` creates the agent's work tree and branch the first time,
connects it with the agent's token, pulls its tasks, and starts Claude Code
with the agent's definition and fausto's MCP tools, both from fausto
(`claude --agents … --agent residents --mcp-config …`): nothing about the
agent is written to the project. Arguments after `--` go to Claude Code in
place of the "Work your open tasks." prompt. Run one session per agent;
separate work trees let them work in parallel. The Tasks tab shows these
commands per task.

## Claude Code plugin

This repository is also a Claude Code plugin marketplace. The `fausto`
plugin gives any session in a connected clone fausto's MCP tools
(`fausto_tasks`, `fausto_task_start`, `fausto_task_done`, `fausto_task_drop`,
`fausto_check`, through `fausto mcp`) and two skills, for working tasks and
for running the drift checks. Install it once, with `fausto` on your `PATH`:

```text
/plugin install fausto --marketplace JuanVizcaya/fausto
```

`fausto claude` doesn't need it: it hands the agent the MCP tools itself.

## Code owners

`fausto agents` writes `.github/CODEOWNERS` from the charters, so GitHub
asks the right people to review each change. A charter's `reviewers` lists
GitHub users (`@name`) or teams (`@org/team`); every path it `owns` goes to
them. A charter without reviewers, typically an agent's, goes to the
reviewers of every human charter, since people review what agents write.
Without any reviewers in any charter, fausto writes no CODEOWNERS and leaves a
hand-written one alone.

GitHub applies a CODEOWNERS file's last matching line, so fausto lists
directories from shortest to longest, then files: a file gets the same owner
as the `ownership` check gives it. The `agents` check fails when the file is
out of date. To make the reviews required, turn on "Require review from Code
Owners" in the branch protection rules; GitHub doesn't let authors approve
their own pull requests, so a sole reviewer needs a second person or an admin
bypass to merge their own changes.

## The web UI

The UI source is in `web` (React + Vite); after changing it, run
`npm run build` there and commit the regenerated `internal/server/dist`.

## Releases

Tag `vX.Y.Z` to release: the `release` workflow publishes binaries on the
GitHub release and the image `ghcr.io/juanvizcaya/fausto:vX.Y.Z` (and `:vX`,
`:latest`), and moves the tag `vX` to it, which projects' CI calls the check
workflow at. The `check workflow` workflow runs that workflow on
`testdata/example` with fausto built from each commit.
