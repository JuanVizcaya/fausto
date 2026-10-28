# Agent tasks

Tasks no longer live here. fausto keeps them in its database, and mirrors
them as Jira issues (with the task's .md attached) when Jira is configured.

- People see and update their tasks in fausto's Tasks tab.
- An agent downloads its open tasks with `fausto task pull <agent>`, which
  writes them to the git-ignored `.fausto/tasks/`, and reports progress
  with `fausto task start <id>` and `fausto task done <id>`.

See fausto/README.md.
