// Command fausto supervises the Habyta system: it checks that the code
// matches the spec under system/ and serves a web UI for reviewing it.
//
// Usage:
//
//	fausto check [-root dir] [-json] [check ...]
//	fausto agents [-root dir]
//	fausto serve [-root dir] [-addr host:port] [-base branch] [-author "Name <email>"] [-repo url]
//	fausto jira sync [-dry-run] [task ...]
//	fausto task list|pull|start|done|drop ...
//	fausto migrate [-root dir] [-base branch] [-author "Name <email>"] [-dry-run]
//
// Proposals and tasks live in a MongoDB database (FAUSTO_MONGO_URI); git
// holds only the spec and the code.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/JuanVizcaya/fausto/internal/agentdef"
	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/server"
	"github.com/JuanVizcaya/fausto/internal/spec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  fausto check [-root dir] [-json] [check ...]
  fausto agents [-root dir]    regenerate .claude/agents and .github/CODEOWNERS
                               from system/agents
  fausto serve [-root dir] [-addr host:port] [-base branch] [-author "Name <email>"] [-repo url]
  fausto jira sync [-dry-run] [task ...]
                               mirror tasks as Jira issues (needs FAUSTO_JIRA_*)
  fausto task list [-agent id] [-status s,...]
  fausto task pull [-root dir] <agent>
                               write the agent's open tasks to .fausto/tasks/
  fausto task start|done|drop <task>
  fausto migrate [-root dir] [-base branch] [-author "Name <email>"] [-dry-run]
                               move tasks/ and system/proposals/ into the database
Proposals and tasks live in MongoDB: set FAUSTO_MONGO_URI (and FAUSTO_MONGO_DB).`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "agents":
		return runAgents(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stderr)
	case "jira":
		if len(args) < 2 || args[1] != "sync" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		return runJiraSync(args[2:], stdout, stderr)
	case "task":
		return runTask(args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		fmt.Fprintln(stderr, "checks:", strings.Join(check.Names(), ", "))
		return 2
	}
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	report, err := check.Run(*root, fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	} else {
		printText(stdout, report)
	}
	if !report.OK {
		return 1
	}
	return 0
}

// runAgents regenerates the files generated from the charters: the Claude
// Code agent definitions and CODEOWNERS.
func runAgents(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	s, err := spec.Load(*root)
	if err != nil {
		fmt.Fprintln(stderr, "load the spec:", err)
		return 1
	}
	files, err := agentdef.Sync(*root, s)
	if err == nil {
		err = agentdef.Write(*root, files)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		verb := "wrote"
		if files[p] == nil {
			verb = "removed"
		}
		fmt.Fprintf(stdout, "%s %s\n", verb, p)
	}
	if len(paths) == 0 {
		fmt.Fprintln(stdout, "Agent definitions and CODEOWNERS are up to date.")
	}
	return 0
}

func runServe(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	addr := fs.String("addr", envOr("FAUSTO_ADDR", "127.0.0.1:7070"), "listen address (env FAUSTO_ADDR)")
	base := fs.String("base", os.Getenv("FAUSTO_BASE"), "branch that proposals target and are merged into (env FAUSTO_BASE; default: the checked-out branch)")
	author := fs.String("author", os.Getenv("FAUSTO_AUTHOR"), `identity for proposal commits, "Name <email>" (env FAUSTO_AUTHOR; default: git's configured identity)`)
	repo := fs.String("repo", os.Getenv("FAUSTO_REPO"), "GitHub URL of the repository, for the Architecture tab to link into (env FAUSTO_REPO; default: detected from the origin remote)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	// readonly says why editing is off; the UI shows it.
	readonly := ""
	if *base == "" {
		out, err := exec.Command("git", "-C", *root, "-c", "safe.directory=*", "symbolic-ref", "--quiet", "--short", "HEAD").Output()
		*base = strings.TrimSpace(string(out))
		if err != nil || *base == "" {
			readonly = "no checked-out branch to propose against. Check out a branch, or pass -base (env FAUSTO_BASE)."
		}
	}
	if *base != "" && *author == "" {
		// Proposal commits need an identity; without one, fail now rather
		// than on the first proposal.
		if err := exec.Command("git", "-C", *root, "-c", "safe.directory=*", "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
			readonly = `git has no identity for proposal commits. Set FAUSTO_AUTHOR="Name <email>" (in .env when using docker compose) or pass -author, then restart fausto.`
			*base = ""
		}
	}
	if readonly != "" {
		logger.Warn("editing disabled: " + readonly)
	}
	if *repo == "" {
		*repo = detectGithubRepo(*root)
	}
	db, err := openDB(context.Background())
	if err != nil && readonly == "" {
		fix := "Set FAUSTO_MONGO_URI (see https://github.com/JuanVizcaya/fausto#storage)"
		if os.Getenv("FAUSTO_MONGO_URI") != "" {
			fix = "Check that the database in FAUSTO_MONGO_URI is running and reachable"
		}
		readonly = err.Error() + ". " + fix + ", then restart fausto."
	}
	if db != nil {
		defer db.Close(context.Background())
	}
	var store *proposal.Store
	if *base != "" && db != nil && readonly == "" {
		store = &proposal.Store{Root: *root, Base: *base, Author: *author, DB: db}
	}
	jc, err := jiraClient()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if jc != nil {
		logger.Info("mirroring tasks in Jira", "url", jc.URL, "project", jc.Project)
		if store == nil {
			logger.Warn("Jira sync is off while fausto is read-only")
		}
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(*root, store, readonly, *repo, jc, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	logger.Info("fausto serving", "url", "http://"+*addr, "root", *root, "base", *base)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "error", err)
		return 1
	}
	return 0
}

// runJiraSync mirrors the tasks (or the named ones) as Jira issues and
// records the keys of newly linked issues.
func runJiraSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jira sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "only read from Jira and print what a sync would do")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jc, err := jiraClient()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if jc == nil {
		fmt.Fprintln(stderr, "Jira is not configured: set FAUSTO_JIRA_URL, FAUSTO_JIRA_EMAIL, FAUSTO_JIRA_TOKEN and FAUSTO_JIRA_PROJECT.")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := openDB(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer db.Close(context.Background())
	report, err := jira.SyncStore(ctx, jc, db, fs.Args(), *dryRun)
	if report != nil {
		printJiraReport(stdout, report)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if n := report.Failed(); n > 0 {
		fmt.Fprintf(stderr, "%d of %d tasks failed to sync.\n", n, len(report.Results))
		return 1
	}
	return 0
}

func printJiraReport(w io.Writer, report *jira.Report) {
	for _, r := range report.Results {
		line := fmt.Sprintf("%-30s %-10s %s", strings.Join(r.Actions, ", "), r.Key, r.Task)
		if r.Error != "" {
			line += ": " + r.Error
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}

// jiraClient returns a Jira client from the FAUSTO_JIRA_* variables, or nil
// when they are unset. Credentials only come from the environment.
func jiraClient() (*jira.Client, error) {
	cfg, err := jira.ConfigFromEnv()
	if err != nil || cfg == nil {
		return nil, err
	}
	return jira.NewClient(*cfg), nil
}

// detectGithubRepo guesses the GitHub URL of root's origin remote, for the
// web UI to link into. It returns "" if there's no remote, or it isn't on
// github.com.
func detectGithubRepo(root string) string {
	out, err := exec.Command("git", "-C", root, "-c", "safe.directory=*", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return githubURL(strings.TrimSpace(string(out)))
}

// githubURL normalizes a git remote URL to its GitHub web URL, e.g.
// "git@github.com:CMOT/habyta.git" or "https://github.com/CMOT/habyta.git"
// to "https://github.com/CMOT/habyta". Returns "" for anything else.
func githubURL(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		return "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "https://github.com/"), strings.HasPrefix(remote, "http://github.com/"):
		return remote
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		return "https://github.com/" + strings.TrimPrefix(remote, "ssh://git@github.com/")
	default:
		return ""
	}
}

func rootFlag(fs *flag.FlagSet) *string {
	return fs.String("root", os.Getenv("FAUSTO_ROOT"), "repository root (env FAUSTO_ROOT; default: nearest parent directory containing system/system.yaml)")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func resolveRoot(root *string) error {
	if *root != "" {
		return nil
	}
	r, err := findRoot()
	*root = r
	return err
}

func printText(w io.Writer, report *check.Report) {
	failed := 0
	for _, r := range report.Results {
		fmt.Fprintf(w, "%-7s  %-12s  %s\n", strings.ToUpper(string(r.Status)), r.Check, r.Description)
		for _, f := range r.Findings {
			var where []string
			if f.Component != "" {
				where = append(where, f.Component)
			}
			if f.Path != "" {
				where = append(where, f.Path)
			}
			loc := ""
			if len(where) > 0 {
				loc = "[" + strings.Join(where, " ") + "] "
			}
			fmt.Fprintf(w, "         - %s%s\n", loc, f.Message)
		}
		if r.Status == check.Fail {
			failed++
		}
	}
	if failed == 0 {
		fmt.Fprintln(w, "\nThe code matches the system spec.")
	} else {
		fmt.Fprintf(w, "\n%d of %d checks failed.\n", failed, len(report.Results))
	}
}

func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, spec.Dir, "system.yaml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no " + spec.Dir + "/system.yaml found in this or any parent directory; pass -root")
		}
		dir = parent
	}
}
