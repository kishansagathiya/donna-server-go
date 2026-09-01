package localagent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type BrowserProc struct {
	cmd     *exec.Cmd
	URL     string
	Profile string
}

func StartBrowser(ctx context.Context, supportDir, scriptPath string, headed bool) (*BrowserProc, error) {
	if scriptPath == "" {
		return nil, fmt.Errorf("browser_script_missing")
	}
	profile := filepath.Join(supportDir, "browser-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	port := "0"
	cmd := exec.CommandContext(ctx, "node", scriptPath)
	cmd.Env = append(os.Environ(),
		"DONNA_BROWSER_HOST=127.0.0.1",
		"DONNA_BROWSER_PORT="+port,
		"DONNA_BROWSER_USER_DATA_DIR="+profile,
	)
	if headed {
		cmd.Env = append(cmd.Env, "DONNA_BROWSER_HEADED=1")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// The bundled browser script prints LISTEN <url> on startup.
	type result struct {
		url string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := stdout.Read(buf)
		if err != nil {
			ch <- result{err: err}
			return
		}
		line := string(buf[:n])
		url := ""
		for _, part := range []string{"LISTEN ", "listening on "} {
			if i := indexOf(line, part); i >= 0 {
				url = trimURL(line[i+len(part):])
				break
			}
		}
		if url == "" {
			url = "http://127.0.0.1:9229"
		}
		ch <- result{url: url}
	}()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	case <-time.After(8 * time.Second):
		return &BrowserProc{cmd: cmd, URL: "http://127.0.0.1:9229", Profile: profile}, nil
	case r := <-ch:
		if r.err != nil {
			return &BrowserProc{cmd: cmd, URL: "http://127.0.0.1:9229", Profile: profile}, nil
		}
		return &BrowserProc{cmd: cmd, URL: r.url, Profile: profile}, nil
	}
}

func (b *BrowserProc) Ready(ctx context.Context) bool {
	if b == nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL+"/health", nil)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode < 500
}

func (b *BrowserProc) Stop() {
	if b == nil || b.cmd == nil || b.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-b.cmd.Process.Pid, syscall.SIGTERM)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimURL(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == ' ' || s[i] == '\r' {
			break
		}
		out = append(out, s[i])
	}
	return string(out)
}
