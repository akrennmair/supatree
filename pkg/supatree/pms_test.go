package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// An install from before PMs were plural has one PM, and it must come back as
// the same agent: same tab, address and offset, and resuming the conversation
// --continue would have picked — the newest transcript in the PM's home.
func TestLoadPMsMigratesTheSinglePM(t *testing.T) {
	testutil.IsolateHome(t)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude", "projects", encodeProjectPath(PMDir()))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old, newest := filepath.Join(dir, "old.jsonl"), filepath.Join(dir, "newest.jsonl")
	for _, p := range []string{old, newest} {
		if err := os.WriteFile(p, []byte("{}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	pms, err := LoadPMs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pms) != 1 {
		t.Fatalf("pms = %+v, want the default PM alone", pms)
	}
	p := pms[0]
	if p.Name != DefaultPMName || p.Tab() != PMTab || p.Address() != PMAddress || p.AgentID() != PMAgentName || p.OffsetPath() != PMOffsetPath() {
		t.Errorf("default PM = %+v (tab %s, address %s), want the old identity", p, p.Tab(), p.Address())
	}
	if p.SessionID != "newest" {
		t.Errorf("session = %q, want the newest transcript pinned", p.SessionID)
	}
	// Persisted, so the pin does not drift once a second PM writes transcripts.
	if again, _ := LoadPMs(); len(again) != 1 || again[0].SessionID != "newest" {
		t.Errorf("reload = %+v, want the same pinned PM", again)
	}
}

func TestAddAndRemovePMs(t *testing.T) {
	testutil.IsolateHome(t)
	p, err := AddPM("research", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Tab() != PMTab+":research" || p.Address() != PMAddress+"-research" || p.AgentID() != "pm-research" || p.SessionID == "" {
		t.Errorf("new PM = %+v, want its own tab, address, agent name and session", p)
	}
	pms, _ := LoadPMs()
	if len(pms) != 2 || pms[0].Name != DefaultPMName || pms[1].Name != "research" {
		t.Fatalf("pms = %+v, want the default on top and the new one below", pms)
	}
	if pms[0].SessionID == pms[1].SessionID {
		t.Error("two PMs share a session — they would resume one conversation")
	}
	if _, err := AddPM("research", ""); err == nil {
		t.Error("a duplicate PM name was accepted")
	}
	if _, err := AddPM("../x", ""); err == nil {
		t.Error("a PM name that is not a path segment was accepted")
	}

	// Reading a PM's inbox leaves a lock beside it; removing the PM takes both.
	if _, err := Mail(PMDir(), PMAgentName); err != nil {
		t.Fatal(err)
	}
	if _, err := RemovePM(DefaultPMName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mailLockPath(PMDir(), PMAgentName)); !os.IsNotExist(err) {
		t.Errorf("removed PM's mail lock still exists (stat err = %v)", err)
	}
	if top, _ := ResolvePM(""); top.Name != "research" {
		t.Errorf("top after removing the default = %q, want research promoted", top.Name)
	}
	if _, err := RemovePM("research"); err == nil || !strings.Contains(err.Error(), "only PM") {
		t.Errorf("removing the last PM: err = %v, want a refusal", err)
	}
	if _, err := RemovePM("nobody"); err == nil {
		t.Error("removing an unknown PM succeeded")
	}
}

// Each PM reads its own requests; unaddressed ones — and those for a PM that is
// gone — go to the top PM alone, so two PMs never both act on one.
func TestRequestsRouteToOnePM(t *testing.T) {
	testutil.IsolateHome(t)
	if err := AppendRequest(Request{From: fromCLI, Text: "before research existed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddPM("research", ""); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Request{
		{From: fromCLI, Text: "for anyone"},
		{From: fromCLI, To: "research", Text: "for research"},
		{From: fromCLI, To: "gone", Text: "for a removed PM"},
	} {
		if err := AppendRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	top, _, _ := PendingRequests("")
	if got := texts(top); got != "before research existed|for anyone|for a removed PM" {
		t.Errorf("top PM sees %q", got)
	}
	res, off, _ := PendingRequests("research")
	if got := texts(res); got != "for research" {
		t.Errorf("research sees %q, want only its own (and nothing from before it existed)", got)
	}
	if _, _, err := PendingRequests("nobody"); err == nil {
		t.Error("an unknown PM read the queue")
	}

	// Research reading its own does not mark the top PM's read.
	if err := CommitRequests("research", off); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := PendingRequests(""); len(again) != 3 {
		t.Errorf("top PM lost requests to another PM's commit: %q", texts(again))
	}

	// Removing the top PM hands its unread unaddressed requests to the next.
	if _, err := RemovePM(DefaultPMName); err != nil {
		t.Fatal(err)
	}
	promoted, _, _ := PendingRequests("")
	if !strings.Contains(texts(promoted), "for anyone") {
		t.Errorf("promoted PM sees %q, want the old top's backlog", texts(promoted))
	}
}

const fromCLI = "cli"

func texts(reqs []Request) string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Text
	}
	return strings.Join(out, "|")
}

// Mail a tree agent leaves for "pm-<name>" reaches that PM; plain "pm" reaches
// the top PM, as it always did.
func TestForwardPMMailAddressesThePM(t *testing.T) {
	testutil.IsolateHome(t)
	if _, err := AddPM("research", ""); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	inst := &Instance{Name: treeA, Root: root}
	if err := Deliver(root, "pm-research", "main", "done with the spike"); err != nil {
		t.Fatal(err)
	}
	if err := Deliver(root, PMAgentName, "main", "blocked on review"); err != nil {
		t.Fatal(err)
	}
	if n, err := ForwardPMMail([]*Instance{inst}); err != nil || n != 2 {
		t.Fatalf("ForwardPMMail = %d, %v; want 2, nil", n, err)
	}
	if res, _, _ := PendingRequests("research"); texts(res) != "done with the spike" {
		t.Errorf("research sees %q", texts(res))
	}
	if top, _, _ := PendingRequests(""); texts(top) != "blocked on review" {
		t.Errorf("top PM sees %q", texts(top))
	}
}

// The marker is taken exactly once, so only the first sidebar of a new session
// opens the PM.
func TestColdStartMarkerIsTakenOnce(t *testing.T) {
	testutil.IsolateHome(t)
	if TakeColdStart(sessionMain) {
		t.Fatal("took a marker nobody set")
	}
	if err := MarkColdStart(sessionMain); err != nil {
		t.Fatal(err)
	}
	if !TakeColdStart(sessionMain) {
		t.Fatal("marker not taken")
	}
	if TakeColdStart(sessionMain) {
		t.Error("marker taken twice — every sidebar would open the PM")
	}
	if TakeColdStart("") {
		t.Error("an empty session took a marker")
	}
}

// A PM the registry shows running is found by name, so OpenPM focuses or
// refuses instead of starting it twice (#1).
func TestRunningPM(t *testing.T) {
	testutil.IsolateHome(t)
	orig := agentExecs
	agentExecs = allAgentExecs
	t.Cleanup(func() { agentExecs = orig })

	registerProc(ProcRecord{PID: os.Getpid(), PM: pmReviews, Session: "st-main"})
	if r, ok := runningPM(PM{Name: pmReviews}); !ok || r.PID != os.Getpid() {
		t.Errorf("runningPM(reviews) = %+v, %v; want the recorded process", r, ok)
	}
	if _, ok := runningPM(PM{Name: DefaultPMName}); ok {
		t.Error("another PM's process was taken for the top PM's")
	}
}

func TestLaunchCaller(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = []string{"supatree", "open", treeLima, "--agent", MainAgent, "--background"}
	if got := launchCaller(); got != "watcher" {
		t.Errorf("background open: %q, want watcher", got)
	}
	os.Args = []string{"supatree", "pm", pmReviews}
	if got := launchCaller(); got != "cli(supatree pm reviews)" {
		t.Errorf("pm: %q", got)
	}
	t.Setenv("SUPATREE_SIDEBAR", "1")
	if got := launchCaller(); got != "sidebar" {
		t.Errorf("sidebar: %q", got)
	}
}

// Tree agents cannot read the registry, so every change to it republishes the
// roster they can: names and addresses, top first, and no session IDs.
func TestRosterFollowsTheRegistry(t *testing.T) {
	testutil.IsolateHome(t)
	if _, err := AddPM("roshi", ""); err != nil {
		t.Fatal(err)
	}
	roster, err := ReadPMRoster()
	if err != nil {
		t.Fatal(err)
	}
	want := []RosterPM{{Name: DefaultPMName, Agent: PMAgentName, Top: true}, {Name: "roshi", Agent: PMAgentName + "-roshi"}}
	if len(roster) != len(want) || roster[0] != want[0] || roster[1] != want[1] {
		t.Fatalf("roster = %+v, want %+v", roster, want)
	}
	data, _ := os.ReadFile(PMRosterPath())
	if strings.Contains(string(data), "session") {
		t.Errorf("roster leaks session state: %s", data)
	}
	if !strings.Contains(pmSection(), "message_agent pm-roshi") {
		t.Errorf("agents' PM section does not address roshi:\n%s", pmSection())
	}

	if _, err := RemovePM(DefaultPMName); err != nil {
		t.Fatal(err)
	}
	roster, _ = ReadPMRoster()
	if len(roster) != 1 || roster[0].Name != "roshi" || !roster[0].Top {
		t.Errorf("after removing the top PM, roster = %+v; want roshi alone, on top", roster)
	}
}

// Without a roster the agents listing still says how PMs are addressed.
func TestPMSectionWithoutARoster(t *testing.T) {
	testutil.IsolateHome(t)
	if got := pmSection(); !strings.Contains(got, "`pm-<name>`") {
		t.Errorf("pmSection() = %q; want the address pattern", got)
	}
}

// Tree agents read the roster and nothing else of the PMs': not the registry,
// and not the roster for writing, or one could redirect another's reports.
func TestTreeAgentsReadOnlyTheRoster(t *testing.T) {
	testutil.IsolateHome(t)
	inst := &Instance{Name: "t", Root: t.TempDir(), Members: []Member{{Alias: "a", Path: t.TempDir()}}}
	for name, g := range map[string]struct{ allow, read []string }{
		"tree":   {TreeGrants(&Config{}, inst).Allow, TreeGrants(&Config{}, inst).Read},
		"member": {MemberGrants(&Config{}, inst, "a").Allow, MemberGrants(&Config{}, inst, "a").Read},
	} {
		if !strings.Contains(strings.Join(g.read, " "), RosterDir()) {
			t.Errorf("%s: Read = %v, want the roster", name, g.read)
		}
		for _, p := range append(append([]string{}, g.allow...), g.read...) {
			if p == PMDir() || p == StateRoot() {
				t.Errorf("%s: granted %q — the PM registry is not a tree agent's", name, p)
			}
		}
		for _, p := range g.allow {
			if p == RosterDir() {
				t.Errorf("%s: the roster is writable", name)
			}
		}
	}
}

// A launch's refresh writes what the registry says now.
func TestPublishPMRoster(t *testing.T) {
	testutil.IsolateHome(t)
	if _, err := AddPM("roshi", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(PMRosterPath()); err != nil {
		t.Fatal(err)
	}
	if err := PublishPMRoster(); err != nil {
		t.Fatal(err)
	}
	if roster, err := ReadPMRoster(); err != nil || len(roster) != 2 {
		t.Errorf("roster = %+v, %v; want both PMs", roster, err)
	}
}

// A registry from before #23 sat in the PMs' writable home. It is moved out
// once, keeping what `pm new` would have written and nothing else, and what a
// PM writes there afterwards is never read.
func TestLegacyPMRegistryIsMigratedOnce(t *testing.T) {
	testutil.IsolateHome(t)
	const sid = "0f8fad5b-d9cb-469f-a165-70867728950e"
	writeFile(t, legacyPMsPath(), `pms:
  - name: scout
    session_id: not-a-session
    model: loose
    config_at: deadbeef
  - name: pm
    session_id: `+sid+`
  - name: ../escape
    session_id: `+sid+`
  - name: scout
    session_id: `+sid+`
`)
	pms, err := LoadPMs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pms) != 2 || pms[0].Name != DefaultPMName || pms[1].Name != "scout" {
		t.Fatalf("pms = %+v, want pm then scout", pms)
	}
	if pms[0].SessionID != sid {
		t.Errorf("the top PM's session = %q, want it kept", pms[0].SessionID)
	}
	if pms[1].SessionID == "not-a-session" || !sessionIDPattern.MatchString(pms[1].SessionID) {
		t.Errorf("scout's session = %q, want a fresh one in place of what the PM wrote", pms[1].SessionID)
	}
	if pms[1].Model != "" || pms[1].ConfigAt != "" {
		t.Errorf("scout kept model %q, config_at %q from a PM-writable file", pms[1].Model, pms[1].ConfigAt)
	}
	if _, err := os.Stat(legacyPMsPath()); !os.IsNotExist(err) {
		t.Errorf("the old registry is still there: %v", err)
	}
	if _, ok := LegacyPMRegistry(); ok {
		t.Error("doctor would flag a registry that was just migrated")
	}

	// A PM writing the old file again changes nothing, and doctor says so.
	writeFile(t, legacyPMsPath(), "pms:\n  - name: usurper\n    session_id: "+sid+"\n")
	again, err := LoadPMs()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[0].Name != DefaultPMName {
		t.Errorf("after a PM rewrote the old file, pms = %+v", again)
	}
	if _, ok := LegacyPMRegistry(); !ok {
		t.Error("doctor would not flag an old registry written after the migration")
	}
}

// Inside the PM's sandbox the registry cannot be read; request routing works
// from the roster instead.
func TestRoutingPMsFallsBackToTheRoster(t *testing.T) {
	testutil.IsolateHome(t)
	if _, err := AddPM("roshi", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(PMsPath(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(PMsPath(), 0644) })
	if _, err := os.ReadFile(PMsPath()); err == nil {
		t.Skip("running as a user that can read a mode-0 file")
	}
	pms, err := RoutingPMs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pms) != 2 || pms[0].Name != DefaultPMName || pms[1].Name != "roshi" {
		t.Errorf("pms = %+v, want pm then roshi from the roster", pms)
	}
	if p, err := ResolvePM("roshi"); err != nil || p.OffsetPath() != filepath.Join(PMDir(), "requests.roshi.offset") {
		t.Errorf("ResolvePM(roshi) = %+v, %v", p, err)
	}
}

// allAgentExecs stands in for agentExecs where a test's pids are not real
// agent-execs: every pid asked about counts as one.
func allAgentExecs(pids []int) map[int]bool {
	out := make(map[int]bool, len(pids))
	for _, pid := range pids {
		out[pid] = true
	}
	return out
}
