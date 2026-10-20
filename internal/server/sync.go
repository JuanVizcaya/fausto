package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/JuanVizcaya/fausto/internal/prcheck"
	"github.com/JuanVizcaya/fausto/internal/proposal"
)

// syncer brings accepted proposals up to date with their reviews (see
// proposal.Store.Sync), and mirrors the tasks that creates in Jira. A nil
// *syncer does nothing.
type syncer struct {
	ps     *proposal.Store
	js     *jiraSync
	logger *slog.Logger
	every  time.Duration
	prs    *prcheck.Checker // nil: no pull request checks

	mu   sync.Mutex
	last time.Time
}

// minGap is how long a sync triggered by looking at proposals waits after
// the last one.
const minGap = 10 * time.Second

func newSyncer(ps *proposal.Store, js *jiraSync, logger *slog.Logger, every time.Duration) *syncer {
	if ps == nil {
		return nil
	}
	return &syncer{ps: ps, js: js, logger: logger, every: every}
}

// run syncs, and mirrors in Jira the tasks that created; the outcome of
// that is nil when there was nothing to mirror.
func (s *syncer) run(ctx context.Context) ([]proposal.SyncResult, *jiraOutcome, error) {
	if s == nil {
		return nil, nil, nil
	}
	s.mu.Lock()
	s.last = time.Now()
	s.mu.Unlock()
	results, err := s.ps.Sync(ctx)
	var tasks []string
	for _, r := range results {
		if r.Error != "" {
			s.logger.Warn("proposal sync", "proposal", r.Proposal, "error", r.Error)
		} else {
			s.logger.Info("proposal sync", "proposal", r.Proposal, "status", r.Status)
		}
		tasks = append(tasks, r.Tasks...)
	}
	return results, s.js.after(ctx, tasks...), err
}

// maybe syncs unless a sync ran in the last minGap; errors are logged.
func (s *syncer) maybe(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	recent := time.Since(s.last) < minGap
	s.mu.Unlock()
	if recent {
		return
	}
	if _, _, err := s.run(ctx); err != nil {
		s.logger.Warn("proposal sync", "error", err)
	}
}

func (s *syncer) loop(ctx context.Context) {
	s.checkPRs(ctx) // pull requests opened while fausto was off
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, _, err := s.run(ctx); err != nil {
				s.logger.Warn("proposal sync", "error", err)
			}
			s.checkPRs(ctx)
		}
	}
}

// checkPRs checks the pull requests not yet checked at the latest spec
// revision; errors are logged.
func (s *syncer) checkPRs(ctx context.Context) {
	if s.prs == nil {
		return
	}
	results, err := s.prs.Run(ctx)
	if err != nil {
		s.logger.Warn("pull request checks", "repo", s.prs.Repo, "error", err)
		return
	}
	for _, r := range results {
		switch {
		case r.Error != "":
			s.logger.Warn("pull request check", "pr", r.PR, "error", r.Error)
		case !r.Skipped:
			s.logger.Info("pull request check", "pr", r.PR, "sha", r.SHA, "state", r.State, "spec revision", r.Revision)
		}
	}
}

func nonNilResults(r []proposal.SyncResult) []proposal.SyncResult {
	if r == nil {
		return []proposal.SyncResult{}
	}
	return r
}
