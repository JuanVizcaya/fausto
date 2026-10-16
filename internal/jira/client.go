// Package jira mirrors fausto tasks as Jira Cloud issues. Task files stay the
// source of truth: Sync creates an issue for each task that has none, finds
// it again by its fausto-task-<id> label so retries never duplicate it, and
// moves it to the Jira status mapped from the task's status.
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config says which Jira to use and how.
type Config struct {
	URL       string // e.g. https://cmot.atlassian.net
	Email     string
	Token     string // API token; never logged or returned
	Project   string // project key, e.g. HAB
	IssueType string
	// Statuses maps a task status to the Jira status name its issue gets.
	Statuses map[string]string
}

// DefaultStatuses is the status mapping when FAUSTO_JIRA_STATUSES is unset.
const DefaultStatuses = "open=To Do,in-progress=In Progress,done=Done,dropped=Done"

// ConfigFromEnv reads the FAUSTO_JIRA_* variables. It returns nil, nil when
// FAUSTO_JIRA_URL is unset: the integration is off.
func ConfigFromEnv() (*Config, error) {
	c := &Config{
		URL:       strings.TrimRight(os.Getenv("FAUSTO_JIRA_URL"), "/"),
		Email:     os.Getenv("FAUSTO_JIRA_EMAIL"),
		Token:     os.Getenv("FAUSTO_JIRA_TOKEN"),
		Project:   os.Getenv("FAUSTO_JIRA_PROJECT"),
		IssueType: os.Getenv("FAUSTO_JIRA_ISSUE_TYPE"),
	}
	if c.URL == "" {
		return nil, nil
	}
	if c.IssueType == "" {
		c.IssueType = "Task"
	}
	statuses := os.Getenv("FAUSTO_JIRA_STATUSES")
	if statuses == "" {
		statuses = DefaultStatuses
	}
	var err error
	if c.Statuses, err = ParseStatuses(statuses); err != nil {
		return nil, fmt.Errorf("FAUSTO_JIRA_STATUSES: %w", err)
	}
	var missing []string
	for name, v := range map[string]string{"FAUSTO_JIRA_EMAIL": c.Email, "FAUSTO_JIRA_TOKEN": c.Token, "FAUSTO_JIRA_PROJECT": c.Project} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("FAUSTO_JIRA_URL is set but %s is not", strings.Join(missing, ", "))
	}
	if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("FAUSTO_JIRA_URL %q is not an http(s) URL", c.URL)
	}
	return c, nil
}

// ParseStatuses parses "open=To Do,done=Done".
func ParseStatuses(s string) (map[string]string, error) {
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("want task-status=Jira status pairs, got %q", pair)
		}
		m[k] = v
	}
	return m, nil
}

// Client calls the Jira Cloud REST API (v3).
type Client struct {
	Config
	HTTP *http.Client
}

// NewClient returns a client for c.
func NewClient(c Config) *Client {
	return &Client{Config: c, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// IssueURL is the browser link to an issue.
func (c *Client) IssueURL(key string) string { return c.URL + "/browse/" + key }

// Issue is what Create needs.
type Issue struct {
	Summary     string
	Description map[string]any // ADF document
	Labels      []string
}

// Find returns the key and status of the project's issue carrying label,
// or "" when there is none.
func (c *Client) Find(ctx context.Context, label string) (key, status string, err error) {
	var res struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Status struct {
					Name string `json:"name"`
				} `json:"status"`
			} `json:"fields"`
		} `json:"issues"`
	}
	body := map[string]any{
		"jql":        fmt.Sprintf("project = %q AND labels = %q ORDER BY created ASC", c.Project, label),
		"fields":     []string{"status"},
		"maxResults": 1,
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &res); err != nil {
		return "", "", err
	}
	if len(res.Issues) == 0 {
		return "", "", nil
	}
	return res.Issues[0].Key, res.Issues[0].Fields.Status.Name, nil
}

// IssueInfo is what fausto shows about a task's issue.
type IssueInfo struct {
	Key      string `json:"key"`
	URL      string `json:"url"`
	Status   string `json:"status"`
	Assignee string `json:"assignee,omitempty"` // display name; empty when unassigned
	Avatar   string `json:"avatar,omitempty"`
	Priority string `json:"priority,omitempty"`
	Updated  string `json:"updated,omitempty"`
	// Task is the fausto task id, from the issue's fausto-task-<id> label.
	Task string `json:"task,omitempty"`
}

// Issues returns the project's issues labeled fausto.
func (c *Client) Issues(ctx context.Context) ([]IssueInfo, error) {
	type page struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Status struct {
					Name string `json:"name"`
				} `json:"status"`
				Assignee *struct {
					DisplayName string            `json:"displayName"`
					AvatarURLs  map[string]string `json:"avatarUrls"`
				} `json:"assignee"`
				Priority *struct {
					Name string `json:"name"`
				} `json:"priority"`
				Updated string   `json:"updated"`
				Labels  []string `json:"labels"`
			} `json:"fields"`
		} `json:"issues"`
		NextPageToken string `json:"nextPageToken"`
	}
	out := []IssueInfo{}
	token := ""
	for range 50 { // at most 5000 issues
		body := map[string]any{
			"jql":        fmt.Sprintf("project = %q AND labels = \"fausto\" ORDER BY key ASC", c.Project),
			"fields":     []string{"status", "assignee", "priority", "updated", "labels"},
			"maxResults": 100,
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		var p page
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &p); err != nil {
			return nil, err
		}
		for _, is := range p.Issues {
			info := IssueInfo{Key: is.Key, URL: c.IssueURL(is.Key), Status: is.Fields.Status.Name, Updated: is.Fields.Updated}
			if a := is.Fields.Assignee; a != nil {
				info.Assignee, info.Avatar = a.DisplayName, a.AvatarURLs["24x24"]
			}
			if is.Fields.Priority != nil {
				info.Priority = is.Fields.Priority.Name
			}
			for _, l := range is.Fields.Labels {
				if id, ok := strings.CutPrefix(l, "fausto-task-"); ok {
					info.Task = id
				}
			}
			out = append(out, info)
		}
		if token = p.NextPageToken; token == "" {
			break
		}
	}
	return out, nil
}

// Create creates an issue in the project and returns its key.
func (c *Client) Create(ctx context.Context, issue Issue) (string, error) {
	var res struct {
		Key string `json:"key"`
	}
	body := map[string]any{"fields": map[string]any{
		"project":     map[string]string{"key": c.Project},
		"issuetype":   map[string]string{"name": c.IssueType},
		"summary":     issue.Summary,
		"description": issue.Description,
		"labels":      issue.Labels,
	}}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/issue", body, &res); err != nil {
		return "", err
	}
	if res.Key == "" {
		return "", errors.New("create issue: Jira returned no key")
	}
	return res.Key, nil
}

// State returns the name of an issue's status and the file names of its
// attachments.
func (c *Client) State(ctx context.Context, key string) (status string, attachments []string, err error) {
	var res struct {
		Fields struct {
			Status struct {
				Name string `json:"name"`
			} `json:"status"`
			Attachment []struct {
				Filename string `json:"filename"`
			} `json:"attachment"`
		} `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?fields=status,attachment", nil, &res); err != nil {
		return "", nil, err
	}
	for _, a := range res.Fields.Attachment {
		attachments = append(attachments, a.Filename)
	}
	return res.Fields.Status.Name, attachments, nil
}

// Attach uploads a file to an issue.
func (c *Client) Attach(ctx context.Context, key, filename string, data []byte) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err == nil {
		_, err = part.Write(data)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		return err
	}
	// Jira refuses attachment uploads without this header (XSRF check).
	header := http.Header{"X-Atlassian-Token": {"no-check"}, "Content-Type": {w.FormDataContentType()}}
	return c.send(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/attachments", &body, header, nil)
}

// Transition moves an issue to the status named status, through whichever
// of its available transitions leads there.
func (c *Client) Transition(ctx context.Context, key, status string) error {
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	var res struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &res); err != nil {
		return err
	}
	var to []string
	for _, t := range res.Transitions {
		if strings.EqualFold(t.To.Name, status) {
			return c.do(ctx, http.MethodPost, path, map[string]any{"transition": map[string]string{"id": t.ID}}, nil)
		}
		to = append(to, t.To.Name)
	}
	return fmt.Errorf("%s: no transition to %q (available: %s)", key, status, strings.Join(to, ", "))
}

// do sends a JSON request with basic auth and decodes a JSON response into
// out. Errors carry Jira's messages but never the credentials.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	header := http.Header{}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
		header.Set("Content-Type", "application/json")
	}
	return c.send(ctx, method, path, r, header, out)
}

// send sends a request with basic auth and the given headers, and decodes
// a JSON response into out.
func (c *Client) send(ctx context.Context, method, path string, body io.Reader, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.SetBasicAuth(c.Email, c.Token)
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return fmt.Errorf("jira %s %s: %w", method, path, uerr.Err)
		}
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("jira %s %s: %s%s", method, path, res.Status, jiraMessages(data))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// jiraMessages extracts Jira's error messages from an error response.
func jiraMessages(data []byte) string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(data, &e) != nil {
		return ""
	}
	msgs := e.ErrorMessages
	for k, v := range e.Errors {
		msgs = append(msgs, k+": "+v)
	}
	if len(msgs) == 0 {
		return ""
	}
	return ": " + strings.Join(msgs, "; ")
}
