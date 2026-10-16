package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/mongostore"
)

// pulledDir is where `fausto task pull` writes tasks, relative to the
// repository root; it is git-ignored.
const pulledDir = ".fausto/tasks"

// openDB connects to the database in FAUSTO_MONGO_URI.
func openDB(ctx context.Context) (store.Store, error) {
	db, err := mongostore.FromEnv(ctx)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("fausto has no database for proposals and tasks: FAUSTO_MONGO_URI is not set")
	}
	return db, nil
}

// checkedOutBranch returns the branch checked out at root.
func checkedOutBranch(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "-c", "safe.directory=*", "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	branch := strings.TrimSpace(string(out))
	if err != nil || branch == "" {
		return "", errors.New("no checked-out branch: pass -base (env FAUSTO_BASE)")
	}
	return branch, nil
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
	agent := fs.String("agent", "", "only this agent's tasks")
	statuses := fs.String("status", "", "only tasks with these statuses, comma-separated")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	db, err := openDB(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer db.Close(context.Background())

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
		if err := resolveRoot(root); err != nil {
			fmt.Fprintln(stderr, err)
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
		by := "unknown"
		if err := resolveRoot(root); err == nil {
			by = (&proposal.Store{Root: *root, Author: os.Getenv("FAUSTO_AUTHOR")}).Who()
		}
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

// runMigrate moves tasks/ and system/proposals/ into the database, then
// attaches each task's .md to its Jira issue if Jira is configured.
func runMigrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	base := fs.String("base", os.Getenv("FAUSTO_BASE"), "branch to migrate (env FAUSTO_BASE; default: the checked-out branch)")
	author := fs.String("author", os.Getenv("FAUSTO_AUTHOR"), `identity for the commit, "Name <email>" (env FAUSTO_AUTHOR; default: git's configured identity)`)
	dryRun := fs.Bool("dry-run", false, "only print what would be imported and removed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *base == "" {
		var err error
		if *base, err = checkedOutBranch(*root); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := openDB(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer db.Close(context.Background())
	ps := &proposal.Store{Root: *root, Base: *base, Author: *author, DB: db}
	m, err := ps.Migrate(ctx, *dryRun)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	verb := "imported"
	if *dryRun {
		verb = "would import"
	}
	for _, id := range m.Proposals {
		fmt.Fprintf(stdout, "%s proposal %s\n", verb, id)
	}
	for _, id := range m.Tasks {
		fmt.Fprintf(stdout, "%s task %s\n", verb, id)
	}
	for _, id := range m.Skipped {
		fmt.Fprintf(stdout, "already in the database: %s\n", id)
	}
	if *dryRun {
		fmt.Fprintf(stdout, "would remove %d files from %s\n", len(m.Removed), *base)
		return 0
	}
	if m.Commit != "" {
		fmt.Fprintf(stdout, "Removed %d files from %s in %.12s.\n", len(m.Removed), *base, m.Commit)
	} else {
		fmt.Fprintln(stdout, "Nothing left to remove from git.")
	}
	jc, err := jiraClient()
	if err != nil || jc == nil {
		return 0
	}
	fmt.Fprintln(stdout, "Syncing with Jira (attaches each task's .md):")
	report, err := jira.SyncStore(ctx, jc, db, nil, false)
	if report != nil {
		printJiraReport(stdout, report)
	}
	if err != nil || report.Failed() > 0 {
		fmt.Fprintln(stderr, "Some tasks didn't sync; run `fausto jira sync` to retry.", err)
		return 1
	}
	return 0
}
