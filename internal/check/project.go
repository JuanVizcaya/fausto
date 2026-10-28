package check

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// defaultTimeout bounds a project check that declares no timeout.
const defaultTimeout = 10 * time.Minute

// outputLines is how much of a failed command's output its finding keeps.
const outputLines = 40

// runProjectCheck runs c's command in the repository at root and returns a
// finding when it fails, or nil when it passes.
func runProjectCheck(root string, c spec.ProjectCheck, commands Commands) *Finding {
	timeout := defaultTimeout
	if c.Timeout != "" {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return &Finding{Message: fmt.Sprintf("invalid timeout %q", c.Timeout), Path: spec.Dir + "/checks.yaml"}
		}
		timeout = d
	}
	ctx, cancel := context.WithTimeout(commands.Ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Run)
	cmd.Dir = filepath.Join(root, filepath.FromSlash(c.Dir))
	// A command that leaves children holding its output still ends.
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	var w io.Writer = &out
	if commands.Output != nil {
		fmt.Fprintf(commands.Output, "==> %s: %s\n", c.ID, c.Run)
		w = io.MultiWriter(&out, commands.Output)
	}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	if err == nil {
		return nil
	}
	msg := fmt.Sprintf("`%s` failed: %v", c.Run, err)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		msg = fmt.Sprintf("`%s` ran longer than %s", c.Run, timeout)
	}
	if tail := lastLines(out.String(), outputLines); tail != "" {
		msg += "\n" + tail
	}
	return &Finding{Message: msg}
}

// lastLines returns the last n lines of s, without a trailing newline.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
