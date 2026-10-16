package proposal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMigrate(t *testing.T) {
	s := newRepo(t)
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(s.Root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("system/proposals/2026-10-01-add-x.yaml", "id: 2026-10-01-add-x\ntitle: Add x\nauthor: A <a@example.com>\ncreated: 2026-10-01\n"+
		"status: accepted\naccepted: 2026-10-01\ntasks:\n  - 2026-10-01-add-x--platform\n")
	write("tasks/2026-10-01-add-x--platform.md", "---\nid: 2026-10-01-add-x--platform\nagent: platform\nproposal: 2026-10-01-add-x\n"+
		"status: done\ncreated: 2026-10-01\ntitle: Add x\njira: FAUS-3\n---\n\nDo x.\n")
	run(t, s.Root, "add", "-A")
	run(t, s.Root, "commit", "-q", "-m", "Accept proposal: Add x\n\nProposal: 2026-10-01-add-x")
	accepted := run(t, s.Root, "rev-parse", "HEAD")

	dry, err := s.Migrate(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Proposals) != 1 || len(dry.Tasks) != 1 || len(dry.Removed) != 2 || dry.Commit != "" {
		t.Errorf("dry run = %+v", dry)
	}
	if list, _ := s.List(ctx); len(list) != 0 || run(t, s.Root, "rev-parse", "HEAD") != accepted {
		t.Fatal("dry run changed something")
	}

	m, err := s.Migrate(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Commit == "" || run(t, s.Root, "rev-parse", "HEAD") != m.Commit {
		t.Fatalf("migration = %+v", m)
	}
	files := strings.Fields(run(t, s.Root, "ls-files", "tasks", "system/proposals"))
	if !slices.Equal(files, []string{"tasks/README.md"}) {
		t.Errorf("left in git: %v", files)
	}
	if status := run(t, s.Root, "status", "--porcelain"); status != "" {
		t.Errorf("work tree not clean:\n%s", status)
	}
	task, err := s.DB.Task(ctx, "2026-10-01-add-x--platform")
	if err != nil || task.Status != "done" || task.Jira != "FAUS-3" || task.Body != "Do x.\n" {
		t.Errorf("task = %+v, %v", task, err)
	}
	p, err := s.DB.Proposal(ctx, "2026-10-01-add-x")
	if err != nil || !p.Legacy || p.Status != "accepted" || p.Commit != accepted {
		t.Errorf("proposal = %+v, %v", p, err)
	}
	if d, err := s.Detail(ctx, p.ID); err != nil || !strings.Contains(d.Diff, "tasks/2026-10-01-add-x--platform.md") {
		t.Errorf("legacy detail = %+v, %v (shows its accepting commit)", d, err)
	}

	// Running it again finds nothing left to do.
	again, err := s.Migrate(ctx, false)
	if err != nil || again.Commit != "" || len(again.Removed) != 0 {
		t.Errorf("second run = %+v, %v", again, err)
	}
}
