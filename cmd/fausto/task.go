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

	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// pulledDir is where `fausto task pull` writes tasks, relative to the
// repository root; it is git-ignored.
const pulledDir = ".fausto/tasks"

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
	reg, err := openDB(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer reg.Close(context.Background())
	_ = resolveRoot(root) // tasks can be listed and set from anywhere
	id, err := resolveProject(ctx, reg, *project, *root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	db := reg.Store(id)

	switch sub {
	case "list":
		f := store.TaskFilter{Agent: *agent}
		if *statuses != "" {
			f.Statuses = strings.Split(*statuses, ",")
		}
		tasks, err := db.Tasks(ctx, f)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, t := range tasks {
			fmt.Fprintf(stdout, "%-11s %-14s %-9s %s\n", t.Status, t.Agent, t.Jira, t.ID)
		}
		return 0

	case "pull":
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: fausto task pull [-root dir] <agent>")
			return 2
		}
		if *root == "" {
			fmt.Fprintln(stderr, "no "+spec.Dir+"/system.yaml found in this or any parent directory; run it in the project's clone, or pass -root")
			return 2
		}
		return pullTasks(ctx, db, *root, fs.Arg(0), stdout, stderr)

	case "start", "done", "drop":
		if fs.NArg() != 1 {
			fmt.Fprintf(stderr, "usage: fausto task %s <task>\n", sub)
			return 2
		}
		status := map[string]string{"start": "in-progress", "done": "done", "drop": "dropped"}[sub]
		id := fs.Arg(0)
		by := proposal.Who(*root, os.Getenv("FAUSTO_AUTHOR"))
		before, err := store.SetStatus(ctx, db, id, status, by)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", id, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %s → %s\n", id, before.Status, status)
		// Mirror it now if Jira is configured; otherwise the next sync does.
		jc, err := jiraClient()
		if err != nil || jc == nil {
			return 0
		}
		report, err := jira.SyncStore(ctx, jc, db, []string{id}, false)
		if err == nil && report.Failed() > 0 {
			err = errors.New(report.Results[0].Error)
		}
		if err != nil {
			fmt.Fprintf(stderr, "warning: Jira not updated (the next sync will): %v\n", err)
		} else if r := report.Results[0]; r.Key != "" {
			fmt.Fprintf(stdout, "Jira %s: %s\n", r.Key, strings.Join(r.Actions, ", "))
		}
		return 0
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// pullTasks writes agent's open and in-progress tasks to .fausto/tasks/
// under root, and removes the files of its tasks that are no longer open.
func pullTasks(ctx context.Context, db store.Store, root, agent string, stdout, stderr io.Writer) int {
	tasks, err := db.Tasks(ctx, store.TaskFilter{Agent: agent, Statuses: []string{"open", "in-progress"}})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dir := filepath.Join(root, filepath.FromSlash(pulledDir))
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
