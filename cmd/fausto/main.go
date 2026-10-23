// Command fausto keeps a project's code in line with its architecture spec
// under system/: it checks that the code matches the spec, and serves a web
// UI for reviewing it and proposing changes to it.
//
// Usage:
//
//	fausto check [-root dir] [-json] [check ...]
//	fausto agents [-root dir]
//	fausto serve [-root dir] [-project id] [-addr host:port] [-base branch] [-author "Name <email>"] [-repo url] [-poll 1m]
//	fausto project list|add|rename|rm ...
//	fausto spec import|export|log|pull [-root dir] [-project id]
//	fausto jira sync [-project id] [-dry-run] [task ...]
//	fausto task list|pull|start|done|drop [-project id] ...
//
// Projects, and their proposals and tasks, live in fausto's MongoDB
// database (FAUSTO_MONGO_URI); a project's git holds only its spec and code.
package main

import (
	"cmp"
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
	"github.com/JuanVizcaya/fausto/internal/github"
	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/prcheck"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/server"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/mongostore"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  fausto check [-root dir] [-json] [-github] [-commands-dir d] [-compose-file f] [-proto-root d] [check ...]
  fausto agents [-root dir] [-project id]
                               regenerate .claude/agents and .github/CODEOWNERS
                               from the charters (fausto's, or system/agents)
  fausto serve [-root dir] [-project id] [-addr host:port] [-base branch] [-author "Name <email>"] [-repo url] [-poll 1m]
                               the web UI, for every project (or only -root)
  fausto project list
  fausto project add <id> -path <clone> | -github owner/repo [-name name] [-base branch] [-adopt]
  fausto project rename <id> <new-id>
  fausto project rm <id>
  fausto spec import [-force] [-m message] | export [-revision n] | log | pull   [-root dir] [-project id]
                               keep a project's spec in fausto's database, write it
                               back, list its revisions, or write it where an agent works
  fausto token create [-project id] <agent> | list | rm <label>
                               agents' tokens for fausto's API (needs the database)
  fausto connect -token <token> [-url url]
                               connect this clone to fausto as that token's agent
  fausto mcp                   MCP server for Claude Code: tasks and checks (stdio)
  fausto claude [-base branch] <agent> [-- claude args...]
                               start the agent in its own work tree, on its tasks
  fausto jira sync [-project id] [-dry-run] [task ...]
                               mirror tasks as Jira issues (needs FAUSTO_JIRA_*)
  fausto task list [-project id] [-agent id] [-status s,...]
  fausto task pull [-root dir] [-project id] <agent>
                               write the agent's open tasks to .fausto/tasks/
  fausto task start|done|drop [-project id] <task>
Projects, proposals and tasks live in fausto's MongoDB (FAUSTO_MONGO_URI). Agents
reach them through fausto's API instead, with a token (fausto connect).`

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
	case "project":
		return runProject(args[1:], stdout, stderr)
	case "spec":
		return runSpec(args[1:], stdout, stderr)
	case "charters":
		fmt.Fprintln(stderr, "fausto now keeps whole specs, charters included: use `fausto spec import|export|log|pull`")
		return 2
	case "token":
		return runToken(args[1:], stdout, stderr)
	case "connect":
		return runConnect(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], os.Stdin, stdout, stderr)
	case "claude":
		return runClaude(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, "fausto", version)
		return 0
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
	project := projectFlag(fs)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	github := fs.Bool("github", os.Getenv("GITHUB_ACTIONS") == "true", "also annotate findings for GitHub Actions and write the job summary (default: on in GitHub Actions)")
	var layout spec.Layout
	fs.StringVar(&layout.CommandsDir, "commands-dir", "", "where Go commands live (default: .fausto.yaml's, or "+spec.DefaultLayout.CommandsDir+")")
	fs.StringVar(&layout.ComposeFile, "compose-file", "", "the Compose file (default: .fausto.yaml's, or "+spec.DefaultLayout.ComposeFile+")")
	fs.StringVar(&layout.ProtoRoot, "proto-root", "", "where protobuf packages live (default: .fausto.yaml's, or "+spec.DefaultLayout.ProtoRoot+")")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := specOptions(ctx, *root, *project, stderr)
	opts.Layout = &layout
	report, err := check.RunWith(*root, fs.Args(), opts)
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
	if *github {
		report.WriteGitHub(stdout)
		if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
			if f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
				report.WriteMarkdown(f)
				f.Close()
			}
		}
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
	project := projectFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := spec.LoadWith(*root, specOptions(ctx, *root, *project, stderr))
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
	root := fs.String("root", os.Getenv("FAUSTO_ROOT"), "serve only the local clone at this directory (env FAUSTO_ROOT; default: every project in the database, or the project around the current directory)")
	project := projectFlag(fs)
	addr := fs.String("addr", envOr("FAUSTO_ADDR", "127.0.0.1:7070"), "listen address (env FAUSTO_ADDR)")
	base := fs.String("base", os.Getenv("FAUSTO_BASE"), "with -root, the branch proposals target (env FAUSTO_BASE; default: the checked-out branch)")
	author := fs.String("author", os.Getenv("FAUSTO_AUTHOR"), `identity for proposal commits, "Name <email>" (env FAUSTO_AUTHOR; default: git's configured identity)`)
	repo := fs.String("repo", os.Getenv("FAUSTO_REPO"), "with -root, the GitHub URL of the repository, for the UI to link into (env FAUSTO_REPO; default: detected from the origin remote)")
	poll := fs.Duration("poll", envDuration("FAUSTO_POLL", time.Minute), "how often to check accepted proposals for a merge (env FAUSTO_POLL; 0 turns it off)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, dbErr := mongostore.FromEnv(ctx)
	if dbErr == nil && db == nil {
		dbErr = errors.New("FAUSTO_MONGO_URI is not set")
	}
	if db != nil {
		defer db.Close(context.Background())
	}
	// Which projects: the one named by -root, else the registered ones,
	// else the one around the current directory.
	var projects []store.Project
	if *root == "" && db != nil {
		var err error
		if projects, err = db.Projects(ctx); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if len(projects) == 0 && *root == "" {
		if r, err := findRoot(); err == nil {
			*root = r
		} else if db == nil {
			fmt.Fprintln(stderr, err)
			return 2
		} else {
			logger.Warn("no projects yet: add one with `fausto project add`, then restart fausto")
		}
	}
	if len(projects) == 0 && *root != "" {
		abs, _ := filepath.Abs(*root)
		p := store.Project{Path: abs, Base: *base}
		if db != nil {
			if reg := projectAt(ctx, db, abs); reg != nil {
				p = *reg
				if *base != "" {
					p.Base = *base
				}
			}
		}
		if p.ID == "" {
			p.ID = *project
		}
		if p.ID == "" {
			p.ID = slugOf(filepath.Base(abs))
		}
		p.Name = cmp.Or(p.Name, p.ID)
		projects = []store.Project{p}
	}

	jc, err := jiraClient()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if jc != nil {
		logger.Info("mirroring tasks in Jira", "url", jc.URL, "project", jc.Project)
	}
	var entries []server.Project
	for _, p := range projects {
		e := server.Project{ID: p.ID, Name: cmp.Or(p.Name, p.ID), Source: cmp.Or(p.Path, "github.com/"+p.GitHub)}
		h, err := openProject(ctx, p, db, dbErr, *author, *repo, jc, *poll, logger)
		if err != nil {
			logger.Error("open project", "project", p.ID, "error", err)
			e.Error = err.Error()
		} else {
			h.Start(ctx)
			e.Handler = h
		}
		entries = append(entries, e)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.Multi(entries, tokensOf(db)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	ids := make([]string, len(projects))
	for i, p := range projects {
		ids[i] = p.ID
	}
	logger.Info("fausto serving", "url", "http://"+*addr, "projects", strings.Join(ids, ", "))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "error", err)
		return 1
	}
	return 0
}

// tokensOf is db as the server's token lookup, or nil without a database.
func tokensOf(db *mongostore.DB) server.Tokens {
	if db == nil {
		return nil
	}
	return db
}

// openProject opens p's repository and returns its handler. Without a
// database (dbErr), or without an identity for commits, the project is
// served read-only, with the reason in the UI.
func openProject(ctx context.Context, p store.Project, db *mongostore.DB, dbErr error, author, repo string, jc *jira.Client, poll time.Duration, logger *slog.Logger) (*server.Handler, error) {
	src, err := openSource(ctx, p)
	if err != nil {
		var noBase *noBaseError
		if !errors.As(err, &noBase) {
			return nil, err
		}
		// A local clone without a checked-out branch is still readable.
		return server.New(server.Options{Root: p.Path, Readonly: err.Error(), Repo: cmp.Or(repo, detectGithubRepo(p.Path)), Logger: logger}), nil
	}
	if repo == "" {
		repo = cmp.Or(src.Web(), detectGithubRepo(src.Dir()))
	}
	readonly := ""
	if dbErr != nil {
		fix := "Set FAUSTO_MONGO_URI (see https://github.com/JuanVizcaya/fausto#storage)"
		if os.Getenv("FAUSTO_MONGO_URI") != "" {
			fix = "Check that the database in FAUSTO_MONGO_URI is running and reachable"
		}
		readonly = dbErr.Error() + ". " + fix + ", then restart fausto."
	} else if proposal.Who(src.Dir(), author) == "unknown" {
		// Proposal commits need an identity; without one, say so now
		// rather than on the first accept.
		readonly = `git has no identity for proposal commits. Set FAUSTO_AUTHOR="Name <email>" (in .env when using docker compose) or pass -author, then restart fausto.`
	}
	if readonly != "" {
		logger.Warn("editing disabled", "project", p.ID, "reason", readonly)
		return server.New(server.Options{Root: src.Dir(), Readonly: readonly, Repo: repo, Logger: logger}), nil
	}
	ps := &proposal.Store{Src: src, Author: author, DB: db.Store(p.ID)}
	o := server.Options{Project: p.ID, Tokens: db, Root: src.Dir(), Proposals: ps, Repo: repo, Jira: jc, Logger: logger, Poll: poll}
	// Pull requests are checked against the spec fausto keeps, on GitHub.
	gh := cmp.Or(p.GitHub, strings.TrimPrefix(repo, "https://github.com/"))
	switch token := os.Getenv("FAUSTO_GITHUB_TOKEN"); {
	case gh == "":
		o.PRCheckOff = "the project isn't on GitHub"
	case token == "":
		o.PRCheckOff = "set FAUSTO_GITHUB_TOKEN to check " + gh + "'s pull requests"
	case poll <= 0:
		o.PRCheckOff = "polling is off (-poll 0)"
	default:
		o.PRCheck = &prcheck.Checker{Repo: gh, GH: &github.Client{Token: token}, DB: ps.DB, Clone: filepath.Join(dataDir(), "repos", p.ID+"-prs.git")}
	}
	return server.New(o), nil
}

// runJiraSync mirrors the tasks (or the named ones) as Jira issues and
// records the keys of newly linked issues.
func runJiraSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jira sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := projectFlag(fs)
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
	reg, err := openDB(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer reg.Close(context.Background())
	root, _ := findRoot()
	id, err := resolveProject(ctx, reg, *project, root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	report, err := jira.SyncStore(ctx, jc, reg.Store(id), fs.Args(), *dryRun)
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

func envDuration(key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return d
	}
	return fallback
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

func printText(w io.Writer, report *check.Report) { report.WriteText(w) }

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
			break
		}
		dir = parent
	}
	// A clone whose spec fausto keeps has no system/: the git work tree.
	if out, err := exec.Command("git", "-c", "safe.directory=*", "rev-parse", "--show-toplevel").Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return "", errors.New("not in a project clone (no " + spec.Dir + "/system.yaml here or above, and not in a git work tree); pass -root")
}
