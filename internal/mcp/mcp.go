// Package mcp is fausto's Model Context Protocol server for agents
// (`fausto mcp`): over stdio, it gives an agent its tasks, lets it start
// and finish them, and runs the drift checks where it works. Tasks go
// through fausto's API with the clone's agent token (see package client).
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/client"
	"github.com/JuanVizcaya/fausto/internal/spec"
)

// Server answers MCP requests for the clone at Root.
type Server struct {
	// Root is the project clone the agent works in; "" when there is none.
	Root string
	// Client reaches fausto with the agent's token; nil when the clone
	// isn't connected, which only the check tool works without.
	Client  *client.Client
	Version string

	mu sync.Mutex // one response at a time on out
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads newline-delimited JSON-RPC messages from in and writes the
// responses to out, until in ends or ctx is done.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.write(out, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification, such as notifications/initialized
		}
		result, rerr := s.handle(ctx, req)
		s.write(out, response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr})
	}
	return sc.Err()
}

func (s *Server) write(out io.Writer, r response) {
	data, _ := json.Marshal(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	out.Write(append(data, '\n'))
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "fausto", "version": s.Version},
			"instructions": "Your work arrives as fausto tasks. List them with fausto_tasks, mark one started with fausto_task_start, " +
				"run fausto_check before finishing, and mark it done with fausto_task_done once the checks pass and your work is committed.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		text, err := s.call(ctx, p.Name, p.Arguments)
		if err != nil {
			return map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}, nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var idSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"id": map[string]any{"type": "string", "description": "The task's id, such as 2026-10-16-assess-nats--platform."}},
	"required":   []string{"id"},
}

var tools = []tool{
	{"fausto_tasks", "Your tasks from fausto, oldest first, each with what to change and the checks to fix. By default the open and in-progress ones.",
		map[string]any{"type": "object", "properties": map[string]any{
			"status": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"open", "in-progress", "done", "dropped"}}},
		}}},
	{"fausto_task_start", "Mark one of your tasks in progress, when you start working on it.", idSchema},
	{"fausto_task_done", "Mark one of your tasks done: only once fausto_check passes for your area and your work is committed on your branch.", idSchema},
	{"fausto_task_drop", "Drop one of your tasks that turned out not to need doing; say why in your summary.", idSchema},
	{"fausto_check", "Run fausto's drift checks on this clone: the code against the architecture spec in system/. Name checks to run only those.",
		map[string]any{"type": "object", "properties": map[string]any{
			"checks": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": check.Names()}},
		}}},
}

var errNotConnected = errors.New("this clone isn't connected to fausto: run `fausto connect -token <token>` in it, or start the agent with `fausto claude <agent>`")

func (s *Server) call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var a struct {
		ID     string   `json:"id"`
		Status []string `json:"status"`
		Checks []string `json:"checks"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	switch name {
	case "fausto_tasks":
		if s.Client == nil {
			return "", errNotConnected
		}
		statuses := a.Status
		if len(statuses) == 0 {
			statuses = []string{"open", "in-progress"}
		}
		list, err := s.Client.Tasks(ctx, statuses...)
		if err != nil {
			return "", err
		}
		if len(list) == 0 {
			return "You have no " + strings.Join(statuses, " or ") + " tasks.", nil
		}
		var b strings.Builder
		for i, t := range list {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.Write(t.Markdown(true))
		}
		return b.String(), nil
	case "fausto_task_start", "fausto_task_done", "fausto_task_drop":
		if s.Client == nil {
			return "", errNotConnected
		}
		if a.ID == "" {
			return "", errors.New("name the task: id")
		}
		status := map[string]string{"fausto_task_start": "in-progress", "fausto_task_done": "done", "fausto_task_drop": "dropped"}[name]
		from, err := s.Client.SetStatus(ctx, a.ID, status)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s → %s", a.ID, from, status), nil
	case "fausto_check":
		if s.Root == "" {
			return "", errors.New("not in a project clone: no " + spec.Dir + "/system.yaml here or above")
		}
		for _, c := range a.Checks {
			if !slices.Contains(check.Names(), c) {
				return "", fmt.Errorf("unknown check %q (available: %s)", c, strings.Join(check.Names(), ", "))
			}
		}
		var opts spec.Options
		if s.Client != nil {
			ch, err := s.Client.Charters(ctx)
			if err != nil {
				return "", err
			}
			opts.Charters = ch
		}
		report, err := check.RunWith(s.Root, a.Checks, opts)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		report.WriteText(&b)
		return b.String(), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}
