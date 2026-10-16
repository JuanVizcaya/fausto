# Fausto

Fausto keeps a project's architecture spec in sync with its code. The spec
is a `system/` directory in the project's repository: components and layers
(`system.yaml`), implementation principles, the technology radar, domains,
agent charters with file ownership, and decisions (ADRs). Fausto checks the
code against it, shows it in a web UI, turns edits into reviewed proposals,
and hands the resulting work to agents as tasks.

Fausto is a separate tool: a project doesn't depend on it to build or run,
and fausto works on a project only through its repository. It was first
built for [Habyta](https://github.com/CMOT/habyta); `testdata/example` is a
snapshot of that project, used by the tests.

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

Verifies that the code matches the spec. CI runs it on every pull request:

```sh
fausto check          # or: -json, or name checks
```

Checks: `schema`, `services`, `layers`, `db-ownership`, `contracts`,
`ownership`, and `runtime`, which enforces the Compose rules (health checks,
memory limits, hardening, exposure) that `system/requirements.yaml` asks for.

The checks expect Go commands in `backend/cmd/<name>`, the Compose file at
`compose.yaml` and protobuf packages under `proto/`. A project laid out
differently says so in an optional `.fausto.yaml` at its root:

```yaml
commands_dir: cmd
compose_file: deploy/compose.yaml
proto_root: api/proto
```

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
docker compose exec fausto fausto project add habyta -path /projects/habyta    # see Projects
docker compose restart fausto
```

Fausto only listens on localhost and has no login. Without the database, or
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
fausto project add habyta -path ../habyta           # a local clone
fausto project add habyta -github owner/habyta      # a repository on GitHub
fausto project list
fausto project rm habyta                            # keeps its proposals and tasks
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
fausto task pull residents   # writes .fausto/tasks/<id>.md (git-ignored)
fausto task start <id>       # open → in-progress
fausto task done <id>        # → done (or `drop`)
fausto task list -agent residents -status open,in-progress
```

`pull` also removes the files of the agent's tasks that are no longer open.
Each status change records who made it (`FAUSTO_AUTHOR`, else git's
identity), and the Tasks tab shows that history.


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

## Running agents

Each agent charter in `system/agents/` of kind `agent` has a generated
Claude Code agent definition in `.claude/agents/<id>.md`: its scope, owned
paths and components, rules, the principles, and how to work its tasks.
Proposals regenerate them when charters change; `fausto agents` does it by
hand, and the `agents` check fails when they are stale. Don't edit them.

To work an agent's open tasks, give it its own branch and work tree, pull
its tasks, start it, then review and merge (the Tasks tab shows these
commands per task). The agent needs `FAUSTO_MONGO_URI` in its environment:

```sh
git worktree add ../myproject-residents -b agent/residents
cd ../myproject-residents
fausto task pull residents
claude --agent residents "Work your open tasks."
# back in your checkout:
git diff dev...agent/residents && git merge agent/residents
```

Run one session per agent; separate work trees let them work in parallel.

## Code owners

`fausto agents` also writes `.github/CODEOWNERS` from the charters, so GitHub
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
GitHub release and the image `ghcr.io/juanvizcaya/fausto:vX.Y.Z` (and
`:latest`).
