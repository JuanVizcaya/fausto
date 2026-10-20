package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// runConnect connects a clone to fausto with an agent's token: it saves the
// connection in .fausto/config.json, which git ignores.
func runConnect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	url := fs.String("url", envOr("FAUSTO_URL", client.DefaultURL), "fausto's address (env FAUSTO_URL)")
	token := fs.String("token", os.Getenv("FAUSTO_TOKEN"), "the agent's token, from `fausto token create <agent>` (env FAUSTO_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *token == "" {
		fmt.Fprintln(stderr, "usage: fausto connect -token <token> [-url "+client.DefaultURL+"]")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	me, err := (&client.Client{URL: *url, Token: *token}).Me(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cfg, err := client.LoadConfig(*root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cfg.URL, cfg.Token = *url, *token
	if err := cfg.Save(*root); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Connected as agent %s of %s, through %s. Saved in %s (git ignores it).\n", me.Agent, me.Project, *url, client.ConfigFile)
	return 0
}

// runToken manages agents' tokens. It needs the database: run it where
// fausto runs (with docker compose: docker compose exec fausto fausto token ...).
func runToken(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("token "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	project := projectFlag(fs)
	save := fs.Bool("save", true, "create: also keep the token in this clone's "+client.ConfigFile+", for `fausto claude <agent>`")
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

	switch sub {
	case "create":
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: fausto token create [-project id] <agent>")
			return 2
		}
		id, err := resolveProject(ctx, reg, *project, *root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		agent := fs.Arg(0)
		if !hasCharter(ctx, reg.Store(id), *root, agent) {
			fmt.Fprintf(stderr, "%s has no agent %q\n", id, agent)
			return 1
		}
		secret, t, err := store.NewToken(id, agent, proposal.Who(*root, os.Getenv("FAUSTO_AUTHOR")))
		if err == nil {
			err = reg.CreateToken(ctx, t)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Token for agent %s of %s (it is shown only now):\n\n  %s\n\n", agent, id, secret)
		if *save && *root != "" {
			cfg, err := client.LoadConfig(*root)
			if err == nil {
				if cfg.Agents == nil {
					cfg.Agents = map[string]string{}
				}
				cfg.Agents[agent] = secret
				if cfg.URL == "" {
					cfg.URL = envOr("FAUSTO_URL", client.DefaultURL)
				}
				err = cfg.Save(*root)
			}
			if err != nil {
				fmt.Fprintln(stderr, "warning: not saved:", err)
			} else {
				fmt.Fprintf(stdout, "Saved in %s; `fausto claude %s` hands it to the agent.\n", client.ConfigFile, agent)
			}
		}
		fmt.Fprintf(stdout, "Elsewhere: fausto connect -token %s\n", secret)
		return 0

	case "list":
		id := *project
		tokens, err := reg.Tokens(ctx, id)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		for _, t := range tokens {
			fmt.Fprintf(tw, "%s…\t%s\t%s\t%s\t%s\n", t.Label, t.Project, t.Agent, t.Created.Format("2006-01-02"), t.By)
		}
		tw.Flush()
		if len(tokens) == 0 {
			fmt.Fprintln(stdout, "No tokens.")
		}
		return 0

	case "rm":
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: fausto token rm <label>   (the first characters `fausto token list` shows)")
			return 2
		}
		if err := reg.DeleteToken(ctx, fs.Arg(0)); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Revoked %s.\n", fs.Arg(0))
		return 0
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// hasCharter reports whether the project has agent id: in the spec fausto
// keeps, else in the clone at root (unknown without one).
func hasCharter(ctx context.Context, db store.Store, root, id string) bool {
	var s *spec.Spec
	if rev, err := db.SpecRevision(ctx, 0); err == nil {
		s, _ = spec.LoadWith(root, spec.Options{Files: rev.FileMap()})
	} else if root != "" {
		s, _ = spec.Load(root)
	}
	if s == nil {
		return true
	}
	for _, a := range s.Agents {
		if a.ID == id {
			return true
		}
	}
	return false
}
