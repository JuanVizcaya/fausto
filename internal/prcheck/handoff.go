package prcheck

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/github"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// EventType is the repository_dispatch event that hands a checked pull
// request to the project's workflows.
const EventType = "fausto-spec"

// SpecRef is the hidden ref holding spec revision n: a commit with only the
// spec's files, never a branch and never merged, so the spec still lives in
// fausto and the project's history never gains it.
func SpecRef(n int) string { return fmt.Sprintf("refs/fausto/spec/r%d", n) }

// Dispatch is the client_payload of EventType.
type Dispatch struct {
	PR       int    `json:"pr"`
	HeadSHA  string `json:"head_sha"`
	Revision int    `json:"revision"`
	SpecRef  string `json:"spec_ref"`
}

// handOff publishes the spec revision p was checked against at SpecRef and
// dispatches EventType, so the project's workflows can run what fausto's
// server must not: its project checks, which are the pull request's code,
// and its reviewers. The token needs contents write.
func (c *Checker) handOff(ctx context.Context, p pull, rev *store.SpecRevision) error {
	commit, err := c.specCommit(ctx, rev)
	if err != nil {
		return err
	}
	ref := SpecRef(rev.N)
	if _, err := c.git(ctx, github.GitAuthEnv(c.GH.Token), "push", "--quiet", c.remote(), commit+":"+ref); err != nil {
		return err
	}
	return c.GH.Call(ctx, "POST", "/repos/"+c.Repo+"/dispatches", map[string]any{
		"event_type":     EventType,
		"client_payload": Dispatch{PR: p.Number, HeadSHA: p.Head.SHA, Revision: rev.N, SpecRef: ref},
	}, nil)
}

// specCommit writes rev's files as a commit in the bare clone. The commit
// depends only on the revision, so publishing it again changes nothing.
func (c *Checker) specCommit(ctx context.Context, rev *store.SpecRevision) (string, error) {
	index, err := os.CreateTemp("", "fausto-index-")
	if err != nil {
		return "", err
	}
	index.Close()
	os.Remove(index.Name()) // git creates it
	defer os.Remove(index.Name())
	env := []string{"GIT_INDEX_FILE=" + index.Name()}
	files := rev.FileMap()
	for _, path := range slices.Sorted(maps.Keys(files)) {
		blob, err := c.gitIn(ctx, files[path], "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err := c.git(ctx, env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path); err != nil {
			return "", err
		}
	}
	tree, err := c.git(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	date := fmt.Sprintf("@%d +0000", max(rev.At.Unix(), 0))
	commit, err := c.git(ctx, []string{
		"GIT_AUTHOR_NAME=fausto", "GIT_AUTHOR_EMAIL=fausto@localhost", "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=fausto", "GIT_COMMITTER_EMAIL=fausto@localhost", "GIT_COMMITTER_DATE=" + date,
	}, "commit-tree", strings.TrimSpace(string(tree)), "-m", fmt.Sprintf("fausto spec r%d", rev.N))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(commit)), nil
}

// gitIn runs git in the bare clone with stdin, returning trimmed output.
func (c *Checker) gitIn(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=*"}, args...)...)
	cmd.Dir = c.Clone
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
