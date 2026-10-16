package proposal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

var ctx = context.Background()

const repoRoot = "../../testdata/example"

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=*", "-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo copies the current Habyta files into a fresh git repository on
// branch main and returns a store for it, with an empty database. Files
// from before the database (tasks/, system/proposals/) are left out.
func newRepo(t *testing.T) *Store {
	t.Helper()
	files, err := check.ListFiles(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, f := range files {
		if strings.HasPrefix(f, legacyRecordsDir+"/") || (strings.HasPrefix(f, legacyTasksDir+"/") && f != legacyTasksDir+"/README.md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoRoot, f))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, root, "init", "-q", "-b", "main")
	run(t, root, "add", "-A")
	run(t, root, "commit", "-q", "-m", "initial")
	return &Store{
		Root: root, Base: "main", Author: "Test <test@example.com>", DB: memstore.New(),
		Now: func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) },
	}
}

// failing returns the checks that fail in report.
func failing(report *check.Report) map[string]bool {
	out := map[string]bool{}
	for _, r := range report.Results {
		if r.Status == check.Fail {
			out[r.Check] = true
		}
	}
	return out
}

// assertNoNewFailures fails the test if report fails a check that passes in
// the copied repository; work in progress there may fail some already.
func assertNoNewFailures(t *testing.T, s *Store, report *check.Report, when string) {
	t.Helper()
	base, err := check.Run(repoRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := failing(base)
	for name := range failing(report) {
		if !before[name] {
			t.Errorf("%s: check %s fails: %+v", when, name, report)
		}
	}
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func currentSpec(t *testing.T, s *Store) *spec.Spec {
	t.Helper()
	sp, err := spec.Load(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func plan(d *Detail, agent string) *TaskPlan {
	for i := range d.Plans {
		if d.Plans[i].Agent == agent {
			return &d.Plans[i]
		}
	}
	return nil
}

func TestCreateApproveCreatesTasks(t *testing.T) {
	s := newRepo(t)
	sp := currentSpec(t, s)
	principle := sp.Principles.Principles[0]
	principle.Rationale += " No shared schemas."

	d, err := s.Create(ctx, "Plan billing service", "Billing will own invoices.", "", []edit.Change{
		{Kind: "component", Value: json.RawMessage(`{"id":"billing","name":"Billing service","kind":"service",
			"layer":"domain","status":"planned","owner":"platform","tech":["go","grpc"]}`)},
		{Kind: "principle", ID: principle.ID, Value: raw(t, principle)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "2026-10-06-plan-billing-service" || d.Status != "open" || d.Author != "Test <test@example.com>" {
		t.Errorf("proposal = %+v", d.Proposal)
	}
	if status := run(t, s.Root, "status", "--porcelain"); status != "" {
		t.Errorf("work tree changed by Create:\n%s", status)
	}
	if got := len(d.Changes); got != 2 || d.Changes[0].String() != "component `billing` (Billing service) added" {
		t.Errorf("changes = %+v", d.Changes)
	}
	if d.Conflict != "" {
		t.Errorf("conflict %q", d.Conflict)
	}
	assertNoNewFailures(t, s, d.Report, "merged proposal")
	// Drift already in the copied repository may add components of its own.
	p := plan(d, "platform")
	if p == nil || len(p.SpecChanges) != 2 || !slices.Contains(p.Components, "billing") {
		t.Errorf("platform plan = %+v", p)
	}
	if r := plan(d, "residents"); r == nil || len(r.SpecChanges) != 1 {
		t.Errorf("residents plan = %+v (principle changes reach every agent)", r)
	}
	if o := plan(d, "orchestrator"); o == nil || len(o.SpecChanges) != 1 {
		t.Errorf("orchestrator plan = %+v (principle changes reach every charter, agent or human)", o)
	}

	if run(t, s.Root, "rev-parse", "HEAD") != d.BaseCommit {
		t.Error("Create made a commit")
	}

	accepted, err := s.Approve(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "accepted" || len(accepted.Tasks) != len(d.Plans) || accepted.Accepted != "2026-10-06" {
		t.Errorf("accepted = %+v", accepted)
	}
	// One commit on top of the base, touching only the spec (and the agent
	// definitions generated from it).
	if accepted.Commit != run(t, s.Root, "rev-parse", "HEAD") || run(t, s.Root, "rev-parse", "HEAD^") != d.BaseCommit {
		t.Errorf("commit %s is not one commit on top of %s", accepted.Commit, d.BaseCommit)
	}
	for _, f := range strings.Fields(run(t, s.Root, "diff", "--name-only", "HEAD^", "HEAD")) {
		if !strings.HasPrefix(f, "system/") && !strings.HasPrefix(f, ".claude/agents/") {
			t.Errorf("accept commit touches %s", f)
		}
	}
	task, err := s.DB.Task(ctx, d.ID+"--platform")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Body, "component `billing` (Billing service) added") || task.Status != "open" || task.Created != "2026-10-06" ||
		task.Proposal != d.ID || !strings.Contains(task.Body, "fausto task done "+task.ID) {
		t.Errorf("task = %+v", task)
	}
	if tasks, _ := s.DB.Tasks(ctx, store.TaskFilter{}); len(tasks) != len(d.Plans) {
		t.Errorf("tasks = %d, plans = %d", len(tasks), len(d.Plans))
	}
	if currentSpec(t, s).Component("billing") == nil {
		t.Error("billing not in the spec after approval")
	}
	report, err := check.Run(s.Root, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNoNewFailures(t, s, report, "after approval")
	if status := run(t, s.Root, "status", "--porcelain"); status != "" {
		t.Errorf("work tree not clean after approval:\n%s", status)
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Status != "accepted" {
		t.Errorf("list = %+v, %v", list, err)
	}
	// The accepted proposal's review shows what its commit changed.
	if got, err := s.Detail(ctx, d.ID); err != nil || len(got.Changes) != 2 || !strings.Contains(got.Diff, "billing") {
		t.Errorf("accepted detail = %+v, %v", got, err)
	}
	var ce *ConflictError
	if _, err := s.Approve(ctx, d.ID); !errors.As(err, &ce) {
		t.Errorf("approve twice: err = %v", err)
	}
}

func TestDriftBecomesTaskFindings(t *testing.T) {
	s := newRepo(t)
	d, err := s.Create(ctx, "Add billing", "", "", []edit.Change{
		{Kind: "component", Value: json.RawMessage(`{"id":"billing","name":"Billing","kind":"service","layer":"domain",
			"status":"active","owner":"platform","runtime":{"cmd":"backend/cmd/billing","compose":"billing"}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Report.OK {
		t.Fatal("want failing checks for an undeclared-in-code service")
	}
	p := plan(d, "platform")
	if p == nil || len(p.Findings) == 0 || !strings.Contains(strings.Join(p.Findings, "\n"), "backend/cmd/billing") {
		t.Errorf("platform plan = %+v", p)
	}
}

// Deleting a component must not leave its (now gone) id in the task that
// announces the deletion: the merged spec no longer has it, so a task
// listing it as a Components entry would fail the schema check.
func TestDeleteComponentOmitsItFromTask(t *testing.T) {
	s := newRepo(t)
	add, err := s.Create(ctx, "Add throwaway service", "", "", []edit.Change{
		{Kind: "component", Value: json.RawMessage(`{"id":"throwaway","name":"Throwaway","kind":"service",
			"layer":"domain","status":"planned","owner":"platform"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, add.ID); err != nil {
		t.Fatalf("approve add: %v", err)
	}

	d, err := s.Create(ctx, "Delete throwaway", "Not needed anymore.", "", []edit.Change{
		{Kind: "component", ID: "throwaway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := plan(d, "platform"); p == nil || slices.Contains(p.Components, "throwaway") {
		t.Errorf("platform plan = %+v (must not list the removed component)", p)
	}
	if _, err := s.Approve(ctx, d.ID); err != nil {
		t.Fatalf("approve delete: %v", err)
	}
	if after := currentSpec(t, s); after.Component("throwaway") != nil {
		t.Error("throwaway still present after deletion")
	}
}

func TestReject(t *testing.T) {
	s := newRepo(t)
	d, err := s.Create(ctx, "Hold React", "", "", []edit.Change{
		{Kind: "technology", ID: "react", Value: json.RawMessage(`{"id":"react","name":"React","category":"framework","status":"hold"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan(d, "gateway") == nil {
		t.Errorf("gateway (owner of web, which uses react) gets no task: %+v", d.Plans)
	}
	if _, err := s.Reject(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	var ce *ConflictError
	if _, err := s.Approve(ctx, d.ID); !errors.As(err, &ce) {
		t.Errorf("approve rejected: err = %v", err)
	}
	if _, err := s.Reject(ctx, d.ID); !errors.As(err, &ce) {
		t.Errorf("reject twice: err = %v", err)
	}
	if _, err := s.Reject(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reject unknown: err = %v", err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Status != "rejected" {
		t.Errorf("list = %+v", list)
	}
	if got, err := s.Detail(ctx, d.ID); err != nil || got.Conflict != "This proposal was rejected." || got.Diff == "" {
		t.Errorf("rejected detail = %+v, %v (still reviewable)", got, err)
	}
}

// Proposals made on the same day list newest first, not by id.
func TestListOrdersSameDayByTime(t *testing.T) {
	s := newRepo(t)
	clock := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	s.DB.(*memstore.Store).Now = func() time.Time { return clock }
	propose := func(title, status string) string {
		d, err := s.Create(ctx, title, "", "", []edit.Change{
			{Kind: "technology", ID: "react", Value: json.RawMessage(`{"id":"react","name":"React","category":"framework","status":"` + status + `"}`)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	older := propose("Zebra", "hold")
	clock = clock.Add(time.Hour)
	newer := propose("Alpha", "assess")
	clock = clock.Add(time.Hour)
	if _, err := s.Approve(ctx, older); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	// The accepted one changed last, so it comes first.
	if list[0].ID != older || list[1].ID != newer {
		t.Errorf("order = %s, %s", list[0].ID, list[1].ID)
	}
}

func TestConflictingProposals(t *testing.T) {
	s := newRepo(t)
	web := *currentSpec(t, s).Component("web")
	propose := func(title, status string) *Detail {
		w := web
		w.Status = status
		d, err := s.Create(ctx, title, "", "", []edit.Change{{Kind: "component", ID: "web", Value: raw(t, w)}})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	a := propose("Activate web", "active")
	b := propose("Deprecate web", "deprecated")
	if _, err := s.Approve(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	d, err := s.Detail(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Conflict, "component `web` changed on main") {
		t.Errorf("conflict = %q", d.Conflict)
	}
	var ce *ConflictError
	if _, err := s.Approve(ctx, b.ID); !errors.As(err, &ce) {
		t.Errorf("approve conflicting: err = %v", err)
	}
}

func TestNonConflictingProposalAfterBaseMoved(t *testing.T) {
	s := newRepo(t)
	a, err := s.Create(ctx, "Assess NATS", "", "", []edit.Change{{Kind: "technology", Value: json.RawMessage(`{"id":"nats","name":"NATS","category":"infrastructure","status":"assess"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "Define lease", "", "", []edit.Change{{Kind: "term", Value: json.RawMessage(`{"term":"Lease","definition":"A resident's contract for a unit."}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, b.ID); err != nil {
		t.Fatalf("approve after base moved: %v", err)
	}
	sp := currentSpec(t, s)
	if len(sp.Domains.Glossary) != 4 || sp.Tech.Technologies[len(sp.Tech.Technologies)-1].ID != "nats" {
		t.Errorf("both proposals not applied")
	}
}

func TestMilestoneProposal(t *testing.T) {
	s := newRepo(t)
	m := *currentSpec(t, s).Milestone("public-api-v1")
	m.ExitCriteria = append(m.ExitCriteria, spec.Criterion{Text: "Rate limits per tenant"})
	m.Target = "2026-Q4"
	d, err := s.Create(ctx, "Scope public API", "", "public-api-v1", []edit.Change{{Kind: "milestone", ID: m.ID, Value: raw(t, m)}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Milestone != "public-api-v1" || len(d.Changes) != 1 || d.Changes[0].Kind != "milestone" {
		t.Errorf("detail = %+v", d.Proposal)
	}
	// The milestone's only component is api, owned by the gateway agent.
	// Other charters may also get plans here, for failing checks already
	// present in the copied repository (e.g. a human-owned component with
	// drift) — unrelated to this milestone change.
	if g := plan(d, "gateway"); g == nil || len(g.SpecChanges) != 1 {
		t.Fatalf("gateway plan = %+v (plans = %+v)", g, d.Plans)
	}
	if _, err := s.Approve(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	after := currentSpec(t, s)
	tasks, _ := s.DB.Tasks(ctx, store.TaskFilter{})
	if len(tasks) != len(d.Plans) {
		t.Fatalf("tasks = %+v, plans = %+v", tasks, d.Plans)
	}
	for _, task := range tasks {
		if task.Milestone != "public-api-v1" {
			t.Errorf("task %s: milestone = %q", task.ID, task.Milestone)
		}
	}
	if got := after.Milestone("public-api-v1"); got.Target != "2026-Q4" || len(got.ExitCriteria) != 4 {
		t.Errorf("milestone = %+v", got)
	}
}

func TestProposalForUnknownMilestone(t *testing.T) {
	s := newRepo(t)
	_, err := s.Create(ctx, "Nope", "", "no-such-milestone", []edit.Change{
		{Kind: "term", Value: json.RawMessage(`{"term":"Fee","definition":"A recurring charge."}`)},
	})
	var ie *InvalidError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "unknown milestone") {
		t.Errorf("err = %v", err)
	}
}

func TestRuntimeFindingsGoToComposeOwner(t *testing.T) {
	s := newRepo(t)
	// Require hardening on databases too; residents-db (owned by the
	// residents agent) is not hardened, and the fix is in compose.yaml,
	// which the platform agent owns.
	r := currentSpec(t, s).Requirements.Requirements[2]
	if r.ID != "least-privilege" {
		t.Fatalf("seed order changed: %s", r.ID)
	}
	r.AppliesTo.Kinds = append(r.AppliesTo.Kinds, "database")
	d, err := s.Create(ctx, "Harden databases", "", "", []edit.Change{{Kind: "requirement", ID: r.ID, Value: raw(t, r)}})
	if err != nil {
		t.Fatal(err)
	}
	p := plan(d, "platform")
	if p == nil || !strings.Contains(strings.Join(p.Findings, "\n"), "residents-db") {
		t.Errorf("platform plan = %+v", p)
	}
	if res := plan(d, "residents"); res == nil || len(res.Findings) != 0 || len(res.SpecChanges) != 1 {
		t.Errorf("residents plan = %+v (spec change only)", res)
	}
}

func TestRiskProposal(t *testing.T) {
	s := newRepo(t)
	var r spec.Risk
	for _, x := range currentSpec(t, s).Risks.Risks {
		if x.ID == "no-backups" {
			r = x
		}
	}
	r.Status = "mitigating"
	d, err := s.Create(ctx, "Start backups", "", "", []edit.Change{{Kind: "risk", ID: r.ID, Value: raw(t, r)}})
	if err != nil {
		t.Fatal(err)
	}
	// residents-db is owned by the residents agent; the risk by platform.
	for _, agent := range []string{"residents", "platform"} {
		if p := plan(d, agent); p == nil || len(p.SpecChanges) != 1 {
			t.Errorf("%s plan = %+v", agent, p)
		}
	}
	r.Mitigation = ""
	r.Status = "open"
	if _, err := s.Create(ctx, "Drop mitigation", "", "", []edit.Change{{Kind: "risk", ID: r.ID, Value: raw(t, r)}}); err == nil || !strings.Contains(err.Error(), "needs a mitigation") {
		t.Errorf("err = %v", err)
	}
}

func TestEventProposal(t *testing.T) {
	s := newRepo(t)
	e := currentSpec(t, s).Events.Events[0]
	e.Consumers = []string{"api"}
	d, err := s.Create(ctx, "API listens for registrations", "", "", []edit.Change{{Kind: "event", ID: e.ID, Value: raw(t, e)}})
	if err != nil {
		t.Fatal(err)
	}
	// Producer residents (residents agent) and consumer api (gateway agent).
	for _, agent := range []string{"residents", "gateway"} {
		if p := plan(d, agent); p == nil || len(p.SpecChanges) != 1 {
			t.Errorf("%s plan = %+v", agent, p)
		}
	}
}

func TestInvalidProposal(t *testing.T) {
	s := newRepo(t)
	web := *currentSpec(t, s).Component("web")
	web.Owner = "nobody"
	_, err := s.Create(ctx, "Orphan web", "", "", []edit.Change{{Kind: "component", ID: "web", Value: raw(t, web)}})
	var ie *InvalidError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), `owner "nobody"`) {
		t.Errorf("err = %v", err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Errorf("invalid proposal was recorded: %+v", list)
	}
}

// A proposal that affects no agent (an empty layer, say) still lists its
// plans as an empty list: the UI reads them as an array.
func TestPlanTasksWithoutAgentsIsEmptyNotNil(t *testing.T) {
	plans, unassigned := PlanTasks("p", []ItemChange{{Kind: "layer", ID: "staff", Action: "added"}}, nil, nil, nil)
	if plans == nil || unassigned == nil {
		t.Fatalf("plans = %#v, unassigned = %#v; want empty, non-nil slices", plans, unassigned)
	}
	b, _ := json.Marshal(map[string]any{"plans": plans, "unassigned": unassigned})
	if string(b) != `{"plans":[],"unassigned":[]}` {
		t.Errorf("JSON = %s", b)
	}
}

// A proposal that adds an agent carries its Claude Code definition, so
// `claude --agent <id>` works as soon as it is accepted.
func TestProposalGeneratesAgentDefinition(t *testing.T) {
	s := newRepo(t)
	d, err := s.Create(ctx, "Add billing agent", "", "", []edit.Change{
		{Kind: "agent", Value: json.RawMessage(`{"id":"billing","name":"Billing agent","kind":"agent",
			"scope":"Billing service.","owns":["backend/internal/billing/"]}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(d.Files, ".claude/agents/billing.md") {
		t.Fatalf("files = %v, want the generated definition", d.Files)
	}
	if _, err := s.Approve(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Root, ".claude", "agents", "billing.md"))
	if err != nil || !strings.Contains(string(data), "You are the **Billing agent**") {
		t.Errorf("definition after approval: %v\n%s", err, data)
	}
}
