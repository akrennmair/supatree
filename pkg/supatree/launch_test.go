package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

func TestLaunchQueueRoundTrip(t *testing.T) {
	testutil.IsolateHome(t)

	if reqs, err := DrainLaunches(); err != nil || len(reqs) != 0 {
		t.Fatalf("DrainLaunches on no queue = %v, %v; want none, nil", reqs, err)
	}
	if _, err := QueueLaunch(LaunchRequest{Tree: treeA, Session: sessionMain}); err != nil {
		t.Fatalf("QueueLaunch: %v", err)
	}
	if _, err := QueueLaunch(LaunchRequest{Tree: "hobart", Agent: agentReviewer}); err != nil {
		t.Fatalf("QueueLaunch: %v", err)
	}
	// A torn line costs that request only.
	f, err := os.OpenFile(LaunchPath(), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{\"tree\":\n")
	_ = f.Close()

	reqs, err := DrainLaunches()
	if err != nil {
		t.Fatalf("DrainLaunches: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2: %+v", len(reqs), reqs)
	}
	if reqs[0].Tree != treeA || reqs[0].Agent != "main" || reqs[0].Session != sessionMain {
		t.Errorf("first = %+v; want canberra/main in st-main (agent defaults to main)", reqs[0])
	}
	if reqs[1].Agent != agentReviewer {
		t.Errorf("second agent = %q, want reviewer", reqs[1].Agent)
	}
	// Each launch happens once: a second drain is empty, or every watcher tick
	// would reopen the tab.
	if again, _ := DrainLaunches(); len(again) != 0 {
		t.Errorf("second drain = %+v, want empty", again)
	}
}

func TestQueueLaunchRejectsBadInput(t *testing.T) {
	testutil.IsolateHome(t)
	if _, err := QueueLaunch(LaunchRequest{}); err == nil {
		t.Error("QueueLaunch with no tree succeeded")
	}
	// The agent name becomes a tab name and a mailbox path.
	if _, err := QueueLaunch(LaunchRequest{Tree: treeA, Agent: "../x"}); err == nil {
		t.Error("QueueLaunch with a path-like agent succeeded")
	}
}

func TestForwardPMMail(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	inst := &Instance{Name: treeA, Root: root}

	if err := Deliver(root, PMAgentName, "main", "LGTM on api#12, blocking issue on web#40"); err != nil {
		t.Fatal(err)
	}
	// Mail for anyone else stays where it is.
	if err := Deliver(root, "main", "pm", "review these"); err != nil {
		t.Fatal(err)
	}

	n, err := ForwardPMMail([]*Instance{inst})
	if err != nil || n != 1 {
		t.Fatalf("ForwardPMMail = %d, %v; want 1, nil", n, err)
	}
	reqs, _, err := PendingRequests("")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Tree != treeA || reqs[0].From != "main" || !strings.Contains(reqs[0].Text, "web#40") {
		t.Fatalf("requests = %+v; want the agent's reply, attributed to its tree and sender", reqs)
	}
	if HasMail(root, PMAgentName) {
		t.Error("forwarded mail left in the tree mailbox — it would be forwarded again next round")
	}
	if !HasMail(root, "main") {
		t.Error("ForwardPMMail consumed mail addressed to another agent")
	}
	if n, _ := ForwardPMMail([]*Instance{inst}); n != 0 {
		t.Errorf("second forward moved %d, want 0", n)
	}
}

const (
	sessionMain   = "st-main"
	agentReviewer = "reviewer"
	pmReviews     = "reviews"
)

// noLaunchWait stops startAgent waiting on a watcher no test runs.
func noLaunchWait(t *testing.T) {
	t.Helper()
	orig := launchWait
	launchWait = 0
	t.Cleanup(func() { launchWait = orig })
}

func TestStartAgentBriefsThenQueues(t *testing.T) {
	testutil.IsolateHome(t)
	noLaunchWait(t)
	t.Setenv("ZELLIJ_SESSION_NAME", sessionMain)
	root := t.TempDir()
	inst := &Instance{Name: treeA, Root: root, Model: defaultModelKey}

	// Nothing to do and nothing waiting is an error, not an idle launch.
	if _, err := startAgent(inst, "", "", ""); err == nil {
		t.Error("startAgent with no brief and no mail succeeded")
	}

	if _, err := startAgent(inst, "", "fix the flaky test", ""); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	agents, err := LoadAgents(root)
	if err != nil || FindAgent(agents, "main") == nil {
		t.Fatalf("main not registered (%v): a launch resumes by the session id registered here", err)
	}
	msgs, _ := Mail(root, "main")
	if len(msgs) != 1 || msgs[0].Text != "fix the flaky test" || msgs[0].From != PMAgentName {
		t.Fatalf("mailbox = %+v; want the brief, from the PM", msgs)
	}
	reqs, _ := DrainLaunches()
	if len(reqs) != 1 || reqs[0].Tree != treeA || reqs[0].Agent != "main" || reqs[0].Session != sessionMain {
		t.Fatalf("launches = %+v; want canberra/main in the PM's session", reqs)
	}
}

func TestStartAgentDefaultsReviewBrief(t *testing.T) {
	testutil.IsolateHome(t)
	noLaunchWait(t)
	root := t.TempDir()
	inst := &Instance{Name: "hobart", Root: root, Model: defaultModelKey, Mode: ModeReviewing, Members: []Member{
		{Alias: aliasAPI, Review: &ReviewRef{Repo: "o/api", Number: 12, URL: "https://github.com/o/api/pull/12"}},
		{Alias: "docs"},
	}}
	if _, err := startAgent(inst, "main", "", ""); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	msgs, _ := Mail(root, "main")
	if len(msgs) != 1 {
		t.Fatalf("mailbox = %+v; want the default review brief", msgs)
	}
	for _, want := range []string{"https://github.com/o/api/pull/12", ".supatree/review.md", "review_post", `agent "pm"`} {
		if !strings.Contains(msgs[0].Text, want) {
			t.Errorf("review brief missing %q:\n%s", want, msgs[0].Text)
		}
	}
}

// The watcher holds launches back by taking only what it admits; the rest stay
// queued, in order, for the next tick (#1).
func TestTakeLaunchesLeavesTheRestInOrder(t *testing.T) {
	testutil.IsolateHome(t)
	for _, tree := range []string{"a", "b", "c", "d"} {
		if _, err := QueueLaunch(LaunchRequest{Tree: tree}); err != nil {
			t.Fatal(err)
		}
	}
	slots := 2
	took, err := TakeLaunches(func(LaunchRequest) bool {
		if slots == 0 {
			return false
		}
		slots--
		return true
	})
	if err != nil || len(took) != 2 || took[0].Tree != "a" || took[1].Tree != "b" {
		t.Fatalf("took %+v, %v; want a and b, oldest first", took, err)
	}
	if n := QueuedLaunches(); n != 2 {
		t.Errorf("QueuedLaunches = %d, want 2", n)
	}
	rest, _ := DrainLaunches()
	if len(rest) != 2 || rest[0].Tree != "c" || rest[1].Tree != "d" {
		t.Errorf("left %+v, want c then d", rest)
	}
	if took[0].ID == "" || took[0].ID == took[1].ID {
		t.Errorf("requests need distinct ids to be answered: %q, %q", took[0].ID, took[1].ID)
	}
}

// start_agent says what the launch came to, read from the watcher's answer.
func TestStartAgentReportsTheWatchersAnswer(t *testing.T) {
	testutil.IsolateHome(t)
	orig := launchWait
	launchWait = 5 * time.Second
	t.Cleanup(func() { launchWait = orig })
	inst := &Instance{Name: treeA, Root: t.TempDir(), Model: defaultModelKey}

	// Play the watcher: answer the request as soon as it is queued.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if reqs, _ := DrainLaunches(); len(reqs) == 1 {
				_ = WriteLaunchResult(LaunchResult{ID: reqs[0].ID, Tree: treeA, Agent: "main", Outcome: LaunchWoken})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	out, err := startAgent(inst, "", "re-review", "")
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "woken in place") {
		t.Errorf("report = %q, want it to say the running agent was woken", out)
	}
	if entries, _ := os.ReadDir(LaunchResultsDir()); len(entries) != 0 {
		t.Errorf("the answer was not consumed: %d left", len(entries))
	}
}

func TestParseOpenOutcome(t *testing.T) {
	out := "warning: could not pre-trust\n" + OpenOutcomePrefix + "woken\n"
	if got, ok := ParseOpenOutcome(out); !ok || got != LaunchWoken {
		t.Errorf("ParseOpenOutcome = %q, %v", got, ok)
	}
	if _, ok := ParseOpenOutcome("nothing here"); ok {
		t.Error("found an outcome in output without one")
	}
}

// The cap counts new processes only: waking a live agent and a duplicate
// request for one already admitted both go through on a full gate.
func TestLaunchGate(t *testing.T) {
	g := &LaunchGate{Max: 2, Busy: 1, Live: func(tab string) bool { return tab == "live" }}
	for _, tc := range []struct {
		tree string
		want bool
	}{
		{"a", true},    // 2/2 busy after this
		{"live", true}, // a wake: no new process
		{"b", false},   // full
		{"a", true},    // rides with the first a
		{"live", true}, // and so does a second wake
		{"c", false},   // still full
	} {
		if got := g.Admit(LaunchRequest{Tree: tc.tree, Agent: MainAgent}); got != tc.want {
			t.Errorf("Admit(%s) = %v, want %v (busy %d)", tc.tree, got, tc.want, g.Busy)
		}
	}
	unlimited := &LaunchGate{Busy: 50}
	if !unlimited.Admit(LaunchRequest{Tree: "z", Agent: MainAgent}) {
		t.Error("Max 0 is no cap")
	}
}

func TestResolveMaxRunningAgents(t *testing.T) {
	zero, five := 0, 5
	for _, tc := range []struct {
		set  *int
		want int
	}{{nil, DefaultMaxRunningAgents}, {&zero, 0}, {&five, 5}} {
		if got := (&Config{MaxRunningAgents: tc.set}).ResolveMaxRunningAgents(); got != tc.want {
			t.Errorf("ResolveMaxRunningAgents(%v) = %d, want %d", tc.set, got, tc.want)
		}
	}
}

// The launch queue is written by the PM's sandbox directly, so the watcher
// re-checks what QueueLaunch would have refused.
func TestTakeLaunchesDropsInvalidNames(t *testing.T) {
	testutil.IsolateHome(t)
	if _, err := QueueLaunch(LaunchRequest{Tree: treeA}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(LaunchPath(), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"tree":"canberra","agent":"../../x"}` + "\n" + `{"tree":"../up","agent":"main"}` + "\n")
	_ = f.Close()
	reqs, _ := DrainLaunches()
	if len(reqs) != 1 || reqs[0].Agent != MainAgent {
		t.Errorf("took %+v; want only the valid request", reqs)
	}
}

func TestOpenLockPathStaysInLocks(t *testing.T) {
	testutil.IsolateHome(t)
	for _, tab := range []string{"canberra:main", "../../etc/x", "a/b"} {
		p := openLockPath(tab)
		if filepath.Dir(p) != filepath.Join(StateRoot(), "locks") {
			t.Errorf("openLockPath(%q) = %q, outside the locks dir", tab, p)
		}
	}
}

// A new agent runs on the model asked for; an existing one keeps its own, and
// asking for another is refused rather than silently ignored.
func TestStartAgentModel(t *testing.T) {
	testutil.IsolateHome(t)
	noLaunchWait(t)
	root := t.TempDir()
	inst := &Instance{Name: treeA, Root: root, Model: defaultModelKey}

	if _, err := startAgent(inst, agentReviewer, "review it", "codex"); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	agents, _ := LoadAgents(root)
	if a := FindAgent(agents, agentReviewer); a == nil || a.Model != "codex" {
		t.Fatalf("reviewer = %+v; want it on codex", a)
	}
	if _, err := startAgent(inst, agentReviewer, "again", "codex"); err != nil {
		t.Errorf("same model again: %v", err)
	}
	if _, err := startAgent(inst, agentReviewer, "again", ""); err != nil {
		t.Errorf("no model named: %v", err)
	}
	if _, err := startAgent(inst, agentReviewer, "again", defaultModelKey); err == nil {
		t.Error("switched an existing codex agent to claude")
	}
}
