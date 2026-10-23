package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/mongostore"
)

// specOptions returns the options to read the project at root with: the
// spec fausto keeps for it, when it keeps one and this clone is connected
// to fausto (or FAUSTO_MONGO_URI is set). Otherwise, and when fausto can't
// be reached (a warning goes to stderr), the repository's own.
func specOptions(ctx context.Context, root, project string, stderr io.Writer) spec.Options {
	if os.Getenv("FAUSTO_MONGO_URI") == "" {
		c, err := client.Resolve(root)
		if c == nil {
			if err != nil {
				fmt.Fprintln(stderr, "warning: using the repository's spec:", err)
			}
			return spec.Options{}
		}
		files, _, err := c.Spec(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "warning: using the repository's spec:", err)
			return spec.Options{}
		}
		return spec.Options{Files: files}
	}
	db, err := mongostore.FromEnv(ctx)
	if db == nil {
		if err != nil {
			fmt.Fprintln(stderr, "warning: using the repository's spec:", err)
		}
		return spec.Options{}
	}
	defer db.Close(context.Background())
	id, err := resolveProject(ctx, db, project, root)
	if err == nil {
		var rev *store.SpecRevision
		rev, err = db.Store(id).SpecRevision(ctx, 0)
		if errors.Is(err, store.ErrNotFound) {
			return spec.Options{}
		}
		if err == nil {
			return spec.Options{Files: rev.FileMap()}
		}
	}
	fmt.Fprintln(stderr, "warning: using the repository's spec:", err)
	return spec.Options{}
}

// runSpec moves a project's spec between its repository and fausto's
// database, shows its history, and writes it where an agent works.
func runSpec(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("spec "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	project := projectFlag(fs)
	force := fs.Bool("force", false, "import: replace the spec fausto keeps with the repository's, as a new revision")
	revision := fs.Int("revision", 0, "export: the revision to write (default: the latest)")
	message := fs.String("m", "", "import: what the new revision changes (default: where it came from)")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	_ = resolveRoot(root)
	if sub == "pull" {
		return pullSpec(ctx, *root, stdout, stderr)
	}
	reg, err := openDB(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer reg.Close(context.Background())
	id, err := resolveProject(ctx, reg, *project, *root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	db := reg.Store(id)
	latest, err := db.SpecRevision(ctx, 0)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		fmt.Fprintln(stderr, err)
		return 1
	}

	switch sub {
	case "log":
		list, err := db.SpecRevisions(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, r := range list {
			from := ""
			if r.Proposal != "" {
				from = " (proposal " + r.Proposal + ")"
			}
			fmt.Fprintf(stdout, "%4d  %s  %-28s %s%s\n", r.N, r.At.Format("2006-01-02 15:04"), authorShort(r.By), r.Message, from)
		}
		if len(list) == 0 {
			fmt.Fprintf(stdout, "Fausto keeps no spec for %s; it is in its repository.\n", id)
		}
		return 0

	case "import":
		if *root == "" {
			fmt.Fprintln(stderr, "no "+spec.Dir+"/system.yaml found in this or any parent directory; pass -root")
			return 2
		}
		if latest != nil && !*force {
			fmt.Fprintf(stderr, "fausto already keeps %s's spec (revision %d); pass -force to replace it with the repository's\n", id, latest.N)
			return 1
		}
		files, err := spec.ReadDir(*root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		// Charters fausto kept before it kept whole specs join them.
		legacy, err := reg.LegacyCharters(ctx, id)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for p, data := range legacy {
			if _, ok := files[p]; !ok {
				files[p] = data
			}
		}
		if files[spec.Dir+"/system.yaml"] == nil {
			fmt.Fprintf(stderr, "no %s/system.yaml in %s\n", spec.Dir, *root)
			return 1
		}
		opts := spec.Options{Files: files}
		report, err := check.RunWith(*root, []string{"schema"}, opts)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !report.OK {
			report.WriteText(stderr)
			fmt.Fprintln(stderr, "the spec doesn't pass the schema check; fix it, then import")
			return 1
		}
		n := 1
		msg := "Import from " + *root
		if latest != nil {
			n, msg = latest.N+1, "Replace with "+*root+"'s spec"
		}
		if *message != "" {
			msg = *message
		}
		r := &store.SpecRevision{N: n, Files: store.SpecFiles(files), By: proposal.Who(*root, os.Getenv("FAUSTO_AUTHOR")), Message: msg}
		if err := db.AddSpecRevision(ctx, r); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(legacy) > 0 {
			if err := reg.DropLegacyCharters(ctx, id); err != nil {
				fmt.Fprintln(stderr, "warning: the old charters collection is still there:", err)
			}
		}
		fmt.Fprintf(stdout, "Fausto now keeps %s's spec: revision %d, %d files. Change it through proposals; %s/ in the repository is no longer read and can be removed.\n",
			id, r.N, len(files), spec.Dir)
		return 0

	case "export":
		if *root == "" {
			fmt.Fprintln(stderr, "pass -root: the clone to write "+spec.Dir+"/ into")
			return 2
		}
		if latest == nil {
			fmt.Fprintf(stderr, "fausto keeps no spec for %s\n", id)
			return 1
		}
		r := latest
		if *revision != 0 {
			if r, err = db.SpecRevision(ctx, *revision); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		if err := writeSpec(*root, r.FileMap()); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote revision %d (%d files) to %s/. Commit them to keep the spec in the repository.\n", r.N, len(r.Files), filepath.Join(*root, spec.Dir))
		return 0
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// pullSpec writes the spec fausto keeps into root's system/, where an
// agent reads it, and makes git ignore it there. A project that keeps its
// spec in its repository is left alone.
func pullSpec(ctx context.Context, root string, stdout, stderr io.Writer) int {
	if root == "" {
		fmt.Fprintln(stderr, "not in a clone; pass -root")
		return 2
	}
	c, err := client.Resolve(root)
	if err != nil || c == nil {
		fmt.Fprintln(stderr, "not connected to fausto: run `fausto connect -token <token>`")
		return 2
	}
	code, err := pullSpecWith(ctx, c, root, stdout)
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	return code
}

func pullSpecWith(ctx context.Context, c *client.Client, root string, stdout io.Writer) (int, error) {
	files, rev, err := c.Spec(ctx)
	if err != nil {
		return 1, err
	}
	if files == nil {
		return 0, nil // the repository keeps its spec
	}
	if tracked, _ := gitOutput(root, "ls-files", spec.Dir+"/"); tracked != "" {
		return 1, fmt.Errorf("the repository has its own %s/; fausto's spec is not written over it", spec.Dir)
	}
	if err := client.ExcludePath(root, "/"+spec.Dir+"/", "the spec fausto keeps, written by `fausto spec pull`"); err != nil {
		return 1, err
	}
	if err := writeSpec(root, files); err != nil {
		return 1, err
	}
	fmt.Fprintf(stdout, "%-11s %s/ (revision %d, %d files)\n", "spec", spec.Dir, rev, len(files))
	return 0, nil
}

// writeSpec replaces root's system/ with files.
func writeSpec(root string, files map[string][]byte) error {
	dir := filepath.Join(root, spec.Dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasPrefix(p, spec.Dir+"/") || strings.Contains(p, "..") {
			continue // never write outside system/
		}
		dst := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, files[p], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root, "-c", "safe.directory=*"}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// authorShort is "Name" of "Name <email>".
func authorShort(ident string) string {
	if i := strings.Index(ident, " <"); i > 0 {
		return ident[:i]
	}
	return ident
}
