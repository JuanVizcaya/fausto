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
	"text/tabwriter"

	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/mongostore"
)

func projectFlag(fs *flag.FlagSet) *string {
	return fs.String("project", os.Getenv("FAUSTO_PROJECT"), "project id (env FAUSTO_PROJECT; default: the project of the current directory, or the only one)")
}

// slugOf turns a directory name into a project id.
func slugOf(name string) string {
	if s := edit.Slug(name, 40); s != "" {
		return s
	}
	return "project"
}

// projectAt returns the registered project whose local clone, or whose
// GitHub repository (by the origin remote), is the clone at root.
func projectAt(ctx context.Context, db store.Projects, root string) *store.Project {
	projects, err := db.Projects(ctx)
	if err != nil {
		return nil
	}
	abs, _ := filepath.Abs(root)
	gh := strings.TrimPrefix(detectGithubRepo(root), "https://github.com/")
	for i, p := range projects {
		if (p.Path != "" && filepath.Clean(p.Path) == abs) || (p.GitHub != "" && strings.EqualFold(p.GitHub, gh)) {
			return &projects[i]
		}
	}
	return nil
}

// resolveProject picks the project a command works on: the named one, else
// the one root is a clone of, else the only registered one, else one named
// after root's directory (the id `fausto serve` gives an unregistered
// clone).
func resolveProject(ctx context.Context, db store.Projects, id, root string) (string, error) {
	if id != "" {
		return id, store.ValidProjectID(id)
	}
	if root != "" {
		if p := projectAt(ctx, db, root); p != nil {
			return p.ID, nil
		}
	}
	projects, err := db.Projects(ctx)
	if err != nil {
		return "", err
	}
	if len(projects) == 1 {
		return projects[0].ID, nil
	}
	if root != "" {
		abs, _ := filepath.Abs(root)
		return slugOf(filepath.Base(abs)), nil
	}
	return "", errors.New("several projects: name one with -project (env FAUSTO_PROJECT)")
}

// noBaseError means a local clone has no branch to propose against.
type noBaseError struct{ err error }

func (e *noBaseError) Error() string { return e.err.Error() }

// dataDir is where fausto keeps its clones of GitHub repositories.
func dataDir() string {
	if d := os.Getenv("FAUSTO_DATA"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "fausto")
	}
	return filepath.Join(os.TempDir(), "fausto")
}

// openSource opens a project's repository: its local clone if it has one,
// else its GitHub repository.
func openSource(ctx context.Context, p store.Project) (source.Source, error) {
	if p.GitHub != "" && p.Path == "" {
		return source.OpenGitHub(ctx, &source.GitHub{
			Repo: p.GitHub, Branch: p.Base, Token: os.Getenv("FAUSTO_GITHUB_TOKEN"),
			Clone: filepath.Join(dataDir(), "repos", p.ID),
		})
	}
	l, err := source.NewLocal(ctx, p.Path, p.Base)
	if err != nil {
		return nil, &noBaseError{err}
	}
	l.URL = detectGithubRepo(p.Path)
	return l, nil
}

// openDB connects to the database in FAUSTO_MONGO_URI.
func openDB(ctx context.Context) (*mongostore.DB, error) {
	db, err := mongostore.FromEnv(ctx)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("fausto has no database: FAUSTO_MONGO_URI is not set")
	}
	return db, nil
}

func runProject(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("project "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var path, github, name, base *string
	var adopt *bool
	if sub == "add" {
		path = fs.String("path", "", "a local clone of the project")
		github = fs.String("github", "", `the project's GitHub repository, "owner/repo": without -path, fausto keeps its own clone (in FAUSTO_DATA) and opens pull requests; with -path, for checking pull requests (default: the clone's origin)`)
		name = fs.String("name", "", "display name (default: the id)")
		base = fs.String("base", "", "branch proposals target (default: the checked-out branch, or the repository's default branch)")
		adopt = fs.Bool("adopt", false, "give this project the proposals and tasks fausto kept before it had projects")
	}
	if err := parseInterspersed(fs, args); err != nil {
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
		projects, err := db.Projects(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		for _, p := range projects {
			src := p.Path
			switch {
			case src == "":
				src = "github.com/" + p.GitHub
			case p.GitHub != "":
				src += " (github.com/" + p.GitHub + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Name, src, p.Base)
		}
		tw.Flush()
		if len(projects) == 0 {
			fmt.Fprintln(stdout, "No projects. Add one with `fausto project add <id> -path <clone>` or `-github owner/repo`.")
		}
		return 0

	case "add":
		if fs.NArg() != 1 || (*path == "" && *github == "") {
			fmt.Fprintln(stderr, "usage: fausto project add <id> -path <clone> | -github owner/repo [-name name] [-base branch] [-adopt]")
			return 2
		}
		p := store.Project{ID: fs.Arg(0), Name: *name, GitHub: *github, Base: *base}
		if err := store.ValidProjectID(p.ID); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		if *path != "" {
			if p.Path, err = filepath.Abs(*path); err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
		}
		if p.GitHub != "" && strings.Count(p.GitHub, "/") != 1 {
			fmt.Fprintln(stderr, `-github wants "owner/repo"`)
			return 2
		}
		// Open it once, so a wrong path, repository or token shows now.
		src, err := openSource(ctx, p)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := db.PutProject(ctx, &p); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Added %s: %s, proposals target %s.\n", p.ID, src.Dir(), src.Base())
		if *adopt {
			renamed, err := db.AdoptLegacy(ctx, p.ID)
			for _, r := range renamed {
				fmt.Fprintln(stdout, "moved", r)
			}
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		return 0

	case "rename":
		if fs.NArg() != 2 {
			fmt.Fprintln(stderr, "usage: fausto project rename <id> <new-id>")
			return 2
		}
		from, to := fs.Arg(0), fs.Arg(1)
		renamed, err := db.RenameProject(ctx, from, to)
		for _, r := range renamed {
			fmt.Fprintln(stdout, "moved", r)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		// fausto's own clones, when it keeps any for the project.
		for _, suffix := range []string{"", "-prs.git"} {
			old := filepath.Join(dataDir(), "repos", from+suffix)
			if _, err := os.Stat(old); err == nil {
				if err := os.Rename(old, filepath.Join(dataDir(), "repos", to+suffix)); err != nil {
					fmt.Fprintln(stderr, err)
				}
			}
		}
		fmt.Fprintf(stdout, "Renamed %s to %s; restart fausto serve to pick it up.\n", from, to)
		return 0

	case "rm":
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: fausto project rm <id>")
			return 2
		}
		if err := db.DeleteProject(ctx, fs.Arg(0)); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Removed %s; its proposals and tasks stay in the database.\n", fs.Arg(0))
		return 0
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// parseInterspersed parses flags wherever they are among the arguments, so
// `fausto project add habbyte -path ../habbyte` works as well as with the
// flags first.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	return fs.Parse(append([]string{"--"}, positional...))
}
