package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

const token = "s3cret-token"

// fakeJira is an in-memory Jira project with a To Do → In Progress → Done
// workflow (any status reachable from any other).
type fakeJira struct {
	mu       sync.Mutex
	issues   map[string]*fakeIssue
	next     int
	created  int
	failWith int // when set, every request fails with this status
}

type fakeIssue struct {
	status      string
	labels      []string
	fields      map[string]any
	assignee    string
	attachments map[string][]byte
}

func newFake(t *testing.T) (*fakeJira, *Client) {
	f := &fakeJira{issues: map[string]*fakeIssue{}, next: 1}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := NewClient(Config{URL: srv.URL, Email: "me@example.com", Token: token, Project: "HAB", IssueType: "Task", Statuses: mustStatuses(t, DefaultStatuses)})
	return f, c
}

func mustStatuses(t *testing.T, s string) map[string]string {
	t.Helper()
	m, err := ParseStatuses(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fakeJira) add(status string, labels ...string) string {
	key := fmt.Sprintf("HAB-%d", f.next)
	f.next++
	f.issues[key] = &fakeIssue{status: status, labels: labels, attachments: map[string][]byte{}}
	return key
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != "me@example.com" || p != token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.failWith != 0 {
		w.WriteHeader(f.failWith)
		io.WriteString(w, `{"errorMessages":["Jira is down"]}`)
		return
	}
	if key, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"), "/attachments"); ok && r.Method == http.MethodPost {
		is := f.issues[key]
		file, hdr, err := r.FormFile("file")
		if is == nil || err != nil || r.Header.Get("X-Atlassian-Token") != "no-check" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(file)
		is.attachments[hdr.Filename] = data
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `[]`)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	status := func(s string) map[string]any { return map[string]any{"status": map[string]string{"name": s}} }
	path := strings.TrimPrefix(r.URL.Path, "/rest/api/3/")
	switch {
	case r.Method == http.MethodPost && path == "search/jql":
		jql := body["jql"].(string)
		issues := []any{}
		if strings.Contains(jql, `labels = "fausto"`) {
			for key, is := range f.issues {
				if !slices.Contains(is.labels, "fausto") {
					continue
				}
				fields := map[string]any{"status": map[string]string{"name": is.status}, "labels": is.labels, "assignee": nil}
				if is.assignee != "" {
					fields["assignee"] = map[string]any{"displayName": is.assignee, "avatarUrls": map[string]string{"24x24": "https://avatar/" + key}}
				}
				issues = append(issues, map[string]any{"key": key, "fields": fields})
			}
			reply(map[string]any{"issues": issues})
			return
		}
		for key, is := range f.issues {
			for _, l := range is.labels {
				if strings.Contains(jql, fmt.Sprintf("labels = %q", l)) && strings.HasPrefix(l, "fausto-task-") {
					issues = append(issues, map[string]any{"key": key, "fields": status(is.status)})
				}
			}
		}
		reply(map[string]any{"issues": issues})
	case r.Method == http.MethodPost && path == "issue":
		fields := body["fields"].(map[string]any)
		var labels []string
		for _, l := range fields["labels"].([]any) {
			labels = append(labels, l.(string))
		}
		key := f.add("To Do", labels...)
		f.issues[key].fields = fields
		f.created++
		w.WriteHeader(http.StatusCreated)
		reply(map[string]string{"key": key})
	case strings.HasSuffix(path, "/transitions"):
		is := f.issues[strings.TrimSuffix(strings.TrimPrefix(path, "issue/"), "/transitions")]
		if is == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		names := map[string]string{"11": "To Do", "21": "In Progress", "31": "Done"}
		if r.Method == http.MethodGet {
			var ts []any
			for id, name := range names {
				ts = append(ts, map[string]any{"id": id, "to": map[string]string{"name": name}})
			}
			reply(map[string]any{"transitions": ts})
			return
		}
		is.status = names[body["transition"].(map[string]any)["id"].(string)]
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "issue/"):
		is := f.issues[strings.TrimPrefix(path, "issue/")]
		if is == nil {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"errorMessages":["Issue does not exist"]}`)
			return
		}
		fields := status(is.status)
		var att []any
		for name := range is.attachments {
			att = append(att, map[string]string{"filename": name})
		}
		fields["attachment"] = att
		reply(map[string]any{"fields": fields})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func task(id, status, key string) store.Task {
	return store.Task{ID: id, Agent: "platform", Status: status, Created: "2026-10-01", Title: "Do " + id, Jira: key, Body: "## Spec changes\n\n- one\n- two\n"}
}

func TestSyncCreatesLinksAndTransitions(t *testing.T) {
	f, c := newFake(t)
	existing := f.add("To Do", "fausto", Label("found"))
	linked := f.add("To Do")

	links, rep := Sync(context.Background(), c, []store.Task{
		task("new", "open", ""),
		task("found", "done", ""),
		task("linked", "in-progress", linked),
		task("same", "open", ""),
	}, false)
	if rep.Failed() != 0 {
		t.Fatalf("report = %+v", rep)
	}
	actions := map[string][]string{}
	for _, r := range rep.Results {
		actions[r.Task] = r.Actions
	}
	if !slices.Equal(actions["new"], []string{Created, Attached}) || !slices.Equal(actions["found"], []string{Linked, Attached, Transitioned}) ||
		!slices.Equal(actions["linked"], []string{Attached, Transitioned}) {
		t.Errorf("actions = %v", actions)
	}
	if links["found"] != existing || links["new"] == "" || links["linked"] != "" {
		t.Errorf("links = %v (only tasks without a recorded key)", links)
	}
	if f.issues[existing].status != "Done" || f.issues[linked].status != "In Progress" {
		t.Errorf("statuses: %s, %s", f.issues[existing].status, f.issues[linked].status)
	}
	is := f.issues[links["new"]]
	if is.fields["summary"] != "[platform] Do new" || !slices.Contains(is.labels, Label("new")) || !slices.Contains(is.labels, "platform") {
		t.Errorf("created issue = %+v", is)
	}
	md := string(is.attachments["new.md"])
	if !strings.Contains(md, "id: new\n") || !strings.Contains(md, "jira: "+links["new"]+"\n") || strings.Contains(md, "status:") ||
		!strings.HasSuffix(md, "- two\n") {
		t.Errorf("attachment:\n%s", md)
	}

	// A second sync finds everything in line and creates nothing.
	tasks := []store.Task{task("new", "open", links["new"]), task("found", "done", links["found"]), task("linked", "in-progress", linked)}
	created := f.created
	_, rep = Sync(context.Background(), c, tasks, false)
	for _, r := range rep.Results {
		if !slices.Equal(r.Actions, []string{OK}) {
			t.Errorf("resync %s: %v", r.Task, r.Actions)
		}
	}
	if f.created != created {
		t.Error("resync created issues")
	}
}

func TestSyncFindsIssueFromFailedRun(t *testing.T) {
	// A run created the issue but couldn't record its key: the next run must
	// link it, not create a second one.
	f, c := newFake(t)
	links, _ := Sync(context.Background(), c, []store.Task{task("a", "open", "")}, false)
	links2, rep := Sync(context.Background(), c, []store.Task{task("a", "open", "")}, false)
	if f.created != 1 || links2["a"] != links["a"] || !slices.Equal(rep.Results[0].Actions, []string{Linked}) {
		t.Errorf("created %d, links %v then %v, report %+v", f.created, links, links2, rep)
	}
}

func TestSyncDryRunWritesNothing(t *testing.T) {
	f, c := newFake(t)
	key := f.add("To Do")
	links, rep := Sync(context.Background(), c, []store.Task{task("a", "open", ""), task("b", "done", key)}, true)
	if len(links) != 0 || f.created != 0 || f.issues[key].status != "To Do" {
		t.Errorf("dry run wrote: links %v, created %d, status %s", links, f.created, f.issues[key].status)
	}
	if !slices.Equal(rep.Results[0].Actions, []string{WouldCreate, WouldAttach}) || !slices.Equal(rep.Results[1].Actions, []string{WouldAttach, WouldTransition}) ||
		len(f.issues[key].attachments) != 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestSyncReportsErrorsPerTask(t *testing.T) {
	f, c := newFake(t)
	c.Statuses = mustStatuses(t, "open=To Do,done=Closed")
	key := f.add("To Do")
	_, rep := Sync(context.Background(), c, []store.Task{
		task("unmapped", "dropped", ""),
		task("nowhere", "done", key),
		task("fine", "open", ""),
	}, false)
	if rep.Failed() != 2 || rep.Results[2].Error != "" || f.created != 1 {
		t.Errorf("report = %+v", rep)
	}
	if !strings.Contains(rep.Results[1].Error, `no transition to "Closed"`) {
		t.Errorf("error = %q", rep.Results[1].Error)
	}

	f.failWith = http.StatusInternalServerError
	_, rep = Sync(context.Background(), c, []store.Task{task("down", "open", "")}, false)
	if msg := rep.Results[0].Error; !strings.Contains(msg, "Jira is down") || strings.Contains(msg, token) {
		t.Errorf("error = %q", msg)
	}
}

func TestADF(t *testing.T) {
	doc := ADF("Proposal **x** was accepted.\nSecond line.\n\n## Spec changes\n\n- one\n- two\n", "fausto task t")
	b, _ := json.Marshal(doc)
	want := `{"content":[` +
		`{"content":[{"text":"Proposal **x** was accepted. Second line.","type":"text"}],"type":"paragraph"},` +
		`{"attrs":{"level":2},"content":[{"text":"Spec changes","type":"text"}],"type":"heading"},` +
		`{"content":[{"content":[{"content":[{"text":"one","type":"text"}],"type":"paragraph"}],"type":"listItem"},` +
		`{"content":[{"content":[{"text":"two","type":"text"}],"type":"paragraph"}],"type":"listItem"}],"type":"bulletList"},` +
		`{"content":[{"text":"fausto task t","type":"text"}],"type":"paragraph"}` +
		`],"type":"doc","version":1}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestConfigFromEnv(t *testing.T) {
	for _, k := range []string{"URL", "EMAIL", "TOKEN", "PROJECT", "ISSUE_TYPE", "STATUSES"} {
		t.Setenv("FAUSTO_JIRA_"+k, "")
	}
	if c, err := ConfigFromEnv(); c != nil || err != nil {
		t.Errorf("unset: %v, %v", c, err)
	}
	t.Setenv("FAUSTO_JIRA_URL", "https://x.atlassian.net/")
	if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "FAUSTO_JIRA_TOKEN") {
		t.Errorf("incomplete: %v", err)
	}
	t.Setenv("FAUSTO_JIRA_EMAIL", "me@example.com")
	t.Setenv("FAUSTO_JIRA_TOKEN", token)
	t.Setenv("FAUSTO_JIRA_PROJECT", "HAB")
	c, err := ConfigFromEnv()
	if err != nil || c.URL != "https://x.atlassian.net" || c.IssueType != "Task" || c.Statuses["in-progress"] != "In Progress" {
		t.Errorf("config = %+v, %v", c, err)
	}
}

func TestSyncStoreRecordsKeys(t *testing.T) {
	f, c := newFake(t)
	db := memstore.New()
	ctx := context.Background()
	if err := db.CreateTasks(ctx, []store.Task{task("a", "open", ""), task("b", "done", "")}); err != nil {
		t.Fatal(err)
	}
	rep, err := SyncStore(ctx, c, db, []string{"a"}, false)
	if err != nil || len(rep.Results) != 1 || f.created != 1 {
		t.Fatalf("report %+v, err %v, created %d", rep, err, f.created)
	}
	a, _ := db.Task(ctx, "a")
	b, _ := db.Task(ctx, "b")
	if a.Jira != rep.Results[0].Key || b.Jira != "" {
		t.Errorf("keys: a %q, b %q", a.Jira, b.Jira)
	}
	// A dry run records nothing.
	if _, err := SyncStore(ctx, c, db, nil, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := db.Task(ctx, "b"); b.Jira != "" || f.created != 1 {
		t.Errorf("dry run wrote: b %q, created %d", b.Jira, f.created)
	}
}
