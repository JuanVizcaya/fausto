// Package storetest checks that a store.Store behaves as the interface
// promises, so every implementation is held to the same contract.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
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
		pending := []store.Task{{ID: "t1", Agent: "platform", Status: "open", Title: "A", Body: "Do A.\n"}}
		if err := s.MoveProposal(ctx, p.ID, "open", "accepted", store.ProposalUpdate{Date: "2026-10-03", Commit: "def",
			Branch: "fausto/a", PR: 7, URL: "https://github.com/o/r/pull/7", PendingTasks: pending, SpecDiff: "+id: x"}); err != nil {
			t.Fatal(err)
		}
		if err := s.MoveProposal(ctx, p.ID, "open", "rejected", store.ProposalUpdate{}); !errors.Is(err, store.ErrChanged) {
			t.Errorf("move from a stale status: err = %v", err)
		}
		if err := s.MoveProposal(ctx, p.ID, "accepted", "open", store.ProposalUpdate{}); err == nil {
			t.Error("accepted → open: no error")
		}
		if err := s.MoveProposal(ctx, "nope", "open", "rejected", store.ProposalUpdate{}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("move unknown: err = %v", err)
		}
		got, _ = s.Proposal(ctx, p.ID)
		if got.Status != "accepted" || got.Accepted != "2026-10-03" || got.Commit != "def" || got.Branch != "fausto/a" || got.PR != 7 ||
			got.URL != "https://github.com/o/r/pull/7" || len(got.PendingTasks) != 1 || got.PendingTasks[0].Body != "Do A.\n" || len(got.Tasks) != 0 ||
			got.SpecDiff != "+id: x" {
			t.Errorf("accepted = %+v", got)
		}
		if err := s.MoveProposal(ctx, p.ID, "accepted", "applied", store.ProposalUpdate{Date: "2026-10-04", Merged: "fed", Tasks: []string{"t1"}, Revision: 4}); err != nil {
			t.Fatal(err)
		}
		got, _ = s.Proposal(ctx, p.ID)
		if got.Status != "applied" || got.Applied != "2026-10-04" || got.Merged != "fed" || got.Commit != "def" || got.PR != 7 || !slices.Equal(got.Tasks, []string{"t1"}) || got.Revision != 4 {
			t.Errorf("applied = %+v", got)
		}
		if err := s.MoveProposal(ctx, "2026-10-02-b", "open", "rejected", store.ProposalUpdate{Date: "2026-10-05"}); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Proposal(ctx, "2026-10-02-b"); got.Status != "rejected" || got.Closed != "2026-10-05" || got.Accepted != "" {
			t.Errorf("rejected = %+v", got)
		}
		if _, err := s.Proposal(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("unknown: err = %v", err)
		}
	})

	t.Run("spec revisions", func(t *testing.T) {
		s := open(t)
		if _, err := s.SpecRevision(ctx, 0); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("no revisions: err = %v", err)
		}
		r1 := &store.SpecRevision{N: 1, By: "Me", Message: "import", Files: []store.SpecFile{{Path: "system/system.yaml", Content: "name: a\n"}}}
		if err := s.AddSpecRevision(ctx, r1); err != nil {
			t.Fatal(err)
		}
		if err := s.AddSpecRevision(ctx, &store.SpecRevision{N: 1}); !errors.Is(err, store.ErrChanged) {
			t.Errorf("revision 1 twice: err = %v", err)
		}
		if err := s.AddSpecRevision(ctx, &store.SpecRevision{N: 3}); !errors.Is(err, store.ErrChanged) {
			t.Errorf("revision 3 after 1: err = %v", err)
		}
		r2 := &store.SpecRevision{N: 2, By: "Me", Proposal: "p", Message: "change", Files: []store.SpecFile{
			{Path: "system/system.yaml", Content: "name: b\n"}, {Path: "system/agents/x.yaml", Content: "id: x\n"}}}
		if err := s.AddSpecRevision(ctx, r2); err != nil {
			t.Fatal(err)
		}
		latest, err := s.SpecRevision(ctx, 0)
		if err != nil || latest.N != 2 || latest.FileMap()["system/agents/x.yaml"] == nil || latest.At.IsZero() {
			t.Errorf("latest = %+v, %v", latest, err)
		}
		if first, err := s.SpecRevision(ctx, 1); err != nil || string(first.FileMap()["system/system.yaml"]) != "name: a\n" {
			t.Errorf("revision 1 = %+v, %v", first, err)
		}
		if _, err := s.SpecRevision(ctx, 9); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("revision 9: err = %v", err)
		}
		list, err := s.SpecRevisions(ctx)
		if err != nil || len(list) != 2 || list[0].N != 2 || list[0].Proposal != "p" || len(list[0].Files) != 0 {
			t.Errorf("revisions = %+v, %v", list, err)
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

// RunProjects checks the project registry that open returns, and that each
// project's store is separate; each subtest gets a new, empty one.
func RunProjects(t *testing.T, open func(t *testing.T) store.Projects) {
	ctx := context.Background()
	t.Run("projects", func(t *testing.T) {
		r := open(t)
		if err := r.PutProject(ctx, &store.Project{ID: "b", Name: "B", GitHub: "o/b"}); err != nil {
			t.Fatal(err)
		}
		if err := r.PutProject(ctx, &store.Project{ID: "a", Name: "A", Path: "/src/a", Base: "main"}); err != nil {
			t.Fatal(err)
		}
		if err := r.PutProject(ctx, &store.Project{ID: "a", Name: "A2", Path: "/src/a", Base: "dev"}); err != nil {
			t.Fatal(err)
		}
		list, err := r.Projects(ctx)
		if err != nil || len(list) != 2 || list[0].ID != "a" || list[0].Base != "dev" || list[1].GitHub != "o/b" {
			t.Errorf("projects = %+v, %v", list, err)
		}
		if _, err := r.Project(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("unknown: err = %v", err)
		}
		if err := r.Store("a").CreateTasks(ctx, []store.Task{{ID: "t", Agent: "x", Status: "open"}}); err != nil {
			t.Fatal(err)
		}
		if got, _ := r.Store("b").Tasks(ctx, store.TaskFilter{}); len(got) != 0 {
			t.Errorf("project b sees a's tasks: %+v", got)
		}
		if got, _ := r.Store("a").Tasks(ctx, store.TaskFilter{}); len(got) != 1 {
			t.Errorf("project a's tasks = %+v", got)
		}
		secret, tok, err := store.NewToken("a", "residents", "Me <me@example.com>")
		if err != nil || !strings.HasPrefix(secret, "fausto_") || !strings.HasPrefix(secret, tok.Label) || tok.Hash == secret {
			t.Fatalf("token %q = %+v, %v", secret, tok, err)
		}
		if err := r.CreateToken(ctx, tok); err != nil {
			t.Fatal(err)
		}
		_, other, _ := store.NewToken("b", "gateway", "")
		if err := r.CreateToken(ctx, other); err != nil {
			t.Fatal(err)
		}
		if got, err := r.TokenByHash(ctx, store.HashToken(secret)); err != nil || got.Agent != "residents" || got.Project != "a" {
			t.Errorf("token by hash = %+v, %v", got, err)
		}
		if _, err := r.TokenByHash(ctx, store.HashToken("fausto_nope")); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("unknown token: err = %v", err)
		}
		if got, _ := r.Tokens(ctx, "a"); len(got) != 1 || got[0].Label != tok.Label {
			t.Errorf("tokens of a = %+v", got)
		}
		if got, _ := r.Tokens(ctx, ""); len(got) != 2 {
			t.Errorf("all tokens = %+v", got)
		}
		if err := r.DeleteToken(ctx, tok.Label); err != nil {
			t.Fatal(err)
		}
		if _, err := r.TokenByHash(ctx, tok.Hash); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("revoked token: err = %v", err)
		}
		if err := r.DeleteProject(ctx, "b"); err != nil {
			t.Fatal(err)
		}
		if err := r.DeleteProject(ctx, "b"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("delete twice: err = %v", err)
		}
	})
}
