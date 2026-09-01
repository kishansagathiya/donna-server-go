package localagent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

const (
	defaultShellTimeout = 60 * time.Second
	maxShellTimeout     = 5 * time.Minute
	maxShellOutput      = 32 * 1024
)

var destructiveShell = regexp.MustCompile(`(?i)\b(rm\s+-rf|rm\s+-r|sudo|mkfs|dd\s+if=|shutdown|reboot|diskutil|chmod\s+777|chown\s+root)\b`)

func shellTool(root string) agents.RegisteredTool {
	return agents.RegisteredTool{
		Toolset: "process",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "run_shell",
				Description: "Run a command inside the approved workspace. Destructive commands require prior request_approval. Timeout and output are capped. Environment secrets are not returned.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command":    map[string]any{"type": "string"},
						"timeout_ms": map[string]any{"type": "integer"},
					},
					"required": []string{"command"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *agents.RunContext, argsJSON string) (agents.ToolResult, error) {
			args, err := agents.ParseArgs[struct {
				Command   string `json:"command"`
				TimeoutMs int    `json:"timeout_ms"`
			}](argsJSON)
			if err != nil {
				return agents.ToolResult{}, err
			}
			command := strings.TrimSpace(args.Command)
			if command == "" {
				return agents.ToolResult{Content: "Error: command is required"}, nil
			}
			if destructiveShell.MatchString(command) {
				return agents.ToolResult{Content: "Error: destructive command requires request_approval first and is blocked until approved."}, nil
			}
			cwd, err := ResolveWorkspacePath(root, ".")
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			timeout := defaultShellTimeout
			if args.TimeoutMs > 0 {
				timeout = time.Duration(args.TimeoutMs) * time.Millisecond
			}
			if timeout > maxShellTimeout {
				timeout = maxShellTimeout
			}
			runCtx2, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			cmd := exec.CommandContext(runCtx2, "/bin/zsh", "-lc", command)
			cmd.Dir = cwd
			cmd.Env = allowedEnv()
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &limitWriter{buf: &stdout, n: maxShellOutput}
			cmd.Stderr = &limitWriter{buf: &stderr, n: maxShellOutput}
			started := time.Now()
			runErr := cmd.Run()
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			exit := 0
			if runErr != nil {
				exit = 1
				if ee, ok := runErr.(*exec.ExitError); ok {
					exit = ee.ExitCode()
				}
				if runCtx2.Err() != nil {
					exit = -1
				}
			}
			out := strings.TrimSpace(stdout.String())
			errOut := strings.TrimSpace(stderr.String())
			content := fmt.Sprintf("exit=%d duration_ms=%d\nstdout:\n%s\nstderr:\n%s", exit, time.Since(started).Milliseconds(), out, errOut)
			return agents.ToolResult{
				Content: content,
				Meta: map[string]any{
					"command":     command,
					"cwd":         cwd,
					"exit":        exit,
					"duration_ms": time.Since(started).Milliseconds(),
				},
			}, nil
		},
	}
}

func allowedEnv() []string {
	allow := []string{"PATH", "HOME", "USER", "TMPDIR", "LANG", "LC_ALL", "TERM"}
	out := make([]string, 0, len(allow))
	for _, key := range allow {
		if v, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+v)
		}
	}
	return out
}

type limitWriter struct {
	buf *bytes.Buffer
	n   int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	remain := w.n - w.buf.Len()
	if remain <= 0 {
		return len(p), nil
	}
	if len(p) > remain {
		w.buf.Write(p[:remain])
		w.buf.WriteString("\n[truncated]")
		return len(p), nil
	}
	return w.buf.Write(p)
}
