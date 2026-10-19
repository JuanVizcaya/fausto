---
name: work-fausto-tasks
description: Use when working fausto tasks in a project fausto supervises (it has system/system.yaml and a .fausto/ connection), or when the user asks to pick up, start, finish or report on fausto tasks.
---

# Working fausto tasks

Fausto keeps the project's architecture spec in `system/` and hands work to
agents as tasks. Each task says why it exists (an accepted, merged proposal),
which spec changes concern you, and which drift checks to fix.

## The loop

1. **Get your tasks**: call `fausto_tasks`. Work them oldest first; their ids
   start with the date. Without the MCP tools, run `fausto task pull` (it
   writes them to `.fausto/tasks/`, which git ignores; never commit them).
2. **Start one**: `fausto_task_start` with its id (or `fausto task start <id>`).
3. **Read before changing**: `CLAUDE.md`, your scope and rules, and the parts
   of `system/` the task touches: `system.yaml` (components, layers,
   dependencies), `principles.yaml`, `decisions/`, `requirements.yaml` and
   `events.yaml`.
4. **Change only what you own.** If the task needs a change in another
   agent's paths or in `system/`, don't make it; say what you need in your
   summary, so the orchestrator can propose it.
5. **Check**: `fausto_check` (or `fausto check`), and fix what it reports in
   your area. Findings elsewhere aren't yours to fix; mention them.
6. **Finish**: commit on your branch with the task id in the message, then
   `fausto_task_done` (or `fausto task done <id>`). Drop a task that turns out
   not to need doing with `fausto_task_drop`, and say why.
7. **Summarize**: what you did for each task, what you couldn't do and why,
   and the changes you need from other agents or the orchestrator.

## When the tools say you aren't connected

The clone needs an agent token. The orchestrator creates one with
`fausto token create <agent>` and starts you with `fausto claude <agent>`,
which connects your work tree; or connect it yourself with
`fausto connect -token <token>`. Don't look for the database: agents reach
fausto only through its API.
