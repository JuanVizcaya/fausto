package proposal

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/edit"
)

const zeroOID = "0000000000000000000000000000000000000000"

// gitError carries git's stderr.
type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	msg := strings.TrimSpace(e.stderr)
	if msg == "" {
		msg = e.err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), msg)
}

// git runs git in dir with extra environment and optional stdin, returning
// trimmed stdout.
func (s *Store) git(dir string, env []string, stdin []byte, args ...string) (string, error) {
	out, err := s.gitBytes(dir, env, stdin, args...)
	return strings.TrimRight(string(out), "\n"), err
}

func (s *Store) gitBytes(dir string, env []string, stdin []byte, args ...string) ([]byte, error) {
	if dir == "" {
		dir = s.Root
	}
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=*"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), s.identityEnv()...), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &gitError{args: args, stderr: stderr.String(), err: err}
	}
	return stdout.Bytes(), nil
}

// identityEnv sets the author and committer from Store.Author when given;
// otherwise git uses its own configuration.
func (s *Store) identityEnv() []string {
	name, email, ok := parseIdent(s.Author)
	if !ok {
		return nil
	}
	return []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}
}

// parseIdent splits "Name <email>".
func parseIdent(ident string) (name, email string, ok bool) {
	name, rest, found := strings.Cut(ident, "<")
	if !found || !strings.HasSuffix(strings.TrimSpace(rest), ">") {
		return "", "", false
	}
	return strings.TrimSpace(name), strings.TrimSuffix(strings.TrimSpace(rest), ">"), true
}

// author returns the identity recorded in proposals.
func (s *Store) author() string {
	if _, _, ok := parseIdent(s.Author); ok {
		return s.Author
	}
	ident, err := s.git("", nil, nil, "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		return "unknown"
	}
	// "Name <email> 1700000000 +0000"
	if i := strings.LastIndex(ident, ">"); i >= 0 {
		return ident[:i+1]
	}
	return ident
}

func (s *Store) revParse(rev string) (string, error) {
	return s.git("", nil, nil, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

func (s *Store) refExists(ref string) bool {
	_, err := s.git("", nil, nil, "show-ref", "--verify", "--quiet", ref)
	return err == nil
}

// materialize extracts the tree of rev (a commit or tree) into a new
// temporary directory.
func (s *Store) materialize(rev string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "fausto-tree-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	data, err := s.gitBytes("", nil, nil, "archive", "--format=tar", rev)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(dst, dir+string(filepath.Separator)) {
			continue // never write outside dir
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		_, err = io.Copy(f, tr)
		f.Close()
		if err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dir, cleanup, nil
}

// writeFiles writes (or deletes) files under dir.
func writeFiles(dir string, files edit.Files) error {
	for path, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if data == nil {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// commit creates a commit whose tree is tree with files applied, without
// touching any work tree or the repository's index.
func (s *Store) commit(tree string, files edit.Files, parents []string, message string) (string, error) {
	tmp, err := os.MkdirTemp("", "fausto-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, err := s.git("", env, nil, "read-tree", tree); err != nil {
		return "", err
	}
	for path, data := range files {
		if data == nil {
			if _, err := s.git("", env, nil, "update-index", "--force-remove", "--", path); err != nil {
				return "", err
			}
			continue
		}
		oid, err := s.git("", nil, data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err := s.git("", env, nil, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+path); err != nil {
			return "", err
		}
	}
	newTree, err := s.git("", env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", newTree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-F", "-")
	return s.git("", nil, []byte(message), args...)
}

// worktreeFor returns the path of the work tree that has branch checked
// out, or "".
func (s *Store) worktreeFor(branch string) (string, error) {
	out, err := s.git("", nil, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		}
		if line == "branch refs/heads/"+branch {
			return path, nil
		}
	}
	return "", nil
}

// Who is the identity changes are recorded under: Author, else git's.
func (s *Store) Who() string { return s.author() }
