package main

import (
	"context"
	"encoding/json"
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

	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/spec"
)

// execClaude replaces fausto with Claude Code in dir; tests replace it.
var execClaude = func(dir, path string, args []string) error {
	if err := os.Chdir(dir); err != nil {
		return err
	}
	return syscall.Exec(path, append([]string{path}, args...), os.Environ())
}

// runClaude starts an agent on its tasks, from the orchestrator's clone:
// in the agent's own work tree and branch (created the first time), with
// the agent's token there, its tasks pulled, and Claude Code given the
// agent's definition and fausto's MCP tools, both from fausto. Nothing is
// written to the project's files.
func runClaude(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claude", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	dir := fs.String("dir", "", "the agent's work tree (default: ../<project>-<agent>, beside this clone)")
	base := fs.String("base", "", "the branch a new work tree's agent/<agent> branch starts from (default: the checked-out branch)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(stderr, "usage: fausto claude [-dir path] [-base branch] <agent> [-- claude args...]")
		return 2
	}
	agent, extra := fs.Arg(0), fs.Args()[1:]
	if len(extra) > 0 && extra[0] == "--" {
		extra = extra[1:]
	}
	if err := resolveRoot(root); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := startAgent(ctx, *root, agent, *dir, *base, extra, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func startAgent(ctx context.Context, root, agent, dir, base string, extra []string, stdout, stderr io.Writer) error {
	cfg, err := client.LoadConfig(root)
	if err != nil {
		return err
	}
	c := &client.Client{URL: envOr("FAUSTO_URL", cfg.URL), Token: cfg.Agents[agent]}
	if c.URL == "" {
		c.URL = client.DefaultURL
	}
	if c.Token == "" {
		return fmt.Errorf("no token for agent %s in %s: create one with `fausto token create %s` here", agent, client.ConfigFile, agent)
	}
	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	if me.Agent != agent {
		return fmt.Errorf("the token saved for %s is agent %s's", agent, me.Agent)
	}
	if me.Kind == "human" {
		return fmt.Errorf("%s is a person's charter, not an agent's", agent)
	}
	claude := os.Getenv("FAUSTO_CLAUDE")
	if claude == "" {
		if claude, err = exec.LookPath("claude"); err != nil {
			return errors.New("Claude Code (claude) is not on PATH; install it, or set FAUSTO_CLAUDE")
		}
	}

	if dir == "" {
		dir = filepath.Join(filepath.Dir(root), me.Project+"-"+agent)
	}
	if err := ensureWorktree(root, dir, "agent/"+agent, base, stdout); err != nil {
		return err
	}
	// The work tree's own connection: this agent's token only.
	if err := (&client.Config{URL: c.URL, Token: c.Token}).Save(dir); err != nil {
		return err
	}
	if code := pullTasks(ctx, apiTasks{c, me}, dir, agent, stdout, stderr); code != 0 {
		return errors.New("pulling the tasks failed")
	}
	// The spec fausto keeps, where the agent reads it.
	if _, err := pullSpecWith(ctx, c, dir, stdout); err != nil {
		return err
	}

	def, err := c.Definition(ctx)
	if err != nil {
		return err
	}
	agents, _ := json.Marshal(map[string]any{agent: map[string]string{"description": def.Description, "prompt": def.Prompt}})
	self, err := os.Executable()
	if err != nil {
		self = "fausto"
	}
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fausto": map[string]any{"command": self, "args": []string{"mcp"}}}})
	args := []string{"--agents", string(agents), "--agent", agent, "--mcp-config", string(mcpConfig)}
	if len(extra) > 0 {
		args = append(args, extra...)
	} else {
		args = append(args, "Work your open tasks.")
	}
	fmt.Fprintf(stdout, "Starting %s in %s.\n", agent, dir)
	return execClaude(dir, claude, args)
}

// ensureWorktree makes dir a work tree of the clone at root on branch,
// created from base (or the checked-out branch) the first time. An
// existing work tree is used as it is.
func ensureWorktree(root, dir, branch, base string, stdout io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, spec.Dir, "system.yaml")); err == nil {
		return nil
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "safe.directory=*"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		}
		return nil
	}
	if git("rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil {
		if err := git("worktree", "add", "--quiet", dir, branch); err != nil {
			return err
		}
	} else {
		args := []string{"worktree", "add", "--quiet", "-b", branch, dir}
		if base != "" {
			args = append(args, base)
		}
		if err := git(args...); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "Created work tree %s on branch %s.\n", dir, branch)
	return nil
}
