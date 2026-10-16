// Package storetest checks that a store.Store behaves as the interface
// promises, so every implementation is held to the same contract.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// Run checks the store that open returns; each subtest gets a new, empty
// one.
func Run(t *testing.T, open func(t *testing.T) store.Store) {
	ctx := context.Background()

	t.Run("proposals", func(t *testing.T) {
		s := open(t)
		p := &store.Proposal{ID: "2026-10-01-a", Title: "A", Author: "Me <me@example.com>", Created: "2026-10-01", Status: "open",
			BaseCommit: "abc", Changes: []edit.Change{{Kind: "principle", ID: "p", Value: json.RawMessage(`{"id":"p","rationale":"x"}`)}}}
		if err := s.CreateProposal(ctx, p); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateProposal(ctx, p); !errors.Is(err, store.ErrExists) {
			t.Errorf("duplicate: err = %v", err)
		}
		if err := s.CreateProposal(ctx, &store.Proposal{ID: "2026-10-02-b", Title: "B", Created: "2026-10-02", Status: "open"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Proposal(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "A" || len(got.Changes) != 1 || string(got.Changes[0].Value) != `{"id":"p","rationale":"x"}` || got.BaseCommit != "abc" {
			t.Errorf("proposal = %+v", got)
		}
		list, err := s.Proposals(ctx)
		if err != nil || len(list) != 2 || list[0].ID != "2026-10-02-b" {
			t.Errorf("list = %+v, %v (want newest first)", list, err)
		}
		if err := s.CloseProposal(ctx, p.ID, "accepted", "2026-10-03", "def", []string{"t1"}); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseProposal(ctx, p.ID, "rejected", "", "", nil); !errors.Is(err, store.ErrChanged) {
			t.Errorf("close twice: err = %v", err)
		}
		if err := s.CloseProposal(ctx, "nope", "rejected", "", "", nil); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("close unknown: err = %v", err)
		}
		got, _ = s.Proposal(ctx, p.ID)
		if got.Status != "accepted" || got.Accepted != "2026-10-03" || got.Commit != "def" || !slices.Equal(got.Tasks, []string{"t1"}) {
			t.Errorf("accepted = %+v", got)
		}
		if _, err := s.Proposal(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("unknown: err = %v", err)
		}
	})

	t.Run("tasks", func(t *testing.T) {
		s := open(t)
		tasks := []store.Task{
			{ID: "b--platform", Agent: "platform", Status: "open", Created: "2026-10-02", Title: "B", Milestone: "m1", Body: "Do B.\n"},
			{ID: "a--residents", Agent: "residents", Status: "open", Created: "2026-10-01", Title: "A", Components: []string{"residents"}, Body: "Do A.\n"},
		}
		if err := s.CreateTasks(ctx, tasks); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateTasks(ctx, []store.Task{{ID: "c--x", Agent: "x", Status: "open"}, tasks[0]}); !errors.Is(err, store.ErrExists) {
			t.Errorf("duplicate: err = %v", err)
		}
		if _, err := s.Task(ctx, "c--x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("a failed create added c--x: %v", err)
		}
		all, err := s.Tasks(ctx, store.TaskFilter{})
		if err != nil || len(all) != 2 || all[0].ID != "a--residents" {
			t.Errorf("all = %+v, %v (want oldest first)", all, err)
		}
		if got, _ := s.Tasks(ctx, store.TaskFilter{Agent: "platform"}); len(got) != 1 || got[0].ID != "b--platform" {
			t.Errorf("by agent = %+v", got)
		}
		if got, _ := s.Tasks(ctx, store.TaskFilter{Milestone: "m1"}); len(got) != 1 {
			t.Errorf("by milestone = %+v", got)
		}
		if got, _ := s.Tasks(ctx, store.TaskFilter{IDs: []string{"a--residents"}}); len(got) != 1 || got[0].Body != "Do A.\n" {
			t.Errorf("by id = %+v", got)
		}

		if err := s.SetTaskStatus(ctx, "b--platform", "open", "in-progress", "agent platform"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetTaskStatus(ctx, "b--platform", "open", "done", "someone"); !errors.Is(err, store.ErrChanged) {
			t.Errorf("stale status: err = %v", err)
		}
		if err := s.SetTaskStatus(ctx, "nope", "open", "done", "x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("unknown: err = %v", err)
		}
		if got, _ := s.Tasks(ctx, store.TaskFilter{Statuses: []string{"in-progress"}}); len(got) != 1 {
			t.Errorf("by status = %+v", got)
		}
		if err := s.SetTaskJira(ctx, "b--platform", "FAUS-1"); err != nil {
			t.Fatal(err)
		}
		got, err := s.Task(ctx, "b--platform")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "in-progress" || got.Jira != "FAUS-1" || len(got.History) != 1 || got.History[0].By != "agent platform" || got.History[0].To != "in-progress" {
			t.Errorf("task = %+v", got)
		}
		if err := s.SetTaskJira(ctx, "nope", "FAUS-2"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("jira unknown: err = %v", err)
		}
	})
}
