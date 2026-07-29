// Package errreport turns server and client errors into GitHub issues.
//
// Reports are fingerprinted (source + normalized message + route) so the first
// occurrence of an error creates one issue and repeats add a comment instead of
// flooding the tracker. Everything runs on a single background goroutine fed by
// a bounded queue: reporting never blocks request paths and a crash loop can
// never overwhelm GitHub.
package errreport

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

const (
	defaultAPIBase = "https://api.github.com"

	queueSize        = 256
	commentCooldown  = 5 * time.Minute
	issueListMaxAge  = 30 * time.Second
	maxIssuesPerHour = 10

	MaxMessage = 1000
	MaxStack   = 8000
	MaxRoute   = 200
	MaxVersion = 100

	maxContextKeys   = 20
	maxContextValue  = 200
	issueTitleMaxLen = 140

	fingerprintLabel = "auto-error"
)

// Report is a single error occurrence from the server or a client.
type Report struct {
	Source     string // "server" | "web" | "ios"
	Message    string
	Stack      string
	Route      string
	AppVersion string
	Context    map[string]string
}

type Config struct {
	Enabled bool
	Token   string
	Repo    string // "owner/name"
}

type Reporter struct {
	cfg     Config
	apiBase string
	client  *http.Client

	queue chan Report

	// Worker-confined state (only touched from run/process).
	issues      map[string]int       // fingerprint -> issue number
	occurrences map[string]int       // fingerprint -> count since start
	firstSeen   map[string]time.Time // fingerprint -> first occurrence since start
	lastComment map[string]time.Time // fingerprint -> last comment time
	createdAt   []time.Time          // timestamps of created issues (global cap)
	listAge     time.Time            // last time open issues were listed
}

func New(cfg Config) *Reporter {
	r := &Reporter{
		cfg:         cfg,
		apiBase:     defaultAPIBase,
		client:      &http.Client{Timeout: 15 * time.Second},
		queue:       make(chan Report, queueSize),
		issues:      map[string]int{},
		occurrences: map[string]int{},
		firstSeen:   map[string]time.Time{},
		lastComment: map[string]time.Time{},
	}
	if r.Enabled() {
		go r.run()
	}
	return r
}

func (r *Reporter) Enabled() bool {
	return r != nil && r.cfg.Enabled && r.cfg.Token != "" && r.cfg.Repo != ""
}

// Report enqueues an error report. It never blocks: when the queue is full the
// report is dropped (a flood of errors must not take the server down with it).
func (r *Reporter) Report(rep Report) {
	if !r.Enabled() {
		return
	}
	select {
	case r.queue <- rep:
	default:
	}
}

func (r *Reporter) run() {
	r.refreshIssues(time.Now())
	for rep := range r.queue {
		r.process(rep, time.Now())
	}
}

func (r *Reporter) process(rep Report, now time.Time) {
	rep = clampReport(rep)
	fp := fingerprint(rep)
	r.occurrences[fp]++
	if _, ok := r.firstSeen[fp]; !ok {
		r.firstSeen[fp] = now
	}

	if num := r.issues[fp]; num > 0 {
		r.commentOnIssue(fp, num, now)
		return
	}

	// Cold miss: refresh the open-issue cache before creating a duplicate.
	if now.Sub(r.listAge) > issueListMaxAge {
		r.refreshIssues(now)
		if num := r.issues[fp]; num > 0 {
			r.commentOnIssue(fp, num, now)
			return
		}
	}

	if !r.canCreateIssue(now) {
		log.Warn("errreport: hourly issue cap reached; dropping report", map[string]any{
			"source":  rep.Source,
			"message": truncate(rep.Message, 120),
		})
		return
	}

	num, err := r.createIssue(rep, fp, now)
	if err != nil {
		log.Warn("errreport: failed to create issue", map[string]any{"error": err.Error()})
		return
	}
	r.issues[fp] = num
	r.createdAt = append(r.createdAt, now)
	// A brand-new issue already represents the current occurrence; repeats
	// inside the cooldown window should stay silent.
	r.lastComment[fp] = now
}

// canCreateIssue enforces a rolling global cap on new issues per hour.
func (r *Reporter) canCreateIssue(now time.Time) bool {
	cutoff := now.Add(-time.Hour)
	kept := r.createdAt[:0]
	for _, t := range r.createdAt {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.createdAt = kept
	return len(r.createdAt) < maxIssuesPerHour
}

func (r *Reporter) commentOnIssue(fp string, num int, now time.Time) {
	if last, ok := r.lastComment[fp]; ok && now.Sub(last) < commentCooldown {
		return
	}
	body := fmt.Sprintf("Still occurring — %d occurrences since %s (last seen %s).",
		r.occurrences[fp],
		r.firstSeen[fp].UTC().Format(time.RFC3339),
		now.UTC().Format(time.RFC3339),
	)
	payload := map[string]any{"body": body}
	if err := r.do(http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", r.cfg.Repo, num), payload, nil); err != nil {
		log.Warn("errreport: failed to comment on issue", map[string]any{"issue": num, "error": err.Error()})
		return
	}
	r.lastComment[fp] = now
}

func (r *Reporter) createIssue(rep Report, fp string, now time.Time) (int, error) {
	title := fmt.Sprintf("[%s] %s", rep.Source, truncate(firstLine(rep.Message), issueTitleMaxLen))
	payload := map[string]any{
		"title":  title,
		"body":   buildIssueBody(rep, fp, now),
		"labels": []string{fingerprintLabel, rep.Source},
	}
	var resp struct {
		Number int `json:"number"`
	}
	if err := r.do(http.MethodPost, fmt.Sprintf("/repos/%s/issues", r.cfg.Repo), payload, &resp); err != nil {
		return 0, err
	}
	log.Print("errreport: created issue", map[string]any{"issue": resp.Number, "title": title})
	return resp.Number, nil
}

var fingerprintMarker = regexp.MustCompile(`<!-- donna-fingerprint:([0-9a-f]{16}) -->`)

// refreshIssues lists open auto-error issues and rebuilds the fingerprint map
// so repeats after a restart comment on the existing issue instead of creating
// duplicates.
func (r *Reporter) refreshIssues(now time.Time) {
	r.listAge = now
	var items []struct {
		Number      int    `json:"number"`
		Body        string `json:"body"`
		PullRequest *struct {
		} `json:"pull_request"`
	}
	path := fmt.Sprintf("/repos/%s/issues?state=open&labels=%s&per_page=100", r.cfg.Repo, fingerprintLabel)
	if err := r.do(http.MethodGet, path, nil, &items); err != nil {
		log.Warn("errreport: failed to list open issues", map[string]any{"error": err.Error()})
		return
	}
	for _, item := range items {
		if item.PullRequest != nil {
			continue
		}
		if m := fingerprintMarker.FindStringSubmatch(item.Body); m != nil {
			r.issues[m[1]] = item.Number
		}
	}
}

func (r *Reporter) do(method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, r.apiBase+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "donna-server-go")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: github %d: %s", method, path, resp.StatusCode, truncate(string(raw), 300))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: decode body: %w", method, path, err)
		}
	}
	return nil
}

var (
	uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	hexPattern  = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)
	// No trailing \b: volatile numbers often carry units (30000ms, 404px).
	numPattern = regexp.MustCompile(`\b\d{4,}`)
)

// fingerprint normalizes dynamic values (ids, long numbers) so the same bug
// hitting different users/sessions maps to one issue.
func fingerprint(rep Report) string {
	norm := strings.ToLower(rep.Message)
	norm = uuidPattern.ReplaceAllString(norm, "#")
	norm = hexPattern.ReplaceAllString(norm, "#")
	norm = numPattern.ReplaceAllString(norm, "#")
	sum := sha1.Sum([]byte(rep.Source + "|" + norm + "|" + rep.Route))
	return hex.EncodeToString(sum[:8])
}

func buildIssueBody(rep Report, fp string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Source:** `%s`\n", rep.Source)
	fmt.Fprintf(&b, "**First seen:** %s\n", now.UTC().Format(time.RFC3339))
	if rep.Route != "" {
		fmt.Fprintf(&b, "**Route:** `%s`\n", rep.Route)
	}
	if rep.AppVersion != "" {
		fmt.Fprintf(&b, "**App version:** `%s`\n", rep.AppVersion)
	}
	fmt.Fprintf(&b, "\n**Message:**\n```\n%s\n```\n", fenceSafe(rep.Message))
	if len(rep.Context) > 0 {
		keys := make([]string, 0, len(rep.Context))
		for k := range rep.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\n**Context:**\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "- `%s`: %s\n", k, rep.Context[k])
		}
	}
	if rep.Stack != "" {
		fmt.Fprintf(&b, "\n<details><summary>Stack trace</summary>\n\n```\n%s\n```\n</details>\n", fenceSafe(rep.Stack))
	}
	fmt.Fprintf(&b, "\n_Automatically created by donna-server-go error reporting._\n<!-- donna-fingerprint:%s -->\n", fp)
	return b.String()
}

func clampReport(rep Report) Report {
	rep.Message = truncate(rep.Message, MaxMessage)
	rep.Stack = truncate(rep.Stack, MaxStack)
	rep.Route = truncate(rep.Route, MaxRoute)
	rep.AppVersion = truncate(rep.AppVersion, MaxVersion)
	if len(rep.Context) > maxContextKeys {
		keys := make([]string, 0, len(rep.Context))
		for k := range rep.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		trimmed := make(map[string]string, maxContextKeys)
		for _, k := range keys[:maxContextKeys] {
			trimmed[k] = rep.Context[k]
		}
		rep.Context = trimmed
	}
	for k, v := range rep.Context {
		rep.Context[k] = truncate(v, maxContextValue)
	}
	return rep
}

// fenceSafe keeps error text from breaking out of markdown code fences.
func fenceSafe(s string) string {
	return strings.ReplaceAll(s, "```", "'''")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
