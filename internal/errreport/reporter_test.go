package errreport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub records GitHub API calls against an httptest server.
type fakeGitHub struct {
	mu       sync.Mutex
	created  []map[string]any // issue create payloads
	comments []commentCall
	listResp []map[string]any // response for the open-issue listing
}

type commentCall struct {
	Issue int
	Body  string
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(f.listResp)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.created = append(f.created, payload)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 100 + len(f.created)})
	})
	mux.HandleFunc("/repos/o/r/issues/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// /repos/o/r/issues/{n}/comments
		var n int
		_, _ = fmt.Sscanf(r.URL.Path, "/repos/o/r/issues/%d/comments", &n)
		var payload struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.comments = append(f.comments, commentCall{Issue: n, Body: payload.Body})
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": len(f.comments)})
	})
	return mux
}

func (f *fakeGitHub) counts() (created, comments int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created), len(f.comments)
}

func newTestReporter(t *testing.T, gh *fakeGitHub) *Reporter {
	t.Helper()
	srv := httptest.NewServer(gh.handler())
	t.Cleanup(srv.Close)
	return &Reporter{
		cfg:         Config{Enabled: true, Token: "x", Repo: "o/r"},
		apiBase:     srv.URL,
		client:      srv.Client(),
		queue:       make(chan Report, queueSize),
		issues:      map[string]int{},
		occurrences: map[string]int{},
		firstSeen:   map[string]time.Time{},
		lastComment: map[string]time.Time{},
	}
}

func TestFingerprintNormalizesDynamicValues(t *testing.T) {
	a := Report{Source: "server", Message: "chat turn failed: user 3fa85f64-5717-4562-b3fc-2c963f66afa6 timed out after 30000ms"}
	b := Report{Source: "server", Message: "chat turn failed: user 1c9a0b12-2222-4562-b3fc-aaaaaaaaaaaa timed out after 45000ms"}
	if fingerprint(a) != fingerprint(b) {
		t.Fatal("expected same fingerprint for messages differing only in ids/numbers")
	}
	c := Report{Source: "ios", Message: a.Message}
	if fingerprint(a) == fingerprint(c) {
		t.Fatal("expected source to affect fingerprint")
	}
	d := Report{Source: "server", Message: "different error"}
	if fingerprint(a) == fingerprint(d) {
		t.Fatal("expected message to affect fingerprint")
	}
}

func TestProcessCreatesIssueOnceThenComments(t *testing.T) {
	gh := &fakeGitHub{}
	r := newTestReporter(t, gh)
	now := time.Now()
	rep := Report{Source: "web", Message: "boom"}

	r.process(rep, now)
	r.process(rep, now.Add(10*time.Minute))
	r.process(rep, now.Add(20*time.Minute))

	created, comments := gh.counts()
	if created != 1 {
		t.Fatalf("expected 1 issue created, got %d", created)
	}
	// Comment cooldown is 5m, so both repeats (10m and 20m later) comment.
	if comments != 2 {
		t.Fatalf("expected 2 comments, got %d", comments)
	}

	gh.mu.Lock()
	body, _ := gh.created[0]["body"].(string)
	title, _ := gh.created[0]["title"].(string)
	labels, _ := gh.created[0]["labels"].([]any)
	gh.mu.Unlock()

	if title != "[web] boom" {
		t.Fatalf("unexpected title: %q", title)
	}
	if !strings.Contains(body, "<!-- donna-fingerprint:") {
		t.Fatal("issue body missing fingerprint marker")
	}
	if len(labels) != 2 || labels[0] != "auto-error" || labels[1] != "web" {
		t.Fatalf("unexpected labels: %v", labels)
	}
}

func TestProcessRespectsCommentCooldown(t *testing.T) {
	gh := &fakeGitHub{}
	r := newTestReporter(t, gh)
	now := time.Now()
	rep := Report{Source: "ios", Message: "crash"}

	r.process(rep, now)
	r.process(rep, now.Add(time.Minute))
	r.process(rep, now.Add(2*time.Minute))

	_, comments := gh.counts()
	if comments != 0 {
		t.Fatalf("expected 0 comments inside cooldown, got %d", comments)
	}
}

func TestProcessDedupsFromIssueListing(t *testing.T) {
	rep := Report{Source: "server", Message: "known failure"}
	gh := &fakeGitHub{
		listResp: []map[string]any{
			{"number": 42, "body": "old issue\n<!-- donna-fingerprint:" + fingerprint(rep) + " -->"},
			{"number": 43, "body": "a PR", "pull_request": map[string]any{}},
		},
	}
	r := newTestReporter(t, gh)
	now := time.Now()

	r.refreshIssues(now)
	r.process(rep, now.Add(commentCooldown+time.Minute))

	created, comments := gh.counts()
	if created != 0 {
		t.Fatalf("expected no new issue, got %d", created)
	}
	if comments != 1 {
		t.Fatalf("expected 1 comment on existing issue, got %d", comments)
	}
	gh.mu.Lock()
	got := gh.comments[0].Issue
	gh.mu.Unlock()
	if got != 42 {
		t.Fatalf("expected comment on issue 42, got %d", got)
	}
}

func TestProcessEnforcesHourlyIssueCap(t *testing.T) {
	gh := &fakeGitHub{}
	r := newTestReporter(t, gh)
	now := time.Now()

	for i := range maxIssuesPerHour + 5 {
		r.process(Report{Source: "server", Message: "error-" + string(rune('a'+i))}, now)
	}

	created, _ := gh.counts()
	if created != maxIssuesPerHour {
		t.Fatalf("expected %d issues (cap), got %d", maxIssuesPerHour, created)
	}
}

func TestReportDropsWhenDisabled(t *testing.T) {
	r := New(Config{Enabled: false})
	r.Report(Report{Source: "web", Message: "x"}) // must not panic or block

	r2 := New(Config{Enabled: true, Token: "", Repo: "o/r"})
	r2.Report(Report{Source: "web", Message: "x"})
}

func TestClampReport(t *testing.T) {
	rep := clampReport(Report{
		Message: strings.Repeat("m", MaxMessage+10),
		Stack:   strings.Repeat("s", MaxStack+10),
		Route:   strings.Repeat("r", MaxRoute+10),
		Context: map[string]string{"k": strings.Repeat("v", maxContextValue+10)},
	})
	if len(rep.Message) > MaxMessage {
		t.Fatalf("message not clamped: %d", len(rep.Message))
	}
	if len(rep.Stack) > MaxStack {
		t.Fatalf("stack not clamped: %d", len(rep.Stack))
	}
	if len(rep.Route) > MaxRoute {
		t.Fatalf("route not clamped: %d", len(rep.Route))
	}
	if len(rep.Context["k"]) > maxContextValue {
		t.Fatalf("context value not clamped: %d", len(rep.Context["k"]))
	}
}
