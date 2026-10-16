package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/mongostore"
)

// specOptions returns the options to read the project at root with: the
// charters fausto keeps for it, when FAUSTO_MONGO_URI is set and fausto
// keeps them. Otherwise, and when the database can't be reached (a warning
// goes to stderr), the repository's own.
func specOptions(ctx context.Context, root, project string, stderr io.Writer) spec.Options {
	db, err := mongostore.FromEnv(ctx)
	if db == nil {
		if err != nil {
			fmt.Fprintln(stderr, "warning: using the repository's charters:", err)
		}
		return spec.Options{}
	}
	defer db.Close(context.Background())
	id, err := resolveProject(ctx, db, project, root)
	if err == nil {
		var list []store.Charter
		if list, err = db.Store(id).Charters(ctx); err == nil {
			if len(list) == 0 {
				return spec.Options{}
			}
			ch := make(map[string][]byte, len(list))
			for _, c := range list {
				ch[c.ID] = []byte(c.YAML)
			}
			return spec.Options{Charters: ch}
		}
	}
	fmt.Fprintln(stderr, "warning: using the repository's charters:", err)
	return spec.Options{}
}

// runCharters moves a project's agent charters between its repository and
// fausto's database.
func runCharters(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("charters "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	project := projectFlag(fs)
	force := fs.Bool("force", false, "import: replace the charters fausto already keeps")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	reg, err := openDB(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer reg.Close(context.Background())
	_ = resolveRoot(root)
	id, err := resolveProject(ctx, reg, *project, *root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	db := reg.Store(id)
	kept, err := db.Charters(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	switch sub {
	case "list":
		for _, c := range kept {
			fmt.Fprintf(stdout, "%-20s updated %s\n", c.ID, c.Updated.Format("2006-01-02 15:04"))
		}
		if len(kept) == 0 {
			fmt.Fprintf(stdout, "Fausto keeps no charters for %s; they are in its repository.\n", id)
		}
		return 0

	case "import":
		if *root == "" {
			fmt.Fprintln(stderr, "no "+spec.Dir+"/system.yaml found in this or any parent directory; pass -root")
			return 2
		}
		if len(kept) > 0 && !*force {
			fmt.Fprintf(stderr, "fausto already keeps %d charters for %s; pass -force to replace them with the repository's\n", len(kept), id)
			return 1
		}
		paths, _ := filepath.Glob(filepath.Join(*root, spec.Dir, "agents", "*.yaml"))
		if len(paths) == 0 {
			fmt.Fprintf(stderr, "no charters in %s\n", filepath.Join(*root, spec.Dir, "agents"))
			return 1
		}
		var put []store.Charter
		names := map[string]bool{}
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			cid := strings.TrimSuffix(filepath.Base(p), ".yaml")
			put = append(put, store.Charter{ID: cid, YAML: string(data)})
			names[cid] = true
		}
		// Check they load as a spec before keeping them.
		ch := map[string][]byte{}
		for _, c := range put {
			ch[c.ID] = []byte(c.YAML)
		}
		if _, err := spec.LoadWith(*root, spec.Options{Charters: ch}); err != nil {
			fmt.Fprintln(stderr, "the charters don't load:", err)
			return 1
		}
		var del []string
		for _, c := range kept {
			if !names[c.ID] {
				del = append(del, c.ID)
			}
		}
		if err := db.PutCharters(ctx, put, del); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Fausto now keeps %d charters for %s. Edit them through proposals; the files in %s/agents/ are no longer read, and can be removed from the repository.\n",
			len(put), id, spec.Dir)
		return 0

	case "export":
		if *root == "" {
			fmt.Fprintln(stderr, "no "+spec.Dir+"/system.yaml found in this or any parent directory; pass -root")
			return 2
		}
		if len(kept) == 0 {
			fmt.Fprintf(stderr, "fausto keeps no charters for %s\n", id)
			return 1
		}
		dir := filepath.Join(*root, spec.Dir, "agents")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, c := range kept {
			p := filepath.Join(dir, c.ID+".yaml")
			if err := os.WriteFile(p, []byte(c.YAML), 0o644); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintln(stdout, "wrote", filepath.ToSlash(filepath.Join(spec.Dir, "agents", c.ID+".yaml")))
		}
		fmt.Fprintln(stdout, "Commit them to keep the charters in the repository; fausto still reads its own until you remove them there.")
		return 0
	}
	fmt.Fprintln(stderr, usage)
	return 2
}
