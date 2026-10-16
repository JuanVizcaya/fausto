package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// jiraSync mirrors tasks as Jira issues after the changes fausto makes.
// A nil *jiraSync means the integration is off.
type jiraSync struct {
	mu     sync.Mutex // one sync at a time
	client *jira.Client
	db     store.Store
	logger *slog.Logger
}

// jiraOutcome is what a best-effort sync reports alongside the change that
// triggered it: the change itself has already succeeded.
type jiraOutcome struct {
	Report *jira.Report `json:"report,omitempty"`
	Error  string       `json:"error,omitempty"`
}

func newJiraSync(c *jira.Client, ps *proposal.Store, logger *slog.Logger) *jiraSync {
	if c == nil || ps == nil {
		return nil
	}
	return &jiraSync{client: c, db: ps.DB, logger: logger}
}

// run syncs the tasks in ids (all tasks when none are given).
func (j *jiraSync) run(ctx context.Context, ids ...string) (*jira.Report, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	report, err := jira.SyncStore(ctx, j.client, j.db, ids, false)
	if err != nil {
		j.logger.Warn("jira sync", "error", err)
	} else if n := report.Failed(); n > 0 {
		j.logger.Warn("jira sync", "failed tasks", n)
	}
	return report, err
}

// issues returns the live Jira issues of the tasks, each with the Jira
// status its task's status maps to, so the UI can flag a drifted issue.
func (j *jiraSync) issues(ctx context.Context) ([]jiraIssue, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := j.client.Issues(ctx)
	if err != nil {
		return nil, err
	}
	tasks, err := j.db.Tasks(ctx, store.TaskFilter{})
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for _, t := range tasks {
		status[t.ID] = t.Status
	}
	out := make([]jiraIssue, 0, len(list))
	for _, is := range list {
		out = append(out, jiraIssue{IssueInfo: is, Want: j.client.Statuses[status[is.Task]]})
	}
	return out, nil
}

// jiraIssue is an issue as the Tasks tab shows it.
type jiraIssue struct {
	jira.IssueInfo
	// Want is the Jira status the task's status maps to; empty when the
	// issue belongs to no task on the base branch.
	Want string `json:"want,omitempty"`
}

// after syncs ids after a change and says how it went; nil when the
// integration is off or there is nothing to sync.
func (j *jiraSync) after(ctx context.Context, ids ...string) *jiraOutcome {
	if j == nil || len(ids) == 0 {
		return nil
	}
	report, err := j.run(ctx, ids...)
	out := &jiraOutcome{Report: report}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}
