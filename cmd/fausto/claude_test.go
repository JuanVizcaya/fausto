package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/server"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// exampleRepo copies the example project into a new repository on main.
func exampleRepo(t *testing.T) string {
	t.Helper()
	src := "../../testdata/example"
	files, err := check.ListFiles(src)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, f)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "initial")
	return root
}

func TestClaude(t *testing.T) {
	ctx := context.Background()
	root := exampleRepo(t)
	reg := memstore.NewProjects()
	db := reg.Store("example")
	db.CreateTasks(ctx, []store.Task{{ID: "2026-10-06-x--platform", Agent: "platform", Status: "open", Created: "2026-10-06", Title: "X", Body: "Do X.\n"}})
	secret, tok, _ := store.NewToken("example", "platform", "")
	reg.CreateToken(ctx, tok)
	ps := &proposal.Store{Src: &source.Local{Path: root, Branch: "main"}, DB: db}
	api := httptest.NewServer(server.Multi([]server.Project{{ID: "example",
		Handler: server.New(server.Options{Project: "example", Tokens: reg, Root: root, Proposals: ps})}}, reg))
	defer api.Close()
	if err := (&client.Config{URL: api.URL, Agents: map[string]string{"platform": secret}}).Save(root); err != nil {
		t.Fatal(err)
	}

	var gotDir, gotPath string
	var gotArgs []string
	execClaude = func(dir, path string, args []string) error {
		gotDir, gotPath, gotArgs = dir, path, args
		return nil
	}
	t.Setenv("FAUSTO_CLAUDE", "/usr/bin/true")
	wt := filepath.Join(t.TempDir(), "example-platform")
	if code := runClaude([]string{"-root", root, "-dir", wt, "platform"}, io.Discard, os.Stderr); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if gotDir != wt || gotPath != "/usr/bin/true" {
		t.Errorf("ran %s in %s", gotPath, gotDir)
	}
	if b := gitIn(t, wt, "branch", "--show-current"); b != "agent/platform" {
		t.Errorf("work tree branch %q", b)
	}
	if _, err := os.Stat(filepath.Join(wt, ".fausto", "tasks", "2026-10-06-x--platform.md")); err != nil {
		t.Errorf("tasks not pulled: %v", err)
	}
	cfg, _ := client.LoadConfig(wt)
	if cfg.Token != secret || len(cfg.Agents) != 0 {
		t.Errorf("work tree config = %+v (want only the agent's token)", cfg)
	}
	for _, dir := range []string{root, wt} {
		if st := gitIn(t, dir, "status", "--porcelain"); st != "" {
			t.Errorf("%s not clean:\n%s", dir, st)
		}
	}
	if len(gotArgs) < 7 || gotArgs[0] != "--agents" || gotArgs[2] != "--agent" || gotArgs[3] != "platform" || gotArgs[4] != "--mcp-config" ||
		gotArgs[len(gotArgs)-1] != "Work your open tasks." {
		t.Fatalf("args = %q", gotArgs)
	}
	var agents map[string]struct{ Description, Prompt string }
	if err := json.Unmarshal([]byte(gotArgs[1]), &agents); err != nil || !strings.Contains(agents["platform"].Prompt, "You are the **Platform agent**") {
		t.Errorf("--agents = %s, %v", gotArgs[1], err)
	}
	if !strings.Contains(gotArgs[5], `"args":["mcp"]`) {
		t.Errorf("--mcp-config = %s", gotArgs[5])
	}

	// Again: the work tree is reused, and extra arguments replace the prompt.
	if code := runClaude([]string{"-root", root, "-dir", wt, "platform", "--", "-p", "hi"}, io.Discard, os.Stderr); code != 0 {
		t.Fatalf("second run: exit %d", code)
	}
	if n := len(gotArgs); gotArgs[n-2] != "-p" || gotArgs[n-1] != "hi" {
		t.Errorf("args = %q", gotArgs)
	}
	if code := runClaude([]string{"-root", root, "residents"}, io.Discard, io.Discard); code == 0 {
		t.Error("an agent without a saved token started")
	}
}
