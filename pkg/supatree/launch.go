package supatree

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
)

// KickoffPrompt is the first message an agent is launched with when mail is
// waiting for it. It is fixed text on purpose: the brief itself travels through
// the mailbox, which every model can read and which survives a restart, and
// nothing a PM (or a PR description it relayed) wrote ever reaches a command
// line or a layout file.
const KickoffPrompt = "You have been started with messages waiting. Read .supatree/info.md, " +
	"then call the `inbox` tool and carry out what it asks, end to end, without waiting for further input."

// WakePrompt is what an agent that is already running, but idle at its
// prompt, is sent when mail arrives for it. An interactive agent reads its
// mailbox only on a turn, and an idle one takes no turn until something types
// — so a brief left for it was, until this, never read (#15). Fixed text for
// the same reason as KickoffPrompt: it is typed into the agent's terminal.
const WakePrompt = "You have new messages waiting. Call the `inbox` tool and carry out what it asks, end to end, without waiting for further input."

// PMAgentName is the name a tree agent uses to address the top PM, and the
// stem of every other PM's agent name ("pm-<name>", see PM.AgentID). PMs are in
// no tree's agents.yml, so message_agent special-cases them, and the watcher
// forwards what lands in those mailboxes into the PMs' request queue.
const PMAgentName = "pm"

// LaunchRequest asks the watcher to open an agent's tab. The PM cannot do it
// itself: it runs inside nono, and only the watcher runs outside.
type LaunchRequest struct {
	// ID names the request, so whoever queued it can read back what the
	// watcher did with it (LaunchResult).
	ID    string    `json:"id,omitempty"`
	At    time.Time `json:"at"`
	Tree  string    `json:"tree"`
	Agent string    `json:"agent"`
	// Session is the zellij session to open the tab in — the PM's own, read
	// from ZELLIJ_SESSION_NAME. Empty falls back to the watcher's --session.
	Session string `json:"session,omitempty"`
	From    string `json:"from,omitempty"`
}

// QueueLaunch appends a launch request for the watcher and returns its ID.
func QueueLaunch(r LaunchRequest) (string, error) {
	if r.Tree == "" {
		return "", fmt.Errorf("launch request has no tree")
	}
	if r.Agent == "" {
		r.Agent = MainAgent
	}
	if err := ValidateAgentName(r.Agent); err != nil {
		return "", err
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	if r.ID == "" {
		r.ID = newOpID()
	}
	err := config.WithFileLock(LaunchLockPath(), func() error {
		return appendJSONL(LaunchPath(), []LaunchRequest{r})
	})
	return r.ID, err
}

// DrainLaunches takes every queued launch request and empties the queue.
func DrainLaunches() ([]LaunchRequest, error) {
	return TakeLaunches(func(LaunchRequest) bool { return true })
}

// TakeLaunches takes, oldest first, the queued requests admit accepts, and
// leaves the rest queued in order — how the watcher holds launches back while
// too many agents are busy (#1). admit runs under the queue lock, so it must
// not block. A torn line costs that request only.
func TakeLaunches(admit func(LaunchRequest) bool) ([]LaunchRequest, error) {
	var out []LaunchRequest
	err := config.WithFileLock(LaunchLockPath(), func() error {
		data, err := os.ReadFile(LaunchPath())
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var rest []LaunchRequest
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var r LaunchRequest
			if err := json.Unmarshal([]byte(line), &r); err != nil || r.Tree == "" {
				continue
			}
			if admit(r) {
				out = append(out, r)
			} else {
				rest = append(rest, r)
			}
		}
		if err := os.Remove(LaunchPath()); err != nil {
			return err
		}
		if len(rest) == 0 {
			return nil
		}
		return appendJSONL(LaunchPath(), rest)
	})
	return out, err
}

// QueuedLaunches counts the requests still waiting for the watcher.
func QueuedLaunches() int {
	n := 0
	_ = config.WithFileLock(LaunchLockPath(), func() error {
		data, err := os.ReadFile(LaunchPath())
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		return nil
	})
	return n
}

// LaunchOutcome is what opening an agent came to.
type LaunchOutcome string

const (
	LaunchStarted LaunchOutcome = "started" // a new agent process, in a new tab
	LaunchWoken   LaunchOutcome = "woken"   // already running; typed WakePrompt into it
	LaunchRunning LaunchOutcome = "running" // already running; only mailed (no agent pane to type into)
	LaunchFailed  LaunchOutcome = "failed"
)

// OpenOutcomePrefix starts the line `supatree open --background` prints with
// its LaunchOutcome, for the watcher that ran it.
const OpenOutcomePrefix = "supatree-open-outcome: "

// ParseOpenOutcome finds that line in a background open's output.
func ParseOpenOutcome(out string) (LaunchOutcome, bool) {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), OpenOutcomePrefix); ok {
			return LaunchOutcome(v), true
		}
	}
	return "", false
}

// LaunchResult is the watcher's answer to one LaunchRequest, left in
// LaunchResultsDir for whoever queued it.
type LaunchResult struct {
	ID      string        `json:"id"`
	Tree    string        `json:"tree"`
	Agent   string        `json:"agent"`
	Outcome LaunchOutcome `json:"outcome"`
	Detail  string        `json:"detail,omitempty"`
	At      time.Time     `json:"at"`
}

// launchResultTTL bounds how long an answer nobody collected is kept.
const launchResultTTL = time.Hour

// WriteLaunchResult records the watcher's answer, and sweeps answers nobody
// came back for.
func WriteLaunchResult(r LaunchResult) error {
	if r.ID == "" {
		return nil
	}
	dir := LaunchResultsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > launchResultTTL {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	return writeFileAtomic(filepath.Join(dir, r.ID+".json"), data)
}

// WaitLaunchResult waits up to timeout for the watcher's answer to request id,
// and consumes it.
func WaitLaunchResult(id string, timeout time.Duration) (LaunchResult, bool) {
	path := filepath.Join(LaunchResultsDir(), id+".json")
	deadline := time.Now().Add(timeout)
	for {
		var r LaunchResult
		if err := readJSON(path, &r); err == nil {
			_ = os.Remove(path)
			return r, true
		}
		if time.Now().After(deadline) {
			return LaunchResult{}, false
		}
		time.Sleep(launchResultPoll)
	}
}

const launchResultPoll = 250 * time.Millisecond

// HasMail reports whether an agent has anything waiting. An unreadable mailbox
// reads as empty: the cost is an agent that starts idle, as it always used to.
func HasMail(root, agent string) bool {
	msgs, err := Mail(root, agent)
	return err == nil && len(msgs) > 0
}

// ForwardPMMail moves what tree agents have left for the PM into the PM's
// request queue, and reports how many it moved.
//
// A tree agent is sandboxed to its own tree, so the one place it can leave the
// PM a message is that tree's mailbox — which the PM never looks in unprompted.
// The request queue is what the PM reads first every turn, so that is where a
// reply has to end up. Only the watcher can write both.
func ForwardPMMail(insts []*Instance) (int, error) {
	n := 0
	var firstErr error
	for _, inst := range insts {
		for _, box := range pmMailboxes(inst.Root) {
			msgs, err := Mail(inst.Root, box)
			if err != nil || len(msgs) == 0 {
				continue
			}
			// "pm" is the top PM, which an unaddressed request reaches anyway.
			to := strings.TrimPrefix(strings.TrimPrefix(box, PMAgentName), "-")
			for _, m := range msgs {
				req := Request{At: m.At, From: m.From, Tree: inst.Name, To: to, Text: m.Text}
				if err := AppendRequest(req); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				// Removed only once queued: a failed append leaves the message
				// to be forwarded next round rather than lost.
				_ = os.Remove(m.Path)
				n++
			}
		}
	}
	return n, firstErr
}

// pmMailboxes lists the mailboxes in a tree addressed to a PM rather than to
// one of the tree's own agents.
func pmMailboxes(root string) []string {
	entries, err := os.ReadDir(filepath.Dir(MailDir(root, PMAgentName)))
	if err != nil {
		return nil
	}
	// Without agents.yml a "pm-x" mailbox may be a real tree agent's, and
	// forwarding consumes it — so only the unambiguous top-PM box is taken.
	agents, agentsErr := LoadAgents(root)
	var out []string
	for _, e := range entries {
		if agentsErr != nil && e.Name() != PMAgentName {
			continue
		}
		if e.IsDir() && IsPMAgentID(e.Name()) && FindAgent(agents, e.Name()) == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// DefaultReviewBrief is what a review tree's agent is told to do when the PM
// starts it without a brief of its own. It is the hands-off version of the
// review craft in `docs topic: review`: do the whole review, leave it where
// the human and the PM can both read it, and say when it is done.
//
// pm is the agent name of the PM starting it, which is who the summary goes to.
func DefaultReviewBrief(inst *Instance, pm string) string {
	var b strings.Builder
	b.WriteString("Review the pull requests checked out in this tree, end to end, without waiting for further input.\n\n")
	for _, m := range inst.Members {
		if m.Review != nil {
			fmt.Fprintf(&b, "- %s: %s\n", m.Alias, m.Review.URL)
		}
	}
	b.WriteString(`
1. Call ` + "`docs`" + ` with topic ` + "`review`" + ` and follow it.
2. Call ` + "`pr_comments`" + ` for each repo so you do not repeat what reviewers already said.
3. Build, lint and test each repo here, and do the cross-repo check.
4. Write the full review to .supatree/review.md: one section per pull request, blocking findings separated from non-blocking ones, every line number taken from the pull request head.
5. Post one batched review per pull request with ` + "`review_post`" + `: APPROVE if nothing blocks, REQUEST_CHANGES if something does. If it is refused, leave the review in .supatree/review.md for the human to post.
6. When you are done, call ` + "`message_agent`" + ` with agent "` + pm + `" and a three-line summary: the verdict per pull request and where the review is.
`)
	return b.String()
}

// BusyWindow is how recently an agent's transcript must have grown for it to
// count as working. An agent at its prompt writes nothing, so it falls out of
// the count — the cap would otherwise fill with finished agents and never let
// another launch through.
const BusyWindow = 2 * time.Minute

// AgentBusy reports whether agent's transcript grew within BusyWindow of now.
func AgentBusy(root string, a Agent, now time.Time) bool {
	p := sandbox.SessionTranscript(root, a.SessionID)
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && now.Sub(fi.ModTime()) < BusyWindow
}

// LaunchGate decides which queued launches go now. Waking an agent whose tab
// is already live costs no slot (no new process); a second request for an
// agent already admitted rides with the first.
type LaunchGate struct {
	Max  int                   // busy-agent cap; 0 is none
	Busy int                   // agents busy right now, launches admitted so far included
	Live func(tab string) bool // whether a tab's agent is running
	seen map[string]bool
}

// Admit is the TakeLaunches predicate.
func (g *LaunchGate) Admit(r LaunchRequest) bool {
	if g.seen == nil {
		g.seen = map[string]bool{}
	}
	tab := TabName(r.Tree, r.Agent)
	if g.seen[tab] {
		return true
	}
	if g.Live == nil || !g.Live(tab) {
		if g.Max > 0 && g.Busy >= g.Max {
			return false
		}
		g.Busy++
	}
	g.seen[tab] = true
	return true
}

// LaunchLogPath records every agent and PM launch: what opened, what that
// came to, and who asked. A PM relaunched mid-session used to leave no trace
// of why (#3).
func LaunchLogPath() string {
	return filepath.Join(LogsDir(), "launch.log")
}

// logLaunch appends one line to the launch log. Best effort.
func logLaunch(who, tab, what string) {
	if err := os.MkdirAll(LogsDir(), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(LaunchLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s %s tab=%s %s by=%s\n", time.Now().Format(time.RFC3339), who, tab, what, launchCaller())
}

// launchCaller names what is opening an agent: the sidebar, the watcher, or a
// command someone ran.
func launchCaller() string {
	if os.Getenv("SUPATREE_SIDEBAR") == "1" {
		return "sidebar"
	}
	args := os.Args[1:]
	for _, a := range args {
		if a == "--background" {
			return "watcher"
		}
	}
	if len(args) > 3 {
		args = args[:3]
	}
	return "cli(supatree " + strings.Join(args, " ") + ")"
}

// openLockPath serializes opening one tab. The check that a tab exists and
// the open that follows are seconds apart (preflight, trust, grants), and two
// opens in that window — a `supatree pm` retried while the machine was slow —
// each found no tab and each started a PM on the same session (#1).
func openLockPath(tab string) string {
	return filepath.Join(StateRoot(), "locks", "open-"+strings.ReplaceAll(tab, ":", "_")+".lock")
}
