package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/server"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

const example = "../../testdata/example"

func TestServe(t *testing.T) {
	ctx := context.Background()
	reg := memstore.NewProjects()
	db := reg.Store("example")
	db.CreateTasks(ctx, []store.Task{{ID: "t--platform", Agent: "platform", Status: "open", Created: "2026-10-06", Title: "Build", Body: "Build the thing.\n"}})
	secret, tok, _ := store.NewToken("example", "platform", "")
	reg.CreateToken(ctx, tok)
	ps := &proposal.Store{Src: &source.Local{Path: example, Branch: "main"}, DB: db}
	api := httptest.NewServer(server.Multi([]server.Project{{ID: "example",
		Handler: server.New(server.Options{Project: "example", Tokens: reg, Root: example, Proposals: ps})}}, reg))
	defer api.Close()

	s := &Server{Root: example, Client: &client.Client{URL: api.URL, Token: secret}, Version: "test"}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fausto_tasks","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fausto_task_start","arguments":{"id":"t--platform"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fausto_check","arguments":{"checks":["schema"]}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"fausto_task_done","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"nope"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := s.Serve(ctx, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("%d responses (want 7, none for the notification):\n%s", len(lines), out.String())
	}
	type res struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion string            `json:"protocolVersion"`
			Tools           []json.RawMessage `json:"tools"`
			Content         []struct{ Text string }
			IsError         bool `json:"isError"`
		} `json:"result"`
		Error *struct{ Code int } `json:"error"`
	}
	got := make([]res, len(lines))
	for i, l := range lines {
		if err := json.Unmarshal([]byte(l), &got[i]); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
	}
	text := func(i int) string {
		if len(got[i].Result.Content) == 0 {
			return ""
		}
		return got[i].Result.Content[0].Text
	}
	if got[0].Result.ProtocolVersion != "2025-06-18" {
		t.Errorf("initialize: %s", lines[0])
	}
	if len(got[1].Result.Tools) != 5 {
		t.Errorf("tools: %s", lines[1])
	}
	if !strings.Contains(text(2), "Build the thing.") || !strings.Contains(text(2), "id: t--platform") {
		t.Errorf("tasks: %q", text(2))
	}
	if text(3) != "t--platform: open → in-progress" {
		t.Errorf("start: %q", text(3))
	}
	if !strings.Contains(text(4), "PASS     schema") {
		t.Errorf("check: %q", text(4))
	}
	if !got[5].Result.IsError {
		t.Errorf("done without an id: %s", lines[5])
	}
	if got[6].Error == nil || got[6].Error.Code != -32601 {
		t.Errorf("unknown method: %s", lines[6])
	}
	if task, _ := db.Task(ctx, "t--platform"); task.Status != "in-progress" {
		t.Errorf("task = %+v", task)
	}
}
