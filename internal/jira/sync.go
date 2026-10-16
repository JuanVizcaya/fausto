package jira

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/store"
)

// Actions a sync can take on a task.
const (
	Created      = "created"      // a new issue was created
	Linked       = "linked"       // an existing issue was found by label
	Attached     = "attached"     // the task's .md was attached to the issue
	Transitioned = "transitioned" // the issue was moved to the mapped status
	OK           = "ok"           // nothing to do
	Failed       = "error"

	WouldCreate     = "would create"     // dry run
	WouldAttach     = "would attach"     // dry run
	WouldTransition = "would transition" // dry run
)

// Result is what a sync did for one task.
type Result struct {
	Task    string   `json:"task"`
	Key     string   `json:"key,omitempty"`
	URL     string   `json:"url,omitempty"`
	Actions []string `json:"actions"`
	Error   string   `json:"error,omitempty"`
}

// Report is the result of a sync, one entry per task.
type Report struct {
	DryRun  bool     `json:"dryRun,omitempty"`
	Results []Result `json:"results"`
}

// Failed counts the tasks the sync could not bring in line.
func (r *Report) Failed() int {
	n := 0
	for _, res := range r.Results {
		if res.Error != "" {
			n++
		}
	}
	return n
}

// Label marks the issue of a task, so a sync can always find it again.
func Label(taskID string) string { return "fausto-task-" + taskID }

// Attachment is the file name of a task's .md attached to its issue.
func Attachment(taskID string) string { return taskID + ".md" }

// Sync brings the issue of each task in line with it: it finds or creates
// the issue, attaches the task's .md if the issue lacks it, and moves the
// issue to the Jira status mapped from the task's. links holds the keys of
// tasks that don't record one yet. With dryRun it only reads from Jira and
// reports what it would do.
func Sync(ctx context.Context, c *Client, tasks []store.Task, dryRun bool) (links map[string]string, report *Report) {
	links = map[string]string{}
	report = &Report{DryRun: dryRun, Results: []Result{}}
	for i := range tasks {
		t := &tasks[i]
		known := t.Jira
		res := syncTask(ctx, c, t, dryRun)
		if res.Key != "" && known == "" && !dryRun {
			links[t.ID] = res.Key
		}
		if res.Key != "" {
			res.URL = c.IssueURL(res.Key)
		}
		report.Results = append(report.Results, res)
	}
	return links, report
}

func syncTask(ctx context.Context, c *Client, t *store.Task, dryRun bool) Result {
	res := Result{Task: t.ID, Key: t.Jira, Actions: []string{}}
	fail := func(err error) Result {
		res.Actions = append(res.Actions, Failed)
		res.Error = err.Error()
		return res
	}
	want, ok := c.Statuses[t.Status]
	if !ok {
		return fail(fmt.Errorf("no Jira status mapped for task status %q", t.Status))
	}

	var err error
	if res.Key == "" {
		if res.Key, _, err = c.Find(ctx, Label(t.ID)); err != nil {
			return fail(err)
		}
		switch {
		case res.Key != "":
			res.Actions = append(res.Actions, Linked)
		case dryRun:
			// A new issue's initial status depends on the project's
			// workflow; the real sync transitions it if needed.
			res.Actions = append(res.Actions, WouldCreate, WouldAttach)
			return res
		default:
			if res.Key, err = c.Create(ctx, newIssue(t)); err != nil {
				return fail(err)
			}
			res.Actions = append(res.Actions, Created)
		}
	}
	// The attachment's front matter names the issue.
	t.Jira = res.Key

	status, attachments, err := c.State(ctx, res.Key)
	if err != nil {
		return fail(err)
	}
	if !slices.Contains(attachments, Attachment(t.ID)) {
		if dryRun {
			res.Actions = append(res.Actions, WouldAttach)
		} else if err := c.Attach(ctx, res.Key, Attachment(t.ID), t.Markdown(false)); err != nil {
			return fail(fmt.Errorf("attach %s: %w", Attachment(t.ID), err))
		} else {
			res.Actions = append(res.Actions, Attached)
		}
	}
	if !strings.EqualFold(status, want) {
		if dryRun {
			res.Actions = append(res.Actions, WouldTransition)
		} else if err := c.Transition(ctx, res.Key, want); err != nil {
			return fail(err)
		} else {
			res.Actions = append(res.Actions, Transitioned)
		}
	}
	if len(res.Actions) == 0 {
		res.Actions = append(res.Actions, OK)
	}
	return res
}

func newIssue(t *store.Task) Issue {
	labels := []string{"fausto", Label(t.ID)}
	if t.Agent != "" && !slices.Contains(labels, t.Agent) {
		labels = append(labels, t.Agent)
	}
	return Issue{
		Summary:     fmt.Sprintf("[%s] %s", t.Agent, t.Title),
		Description: ADF(t.Body, fmt.Sprintf("fausto task %s; the attached %s is what the agent works from.", t.ID, Attachment(t.ID))),
		Labels:      labels,
	}
}

// SyncStore syncs the tasks in s (only those in ids, when given) and
// records the keys of newly linked issues.
func SyncStore(ctx context.Context, c *Client, s store.Store, ids []string, dryRun bool) (*Report, error) {
	tasks, err := s.Tasks(ctx, store.TaskFilter{IDs: ids})
	if err != nil {
		return nil, err
	}
	links, report := Sync(ctx, c, tasks, dryRun)
	for id, key := range links {
		if err := s.SetTaskJira(ctx, id, key); err != nil {
			return report, fmt.Errorf("record the Jira key of %s: %w", id, err)
		}
	}
	return report, nil
}
