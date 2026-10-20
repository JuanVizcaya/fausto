// Package client is how agents reach fausto: its HTTP API with an agent's
// token, so an agent needs no database access. A clone's connection is kept
// in .fausto/config.json, which git ignores through .git/info/exclude, so
// the project's own files never mention fausto.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JuanVizcaya/fausto/internal/store"
)

// ConfigFile is where a clone keeps its connection to fausto.
const ConfigFile = ".fausto/config.json"

// DefaultURL is where a local fausto listens.
const DefaultURL = "http://127.0.0.1:7070"

// Config is a clone's connection to fausto.
type Config struct {
	URL string `json:"url"`
	// Token is the agent token this clone works with.
	Token string `json:"token,omitempty"`
	// Agents are tokens by agent id, kept in the orchestrator's clone for
	// `fausto claude <agent>` to hand to each agent's work tree.
	Agents map[string]string `json:"agents,omitempty"`
}

// LoadConfig reads root's ConfigFile; a missing one is an empty Config.
func LoadConfig(root string) (*Config, error) {
	c := &Config{}
	if root == "" {
		return c, nil
	}
	data, err := os.ReadFile(filepath.Join(root, ConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	return c, nil
}

// Save writes c to root's ConfigFile, readable only by you, and makes git
// ignore .fausto/ without touching the project's .gitignore.
func (c *Config) Save(root string) error {
	p := filepath.Join(root, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return Exclude(root)
}

// Exclude adds .fausto/ to the clone's .git/info/exclude (shared by its
// work trees), if it isn't there. Outside a git repository it does nothing.
func Exclude(root string) error {
	return ExcludePath(root, ".fausto/", "fausto's connection and pulled tasks")
}

// ExcludePath adds pattern, with a comment saying why, to the clone's
// .git/info/exclude if it isn't there.
func ExcludePath(root, pattern, why string) error {
	cmd := exec.Command("git", "-c", "safe.directory=*", "rev-parse", "--git-common-dir")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	p := filepath.Join(dir, "info", "exclude")
	cur, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	bare := strings.Trim(pattern, "/")
	for _, line := range strings.Split(string(cur), "\n") {
		if l := strings.Trim(strings.TrimSpace(line), "/"); l == bare {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if len(cur) > 0 && !bytes.HasSuffix(cur, []byte("\n")) {
		cur = append(cur, '\n')
	}
	cur = append(cur, []byte("# "+why+"\n"+pattern+"\n")...)
	return os.WriteFile(p, cur, 0o644)
}

// Client calls fausto's agent API with one agent's token.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// Resolve returns the client for root: FAUSTO_URL and FAUSTO_TOKEN when
// set, else root's ConfigFile. It returns nil when there is no token.
func Resolve(root string) (*Client, error) {
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	c := &Client{URL: cfg.URL, Token: cfg.Token}
	if v := os.Getenv("FAUSTO_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("FAUSTO_TOKEN"); v != "" {
		c.Token = v
	}
	if c.Token == "" {
		return nil, nil
	}
	if c.URL == "" {
		c.URL = DefaultURL
	}
	return c, nil
}

// Me is who a token is for.
type Me struct {
	Project string `json:"project"`
	Agent   string `json:"agent"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Token   string `json:"token"`
}

// Definition is an agent's definition: when to use it, its system prompt,
// and both as a Claude Code agent file.
type Definition struct {
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Markdown    string `json:"markdown"`
}

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var m Me
	return &m, c.call(ctx, http.MethodGet, "/api/agent/me", nil, &m)
}

func (c *Client) Definition(ctx context.Context) (*Definition, error) {
	var d Definition
	return &d, c.call(ctx, http.MethodGet, "/api/agent/definition", nil, &d)
}

// Spec returns the spec fausto keeps, its files by path (system/...), and
// its revision; nil and 0 when the project keeps its own.
func (c *Client) Spec(ctx context.Context) (map[string][]byte, int, error) {
	var res struct {
		Revision int               `json:"revision"`
		Files    map[string]string `json:"files"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/agent/spec", nil, &res); err != nil {
		return nil, 0, err
	}
	if len(res.Files) == 0 {
		return nil, 0, nil
	}
	out := make(map[string][]byte, len(res.Files))
	for p, content := range res.Files {
		out[p] = []byte(content)
	}
	return out, res.Revision, nil
}

// Tasks returns the agent's tasks with the statuses (all when none).
func (c *Client) Tasks(ctx context.Context, statuses ...string) ([]store.Task, error) {
	path := "/api/agent/tasks"
	if len(statuses) > 0 {
		path += "?status=" + url.QueryEscape(strings.Join(statuses, ","))
	}
	var out []store.Task
	return out, c.call(ctx, http.MethodGet, path, nil, &out)
}

// SetStatus moves one of the agent's tasks to status and returns the status
// it had.
func (c *Client) SetStatus(ctx context.Context, id, status string) (string, error) {
	var res struct {
		From string `json:"from"`
	}
	err := c.call(ctx, http.MethodPost, "/api/agent/tasks/"+url.PathEscape(id)+"/status", map[string]string{"status": status}, &res)
	return res.From, err
}

// Error is an unsuccessful response from fausto.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("fausto: %s (%d)", e.Message, e.Status) }

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("reach fausto at %s: %w", c.URL, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(res.StatusCode)
		}
		return &Error{Status: res.StatusCode, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
