package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A work tree's connection is ignored through the clone's shared exclude
// file, once, and the project's .gitignore is left alone.
func TestSaveExcludes(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "initial")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", wt)

	for _, dir := range []string{wt, repo} {
		if err := (&Config{URL: "http://x", Token: "fausto_t"}).Save(dir); err != nil {
			t.Fatal(err)
		}
	}
	exclude, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Count(string(exclude), ".fausto/") != 1 {
		t.Errorf("exclude:\n%s", exclude)
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = wt
	if out, _ := cmd.Output(); len(out) != 0 {
		t.Errorf("work tree not clean:\n%s", out)
	}
	if info, err := os.Stat(filepath.Join(wt, ConfigFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config file: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); err == nil {
		t.Error("a .gitignore was written")
	}

	c, err := Resolve(wt)
	if err != nil || c == nil || c.Token != "fausto_t" || c.URL != "http://x" {
		t.Errorf("resolve = %+v, %v", c, err)
	}
	t.Setenv("FAUSTO_TOKEN", "fausto_env")
	if c, _ := Resolve(wt); c.Token != "fausto_env" {
		t.Errorf("FAUSTO_TOKEN doesn't win: %+v", c)
	}
	t.Setenv("FAUSTO_TOKEN", "")
	if c, _ := Resolve(t.TempDir()); c != nil {
		t.Errorf("unconnected = %+v", c)
	}
}
