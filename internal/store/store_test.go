package store

import (
	"strings"
	"testing"
)

func TestMarkdownRoundTrip(t *testing.T) {
	task := &Task{ID: "2026-10-06-x--platform", Agent: "platform", Proposal: "2026-10-06-x", Status: "open", Created: "2026-10-06",
		Title: "X", Components: []string{"api", "web"}, Jira: "FAUS-1", Body: "Do x.\n"}
	md := string(task.Markdown(true))
	want := "---\nid: 2026-10-06-x--platform\nagent: platform\nproposal: 2026-10-06-x\nstatus: open\ncreated: 2026-10-06\n" +
		"title: X\ncomponents: [api, web]\njira: FAUS-1\n---\n\nDo x.\n"
	if md != want {
		t.Errorf("got:\n%s\nwant:\n%s", md, want)
	}
	if strings.Contains(string(task.Markdown(false)), "status:") {
		t.Error("Markdown(false) has a status")
	}
	back, err := ParseTask([]byte(md))
	if err != nil {
		t.Fatal(err)
	}
	if back.Created != "2026-10-06" || back.Status != "open" || back.Body != "Do x.\n" || back.Jira != "FAUS-1" || len(back.Components) != 2 {
		t.Errorf("parsed = %+v", back)
	}
	if _, err := ParseTask([]byte("no front matter")); err == nil {
		t.Error("parsed a file without front matter")
	}
}
