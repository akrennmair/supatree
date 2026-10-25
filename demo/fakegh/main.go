// Command fakegh stands in for the GitHub CLI in the demo sandbox. It answers
// exactly the gh calls supatree and workbench's pkg/github make, from a JSON
// state file, and fails loudly on anything else so a new call is noticed.
//
// Each pull request carries a timeline: steps at offsets from its creation
// that move it from checks pending to passing, approved, merged. A step bumps
// updated_at, which is what makes a poll refetch the PR's details, so the
// sidebar follows the timeline the way it follows GitHub.
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// State is the whole fake GitHub.
type State struct {
	PRs []*PR `json:"prs"`
	// NewPRTimeline is given to every PR `gh pr create` opens.
	NewPRTimeline []Step `json:"new_pr_timeline,omitempty"`
	NextReviewID  int    `json:"next_review_id,omitempty"`
}

// PR is one pull request. Created is the timeline's origin.
type PR struct {
	Repo     string    `json:"repo"`
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	Body     string    `json:"body,omitempty"`
	Author   string    `json:"author,omitempty"`
	Head     string    `json:"head"`
	Base     string    `json:"base"`
	HeadSHA  string    `json:"head_sha"`
	Created  time.Time `json:"created"`
	Timeline []Step    `json:"timeline,omitempty"`
	Reviews  []Review  `json:"reviews,omitempty"`
}

// Step changes a PR At seconds after it was created. Empty fields keep the
// previous value.
type Step struct {
	At     float64 `json:"at"`
	State  string  `json:"state,omitempty"`  // open, merged, closed
	Draft  *bool   `json:"draft,omitempty"`  //
	Checks string  `json:"checks,omitempty"` // pending, success, failure
	Review string  `json:"review,omitempty"` // REVIEW_REQUIRED, APPROVED, CHANGES_REQUESTED
}

// Review is one posted review.
type Review struct {
	ID       int             `json:"id"`
	Event    string          `json:"event"`
	Body     string          `json:"body"`
	Comments json.RawMessage `json:"comments,omitempty"`
	At       time.Time       `json:"at"`
}

// view is a PR as of a moment: its timeline folded up to then.
type view struct {
	State     string
	Draft     bool
	Checks    string
	Review    string
	UpdatedAt time.Time
	MergedAt  *time.Time
}

func (p *PR) at(now time.Time) view {
	v := view{State: "open", UpdatedAt: p.Created}
	for _, s := range p.Timeline {
		t := p.Created.Add(time.Duration(s.At * float64(time.Second)))
		if t.After(now) {
			continue
		}
		if s.State != "" {
			v.State = s.State
			if s.State == "merged" {
				m := t
				v.MergedAt = &m
			}
		}
		if s.Draft != nil {
			v.Draft = *s.Draft
		}
		if s.Checks != "" {
			v.Checks = s.Checks
		}
		if s.Review != "" {
			v.Review = s.Review
		}
		if t.After(v.UpdatedAt) {
			v.UpdatedAt = t
		}
	}
	return v
}

func (p *PR) url() string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", p.Repo, p.Number)
}

// graphqlState is the PR's state the way gh's --json reports it.
func (v view) graphqlState() string {
	return strings.ToUpper(v.State)
}

func (v view) rollup() []map[string]string {
	switch v.Checks {
	case "success":
		return []map[string]string{{"status": "COMPLETED", "conclusion": "SUCCESS"}}
	case "failure":
		return []map[string]string{{"status": "COMPLETED", "conclusion": "FAILURE"}}
	case "pending":
		return []map[string]string{{"status": "IN_PROGRESS", "conclusion": ""}}
	}
	return []map[string]string{}
}

func (p *PR) ghJSON(now time.Time) map[string]any {
	v := p.at(now)
	name := p.Repo[strings.Index(p.Repo, "/")+1:]
	return map[string]any{
		"number":            p.Number,
		"state":             v.graphqlState(),
		"title":             p.Title,
		"url":               p.url(),
		"isDraft":           v.Draft,
		"reviewDecision":    v.Review,
		"statusCheckRollup": v.rollup(),
		"headRefName":       p.Head,
		"headRefOid":        p.HeadSHA,
		"baseRefName":       p.Base,
		"updatedAt":         v.UpdatedAt.UTC().Format(time.RFC3339),
		"headRepository":    map[string]string{"name": name},
		"author":            map[string]string{"login": p.Author},
	}
}

func (p *PR) restJSON(now time.Time) map[string]any {
	v := p.at(now)
	state := "open"
	if v.State != "open" {
		state = "closed"
	}
	var merged any
	if v.MergedAt != nil {
		merged = v.MergedAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"number":     p.Number,
		"state":      state,
		"draft":      v.Draft,
		"title":      p.Title,
		"html_url":   p.url(),
		"updated_at": v.UpdatedAt.UTC().Format(time.RFC3339),
		"merged_at":  merged,
		"head":       map[string]string{"ref": p.Head, "sha": p.HeadSHA},
		"base":       map[string]string{"ref": p.Base},
		"user":       map[string]string{"login": p.Author},
	}
}

func main() {
	logCall(os.Args[1:])
	code, err := run(os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func dir() string {
	if d := os.Getenv("FAKEGH_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "demo", "gh")
}

func logCall(args []string) {
	f, err := os.OpenFile(filepath.Join(dir(), "calls.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	wd, _ := os.Getwd()
	_, _ = fmt.Fprintf(f, "%s %s (in %s)\n", time.Now().Format("15:04:05"), strings.Join(args, " "), wd)
}

func run(args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	if len(args) == 0 {
		return 1, errors.New("fake gh: no command")
	}
	switch {
	case args[0] == "--version":
		_, _ = fmt.Fprintln(stdout, "gh version 2.99.0 (demo)")
		return 0, nil
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		_, _ = fmt.Fprintln(stdout, "github.com\n  ✓ Logged in to github.com account demo (demo)")
		return 0, nil
	case args[0] == "api":
		return api(args[1:], stdin, stdout)
	case len(args) == 4 && args[0] == "demo" && args[1] == "merge":
		// Not gh's: how a scene's setup has an author merge a PR.
		return merge(args[2], args[3])
	case len(args) >= 2 && args[0] == "pr":
		switch args[1] {
		case "view":
			return prView(args[2:], stdout)
		case "list":
			return prList(args[2:], stdout)
		case "create":
			return prCreate(args[2:], stdout)
		}
	}
	return 1, unsupported(args)
}

func unsupported(args []string) error {
	msg := "fake gh: unsupported call: gh " + strings.Join(args, " ")
	if f, err := os.OpenFile(filepath.Join(dir(), "unsupported.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_, _ = fmt.Fprintln(f, msg)
		_ = f.Close()
	}
	return errors.New(msg)
}

// flags splits args into positionals and --flag values. Boolean flags must be
// listed, so `--fill` does not swallow the next argument.
func flags(args []string, booleans ...string) (pos []string, vals map[string][]string) {
	vals = map[string][]string{}
	isBool := map[string]bool{}
	for _, b := range booleans {
		isBool[b] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if k, v, ok := strings.Cut(name, "="); ok {
			vals[k] = append(vals[k], v)
			continue
		}
		if isBool[name] || i+1 >= len(args) {
			vals[name] = append(vals[name], "")
			continue
		}
		vals[name] = append(vals[name], args[i+1])
		i++
	}
	return pos, vals
}

func first(vals map[string][]string, names ...string) string {
	for _, n := range names {
		if v := vals[n]; len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func has(vals map[string][]string, names ...string) bool {
	for _, n := range names {
		if _, ok := vals[n]; ok {
			return true
		}
	}
	return false
}

// withState runs fn on the state under an exclusive lock, saving it if fn
// reports a change.
func withState(fn func(s *State) (bool, error)) error {
	path := filepath.Join(dir(), "state.json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	var s State
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("fake gh: %s: %w", path, err)
		}
	}
	changed, err := fn(&s)
	if err != nil || !changed {
		return err
	}
	out, err := json.MarshalIndent(&s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readState() (*State, error) {
	var out *State
	err := withState(func(s *State) (bool, error) { out = s; return false, nil })
	return out, err
}

func (s *State) find(repo string, number int) *PR {
	for _, p := range s.PRs {
		if p.Repo == repo && p.Number == number {
			return p
		}
	}
	return nil
}

var ghRemote = regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/]+?)(?:\.git)?/?$`)

// repoOf is the owner/name of the repo gh would infer from the working
// directory: its origin.
func repoOf(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	out, err := exec.CommandContext(context.Background(), "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", errors.New("fake gh: no origin remote here")
	}
	m := ghRemote.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", fmt.Errorf("fake gh: origin %q is not a GitHub repo", strings.TrimSpace(string(out)))
	}
	return m[1] + "/" + m[2], nil
}

func gitOut(args ...string) string {
	out, err := exec.CommandContext(context.Background(), "git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func writeJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func prView(args []string, stdout io.Writer) (int, error) {
	pos, vals := flags(args)
	if len(pos) == 0 {
		return 1, unsupported(append([]string{"pr", "view"}, args...))
	}
	repo, err := repoOf(first(vals, "repo", "R"))
	if err != nil {
		return 1, err
	}
	number, err := strconv.Atoi(strings.TrimPrefix(pos[0], "#"))
	if err != nil {
		return 1, unsupported(append([]string{"pr", "view"}, args...))
	}
	s, err := readState()
	if err != nil {
		return 1, err
	}
	p := s.find(repo, number)
	if p == nil {
		return 1, fmt.Errorf("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)", number)
	}
	return 0, writeJSON(stdout, p.ghJSON(time.Now()))
}

func prList(args []string, stdout io.Writer) (int, error) {
	_, vals := flags(args)
	repo, err := repoOf(first(vals, "repo", "R"))
	if err != nil {
		return 1, err
	}
	s, err := readState()
	if err != nil {
		return 1, err
	}
	head := first(vals, "head", "H")
	limit, _ := strconv.Atoi(first(vals, "limit", "L"))
	now := time.Now()
	out := []map[string]any{}
	for _, p := range newestFirst(s.PRs, now) {
		if p.Repo != repo || (head != "" && p.Head != head) {
			continue
		}
		out = append(out, p.ghJSON(now))
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return 0, writeJSON(stdout, out)
}

func newestFirst(prs []*PR, now time.Time) []*PR {
	out := append([]*PR(nil), prs...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].at(now).UpdatedAt.After(out[j-1].at(now).UpdatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func prCreate(args []string, stdout io.Writer) (int, error) {
	_, vals := flags(args, "fill", "draft", "d", "f")
	repo, err := repoOf(first(vals, "repo", "R"))
	if err != nil {
		return 1, err
	}
	head := first(vals, "head", "H")
	if head == "" {
		head = gitOut("rev-parse", "--abbrev-ref", "HEAD")
	}
	title := first(vals, "title", "t")
	if title == "" {
		title = gitOut("log", "-1", "--format=%s")
	}
	base := first(vals, "base", "B")
	if base == "" {
		base = "main"
	}
	var created *PR
	err = withState(func(s *State) (bool, error) {
		for _, p := range s.PRs {
			if p.Repo == repo && p.Head == head && p.at(time.Now()).State == "open" {
				return false, fmt.Errorf("a pull request for branch %q into branch %q already exists:\n%s", head, base, p.url())
			}
		}
		number := 1
		for _, p := range s.PRs {
			if p.Repo == repo && p.Number >= number {
				number = p.Number + 1
			}
		}
		created = &PR{
			Repo: repo, Number: number, Title: title, Body: first(vals, "body", "b"),
			Author: "you", Head: head, Base: base, HeadSHA: gitOut("rev-parse", "HEAD"),
			Created: time.Now().UTC(), Timeline: s.NewPRTimeline,
		}
		if has(vals, "draft", "d") {
			draft := true
			created.Timeline = append([]Step{{At: 0, Draft: &draft}}, created.Timeline...)
		}
		s.PRs = append(s.PRs, created)
		return true, nil
	})
	if err != nil {
		return 1, err
	}
	_, err = fmt.Fprintln(stdout, created.url())
	return 0, err
}

var (
	pullsPath   = regexp.MustCompile(`^/?repos/([^/]+/[^/]+)/pulls$`)
	reviewsPath = regexp.MustCompile(`^/?repos/([^/]+/[^/]+)/pulls/(\d+)/reviews$`)
)

func api(args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	pos, vals := flags(args, "i", "include", "paginate", "silent")
	if len(pos) == 0 {
		return 1, unsupported(append([]string{"api"}, args...))
	}
	if pos[0] == "graphql" {
		return graphql(vals, stdout)
	}
	u, err := url.Parse(pos[0])
	if err != nil {
		return 1, err
	}
	method := strings.ToUpper(first(vals, "method", "X"))
	if m := pullsPath.FindStringSubmatch(u.Path); m != nil && (method == "" || method == "GET") {
		return pulls(m[1], u.Query(), first(vals, "H", "header"), has(vals, "i", "include"), stdout)
	}
	if m := reviewsPath.FindStringSubmatch(u.Path); m != nil && method == "POST" {
		number, _ := strconv.Atoi(m[2])
		return postReview(m[1], number, stdin, stdout)
	}
	return 1, unsupported(append([]string{"api"}, args...))
}

// pulls answers the REST pull request listing as `gh api -i` prints it: an
// HTTP response, with the ETag and rate limit headers the poller reads.
func pulls(repo string, q url.Values, header string, include bool, stdout io.Writer) (int, error) {
	s, err := readState()
	if err != nil {
		return 1, err
	}
	now := time.Now()
	head := q.Get("head")
	if _, branch, ok := strings.Cut(head, ":"); ok {
		head = branch
	}
	limit, _ := strconv.Atoi(q.Get("per_page"))
	out := []map[string]any{}
	for _, p := range newestFirst(s.PRs, now) {
		if p.Repo != repo || (head != "" && p.Head != head) {
			continue
		}
		out = append(out, p.restJSON(now))
		if limit > 0 && len(out) == limit {
			break
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return 1, err
	}
	etag := fmt.Sprintf(`W/"%x"`, sha1.Sum(body))
	reset := now.Add(time.Hour).Unix()
	headers := fmt.Sprintf("X-Ratelimit-Limit: 5000\r\nX-Ratelimit-Remaining: 4990\r\nX-Ratelimit-Resource: core\r\nX-Ratelimit-Reset: %d\r\n", reset)
	if _, prev, ok := strings.Cut(header, "If-None-Match:"); ok && strings.TrimSpace(prev) == etag {
		if include {
			_, _ = fmt.Fprintf(stdout, "HTTP/1.1 304 Not Modified\r\nEtag: %s\r\n%s\r\n", etag, headers)
		}
		return 1, nil
	}
	if include {
		_, _ = fmt.Fprintf(stdout, "HTTP/1.1 200 OK\r\nContent-Type: application/json; charset=utf-8\r\nEtag: %s\r\n%s\r\n", etag, headers)
	}
	_, err = stdout.Write(append(body, '\n'))
	return 0, err
}

// postReview records a review and moves the PR's review decision the way
// GitHub would.
func postReview(repo string, number int, stdin io.Reader, stdout io.Writer) (int, error) {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return 1, err
	}
	var in struct {
		Event    string          `json:"event"`
		Body     string          `json:"body"`
		Comments json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &in); err != nil {
		return 1, fmt.Errorf("fake gh: review body: %w", err)
	}
	var posted Review
	err = withState(func(s *State) (bool, error) {
		p := s.find(repo, number)
		if p == nil {
			return false, fmt.Errorf("gh: Not Found (HTTP 404)")
		}
		s.NextReviewID++
		posted = Review{ID: 1000 + s.NextReviewID, Event: in.Event, Body: in.Body, Comments: in.Comments, At: time.Now().UTC()}
		p.Reviews = append(p.Reviews, posted)
		if decision := reviewState(in.Event); decision != "COMMENTED" {
			p.Timeline = append(p.Timeline, Step{At: time.Since(p.Created).Seconds(), Review: decision})
		}
		return true, nil
	})
	if err != nil {
		return 1, err
	}
	return 0, writeJSON(stdout, map[string]any{
		"id":       posted.ID,
		"state":    reviewState(in.Event),
		"body":     posted.Body,
		"html_url": fmt.Sprintf("https://github.com/%s/pull/%d#pullrequestreview-%d", repo, number, posted.ID),
	})
}

// merge moves a PR to merged now.
func merge(repo, number string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(number, "#"))
	if err != nil {
		return 1, err
	}
	return 0, withState(func(s *State) (bool, error) {
		p := s.find(repo, n)
		if p == nil {
			return false, fmt.Errorf("fake gh: no PR %s#%d", repo, n)
		}
		p.Timeline = append(p.Timeline, Step{At: time.Since(p.Created).Seconds(), State: "merged"})
		return true, nil
	})
}

// reviewState is the state a review with this event ends up in.
func reviewState(event string) string {
	switch event {
	case "APPROVE":
		return "APPROVED"
	case "REQUEST_CHANGES":
		return "CHANGES_REQUESTED"
	}
	return "COMMENTED"
}

// graphql answers pr_comments' query with the PR's reviews and no threads.
func graphql(vals map[string][]string, stdout io.Writer) (int, error) {
	fields := map[string]string{}
	for _, kv := range append(vals["F"], vals["f"]...) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			fields[k] = v
		}
	}
	number, _ := strconv.Atoi(fields["number"])
	s, err := readState()
	if err != nil {
		return 1, err
	}
	p := s.find(fields["owner"]+"/"+fields["name"], number)
	if p == nil {
		return 1, fmt.Errorf("GraphQL: Could not resolve to a PullRequest with the number of %d", number)
	}
	reviews := []map[string]any{}
	for _, r := range p.Reviews {
		reviews = append(reviews, map[string]any{
			"author":      map[string]string{"login": "you"},
			"state":       reviewState(r.Event),
			"body":        r.Body,
			"submittedAt": r.At.Format(time.RFC3339),
			"url":         fmt.Sprintf("%s#pullrequestreview-%d", p.url(), r.ID),
		})
	}
	empty := map[string]any{"nodes": []any{}}
	return 0, writeJSON(stdout, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"number": p.Number, "title": p.Title, "url": p.url(),
		"comments": empty, "reviews": map[string]any{"nodes": reviews}, "reviewThreads": empty,
	}}}})
}
