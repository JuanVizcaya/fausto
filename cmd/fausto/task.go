package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// pulledDir is where `fausto task pull` writes tasks, relative to the
// repository root; git ignores it through .git/info/exclude.
const pulledDir = ".fausto/tasks"

// tasks is where the task commands get and change tasks: fausto's API with
// an agent's token (what agents use), or the database directly.
type tasks interface {
	list(ctx context.Context, f store.TaskFilter) ([]store.Task, error)
	// setStatus moves a task to status and returns the status it had.
	setStatus(ctx context.Context, id, status string) (string, error)
}

// apiTasks are one agent's tasks, through fausto's API.
type apiTasks struct {
	c  *client.Client
	me *client.Me
}

func (a apiTasks) list(ctx context.Context, f store.TaskFilter) ([]store.Task, error) {
	if f.Agent != "" && f.Agent != a.me.Agent {
		return nil, fmt.Errorf("this clone's token is for agent %s, not %s", a.me.Agent, f.Agent)
	}
	return a.c.Tasks(ctx, f.Statuses...)
}

func (a apiTasks) setStatus(ctx context.Context, id, status string) (string, error) {
	return a.c.SetStatus(ctx, id, status)
}

// dbTasks are a project's tasks in the database, changed on behalf of by,
// and mirrored in Jira when it is configured.
type dbTasks struct {
	db store.Store
	by string
}

func (d dbTasks) list(ctx context.Context, f store.TaskFilter) ([]store.Task, error) {
	return d.db.Tasks(ctx, f)
}

func (d dbTasks) setStatus(ctx context.Context, id, status string) (string, error) {
	before, err := store.SetStatus(ctx, d.db, id, status, d.by)
	if err != nil {
		return "", err
	}
	// Mirror it now if Jira is configured; otherwise the next sync does.
	if jc, err := jiraClient(); err == nil && jc != nil {
		report, err := jira.SyncStore(ctx, jc, d.db, []string{id}, false)
		if err == nil && report.Failed() > 0 {
			err = errors.New(report.Results[0].Error)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: Jira not updated (the next sync will): %v\n", err)
		}
	}
	return before.Status, nil
}

// openTasks picks where the task commands work: fausto's API when this
// clone is connected (or FAUSTO_TOKEN is set), else the database.
func openTasks(ctx context.Context, root, project string) (tasks, func(), error) {
	c, err := client.Resolve(root)
	if err != nil {
		return nil, nil, err
	}
	if c != nil {
		me, err := c.Me(ctx)
		if err != nil {
			return nil, nil, err
		}
		return apiTasks{c, me}, func() {}, nil
	}
	if os.Getenv("FAUSTO_MONGO_URI") == "" {
		return nil, nil, errors.New("not connected to fausto: run `fausto connect -token <token>` (from `fausto token create <agent>`), or set FAUSTO_URL and FAUSTO_TOKEN")
	}
	reg, err := openDB(ctx)
	if err != nil {
		return nil, nil, err
	}
	id, err := resolveProject(ctx, reg, project, root)
	if err != nil {
		reg.Close(context.Background())
		return nil, nil, err
	}
	return dbTasks{reg.Store(id), proposal.Who(root, os.Getenv("FAUSTO_AUTHOR"))}, func() { reg.Close(context.Background()) }, nil
}

func runTask(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("task "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	project := projectFlag(fs)
	agent := fs.String("agent", "", "only this agent's tasks")
	statuses := fs.String("status", "", "only tasks with these statuses, comma-separated")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	_ = resolveRoot(root) // tasks can be listed and set from anywhere
	ts, done, err := openTasks(ctx, *root, *project)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer done()

	switch sub {
	case "list":
		f := store.TaskFilter{Agent: *agent}
		if *statuses != "" {
			f.Statuses = strings.Split(*statuses, ",")
		}
		list, err := ts.list(ctx, f)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, t := range list {
			fmt.Fprintf(stdout, "%-11s %-14s %-9s %s\n", t.Status, t.Agent, t.Jira, t.ID)
		}
		return 0

	case "pull":
		if fs.NArg() > 1 {
			fmt.Fprintln(stderr, "usage: fausto task pull [-root dir] [agent]")
			return 2
		}
		if *root == "" {
			fmt.Fprintln(stderr, "no "+spec.Dir+"/system.yaml found in this or any parent directory; run it in the project's clone, or pass -root")
			return 2
		}
		who := fs.Arg(0)
		if a, ok := ts.(apiTasks); ok && who == "" {
			who = a.me.Agent
		}
		if who == "" {
			fmt.Fprintln(stderr, "usage: fausto task pull [-root dir] <agent>")
			return 2
		}
		return pullTasks(ctx, ts, *root, who, stdout, stderr)

	case "start", "done", "drop":
		if fs.NArg() != 1 {
			fmt.Fprintf(stderr, "usage: fausto task %s <task>\n", sub)
			return 2
		}
		status := map[string]string{"start": "in-progress", "done": "done", "drop": "dropped"}[sub]
		id := fs.Arg(0)
		from, err := ts.setStatus(ctx, id, status)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", id, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %s → %s\n", id, from, status)
		return 0
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// pullTasks writes agent's open and in-progress tasks to .fausto/tasks/
// under root, and removes the files of its tasks that are no longer open.
func pullTasks(ctx context.Context, ts tasks, root, agent string, stdout, stderr io.Writer) int {
	tasks, err := ts.list(ctx, store.TaskFilter{Agent: agent, Statuses: []string{"open", "in-progress"}})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dir := filepath.Join(root, filepath.FromSlash(pulledDir))
	if err := client.Exclude(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	keep := map[string]bool{}
	for _, t := range tasks {
		name := t.ID + ".md"
		keep[name] = true
		if err := os.WriteFile(filepath.Join(dir, name), t.Markdown(true), 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%-11s %s/%s\n", t.Status, pulledDir, name)
	}
	// Files of this agent's tasks that were closed since the last pull.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if keep[name] || !strings.HasSuffix(name, "--"+agent+".md") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			fmt.Fprintf(stdout, "%-11s %s/%s\n", "removed", pulledDir, name)
		}
	}
	if len(tasks) == 0 {
		fmt.Fprintf(stdout, "No open tasks for %s.\n", agent)
	}
	return 0
}
