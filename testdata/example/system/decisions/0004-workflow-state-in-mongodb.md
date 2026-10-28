---
id: "0004"
title: Proposals and tasks live in a shared MongoDB, mirrored to Jira
status: superseded
date: 2026-10-13
---

## Context

fausto kept its workflow state in git: each proposal was a `proposal/<id>`
branch recorded in `system/proposals/`, and each task a file in `tasks/`
whose status agents edited in their branches. Every proposal, task, status
change, and Jira link was a commit on the main repository, and tasks also
needed to be followed in Jira.

## Decision

Git holds only the spec and the code. Proposals and tasks live in a MongoDB
database shared by the team, behind fausto's `store.Store` interface:

- A proposal stores the edits it makes to the spec. It is reviewed by
  applying them to the base branch as it is now, and accepting it commits
  just the spec change.
- Tasks are created on accept. Agents download theirs with
  `fausto task pull <agent>` into the git-ignored `.fausto/tasks/` and report
  progress with `fausto task start|done`.
- When configured, Jira mirrors every task: an issue with the task's `.md`
  attached, kept at the task's status. The database stays the source of truth.

## Consequences

- Accepting a proposal is the only workflow event that commits, and the
  commit touches only `system/` (and generated agent definitions).
- Task history (who changed a status, when) lives in the database, not in
  git, and needs backing up with it.
- Editing in fausto, and the agents' `fausto task` commands, need the
  database (`FAUSTO_MONGO_URI`); `fausto check` and CI don't.
- `fausto migrate` moves an existing `tasks/` and `system/proposals/` into the
  database once.
