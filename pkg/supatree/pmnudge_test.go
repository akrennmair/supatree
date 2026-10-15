package supatree

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// panesFixture is trimmed from real `zellij action list-panes --json --tab`
// output (zellij 0.45.1).
const panesFixture = `[
 {"id":0,"is_plugin":true,"title":"(.) - zellij:link","exited":false,"tab_name":"supatree"},
 {"id":0,"is_plugin":false,"title":"sidebar","exited":false,"tab_name":"supatree"},
 {"id":2,"is_plugin":false,"title":"sidebar","exited":false,"tab_name":"supatree-pm"},
 {"id":3,"is_plugin":false,"title":"supatree-pm","exited":false,"tab_name":"supatree-pm"},
 {"id":6,"is_plugin":false,"title":"sidebar","exited":false,"tab_name":"supatree-pm:research"},
 {"id":7,"is_plugin":false,"title":"supatree-pm:research","exited":true,"tab_name":"supatree-pm:research"},
 {"id":8,"is_plugin":false,"title":"sidebar","exited":false,"tab_name":"supatree-pm:ops"},
 {"id":9,"is_plugin":false,"title":"✳ Claude Code","exited":false,"tab_name":"supatree-pm:ops"}
]`

// The agent panes panesFixture has for the top PM and for a PM named "ops".
const (
	topPMPane = "terminal_3"
	opsPMPane = "terminal_9"
)

func TestPMPaneID(t *testing.T) {
	panes, err := parsePanes([]byte(panesFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tab, want string
		ok        bool
	}{
		{"supatree-pm", topPMPane, true},
		{"supatree-pm:research", "", false}, // its agent pane has exited
		{"supatree-pm:ops", opsPMPane, true},
		{"supatree", "", false}, // a sidebar and a plugin, no agent
		{"supatree-pm:gone", "", false},
	} {
		got, ok := pmPaneID(panes, tc.tab)
		if got != tc.want || ok != tc.ok {
			t.Errorf("pmPaneID(%q) = %q, %v; want %q, %v", tc.tab, got, ok, tc.want, tc.ok)
		}
	}
}

func TestShouldNudge(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	reqs := []Request{{At: t0}, {At: t0.Add(time.Minute)}}
	for _, tc := range []struct {
		name    string
		state   nudgeState
		pending []Request
		want    bool
	}{
		{"no mail", nudgeState{}, nil, false},
		{"first mail", nudgeState{}, reqs, true},
		{"already nudged", nudgeState{DefaultPMName: t0.Add(time.Minute)}, reqs, false},
		{"newer mail", nudgeState{DefaultPMName: t0}, reqs, true},
	} {
		if got := shouldNudge(tc.state, DefaultPMName, tc.pending); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// stubNudge replaces the zellij seams and records every action run.
func stubNudge(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	oldRun, oldIn, oldSleep := runZellijAction, inZellij, nudgeSleep
	runZellijAction = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] == "list-panes" {
			return []byte(panesFixture), nil
		}
		return nil, nil
	}
	inZellij = func() bool { return true }
	nudgeSleep = func(time.Duration) {}
	t.Cleanup(func() { runZellijAction, inZellij, nudgeSleep = oldRun, oldIn, oldSleep })
	return &calls
}

func TestNudgePMsOncePerMail(t *testing.T) {
	testutil.IsolateHome(t)
	calls := stubNudge(t)
	top := PM{Name: DefaultPMName}
	t0 := time.Now().UTC()
	pending := map[string][]Request{DefaultPMName: {{At: t0, Text: "hi"}}}

	if err := NudgePMs([]PM{top}, pending); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"list-panes", "--json", "--tab"},
		{"write-chars", "--pane-id", topPMPane, PMNudgeText},
		{"send-keys", "--pane-id", topPMPane, "Enter"},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %v, want %v", *calls, want)
	}

	*calls = nil
	if err := NudgePMs([]PM{top}, pending); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("second round for the same mail ran %v", *calls)
	}

	pending[DefaultPMName] = append(pending[DefaultPMName], Request{At: t0.Add(time.Second), Text: "more"})
	if err := NudgePMs([]PM{top}, pending); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 {
		t.Fatalf("new mail ran %v, want a nudge", *calls)
	}
}

// A PM with no live agent pane in this session is left for a sidebar in the
// session that has one: nothing is typed and its state does not advance.
func TestNudgePMsSkipsMissingPane(t *testing.T) {
	testutil.IsolateHome(t)
	calls := stubNudge(t)
	pm := PM{Name: "gone"}
	pending := map[string][]Request{pm.Name: {{At: time.Now().UTC(), Text: "hi"}}}
	if err := NudgePMs([]PM{pm}, pending); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c[0] != "list-panes" {
			t.Errorf("ran %s", strings.Join(c, " "))
		}
	}
	state := nudgeState{}
	_ = readJSON(PMNudgeStatePath(), &state)
	if !state[pm.Name].IsZero() {
		t.Errorf("state advanced to %v, so the nudge would never be retried", state[pm.Name])
	}
}

// With several PMs, mail reaches only the pane of the PM it is for: requests go
// through the real queue and PendingRequests' routing, exactly as the sidebar
// gathers them, and only zellij is stubbed. The fixture has a live agent pane
// for the top PM (terminal_3) and for "ops" (terminal_9).
func TestNudgePMsOnlyTheAddressee(t *testing.T) {
	testutil.IsolateHome(t)
	calls := stubNudge(t)
	if _, err := AddPM("ops", ""); err != nil {
		t.Fatal(err)
	}
	pms, err := LoadPMs()
	if err != nil {
		t.Fatal(err)
	}

	// nudged runs one sidebar round and returns the panes typed into.
	nudged := func() []string {
		t.Helper()
		pending := map[string][]Request{}
		for _, p := range pms {
			reqs, _, err := PendingRequests(p.Name)
			if err != nil {
				t.Fatal(err)
			}
			pending[p.Name] = reqs
		}
		*calls = nil
		if err := NudgePMs(pms, pending); err != nil {
			t.Fatal(err)
		}
		var panes []string
		for _, c := range *calls {
			if c[0] == "write-chars" {
				panes = append(panes, c[2])
			}
		}
		return panes
	}
	queue := func(to, text string) {
		t.Helper()
		if err := AppendRequest(Request{From: "test", To: to, Text: text}); err != nil {
			t.Fatal(err)
		}
	}

	queue("ops", "for ops")
	if got := nudged(); !reflect.DeepEqual(got, []string{opsPMPane}) {
		t.Fatalf("mail for ops nudged %v, want only ops' pane", got)
	}
	queue("", "for whoever is on top")
	if got := nudged(); !reflect.DeepEqual(got, []string{topPMPane}) {
		t.Fatalf("unaddressed mail nudged %v, want only the top PM's pane", got)
	}
	queue(DefaultPMName, "for the top PM by name")
	if got := nudged(); !reflect.DeepEqual(got, []string{topPMPane}) {
		t.Fatalf("mail for the top PM nudged %v, want only its pane", got)
	}
	if got := nudged(); len(got) != 0 {
		t.Fatalf("a round with no new mail nudged %v", got)
	}
	queue("ops", "ops again")
	queue("", "top again")
	if got := nudged(); !reflect.DeepEqual(got, []string{topPMPane, opsPMPane}) {
		t.Fatalf("mail for both nudged %v, want each pane once", got)
	}
}
