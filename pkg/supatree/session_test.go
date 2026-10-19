package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/testutil"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

func codexConfig() *Config {
	return &Config{Models: map[string]config.Model{
		"codex": {
			NonoProfile:       "default",
			Binary:            "codex",
			ResumeSessionArgs: []string{"resume", "{session_id}"},
			SessionStore:      config.SessionStoreCodex,
		},
	}}
}

// writeCodexRollout writes a codex session transcript that started in cwd.
func writeCodexRollout(t *testing.T, id, cwd string, start time.Time) {
	t.Helper()
	dir := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", start.Format("2006/01/02"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	ts := start.UTC().Format(time.RFC3339Nano)
	path := filepath.Join(dir, "rollout-"+start.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	line := fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"timestamp":%q,"cwd":%q}}`+"\n", ts, id, ts, cwd)
	if err := os.WriteFile(path, []byte(line), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBindSessionLearnsCodexSessions(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	root := t.TempDir()
	cfg := codexConfig()
	m, _ := cfg.Model("codex")
	launch := time.Now()

	for _, name := range []string{MainAgent, "second"} {
		if _, _, err := EnsureAgent(root, "t", name, "codex", launch); err != nil {
			t.Fatal(err)
		}
		if err := updateAgent(root, name, func(a *Agent) { a.LaunchedAt = launch }); err != nil {
			t.Fatal(err)
		}
	}
	const sidMain = "0199a1b2-c3d4-7e5f-8a9b-000000000001"
	const sidSecond = "0199a1b2-c3d4-7e5f-8a9b-000000000002"

	agents, _ := LoadAgents(root)
	// Nothing written yet: the placeholder stays and nothing is busy.
	main, err := bindSession(root, *FindAgent(agents, MainAgent), m)
	if err != nil || main.SessionID == sidMain {
		t.Fatalf("bound before any session existed: %+v, %v", main, err)
	}
	if AgentBusy(cfg, root, main, time.Now()) {
		t.Error("busy with no transcript")
	}

	writeCodexRollout(t, sidMain, root, launch.Add(time.Second))
	// Busy is seen before the id is recorded, from the discovered session.
	if !AgentBusy(cfg, root, main, time.Now()) {
		t.Error("not busy though its session just wrote")
	}
	if main, err = bindSession(root, main, m); err != nil || main.SessionID != sidMain {
		t.Fatalf("main bound to %q, %v; want %q", main.SessionID, err, sidMain)
	}

	// The sibling launched at the same time must not take main's session.
	agents, _ = LoadAgents(root)
	second, _ := bindSession(root, *FindAgent(agents, "second"), m)
	if second.SessionID == sidMain {
		t.Fatal("second agent claimed main's session")
	}
	writeCodexRollout(t, sidSecond, root, launch.Add(2*time.Second))
	if second, _ = bindSession(root, second, m); second.SessionID != sidSecond {
		t.Errorf("second bound to %q, want %q", second.SessionID, sidSecond)
	}

	// Recorded, so a later open resumes it.
	agents, _ = LoadAgents(root)
	if got := FindAgent(agents, MainAgent).SessionID; got != sidMain {
		t.Errorf("agents.yml has %q for main, want %q", got, sidMain)
	}
}

// An agent whose own session never appeared does not take the session a
// sibling launched after it started.
func TestBindSessionLeavesLaterSiblingsSession(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	root := t.TempDir()
	m, _ := codexConfig().Model("codex")
	second := time.Now().Add(-time.Minute)
	first := second.Add(-time.Minute)
	for name, at := range map[string]time.Time{"early": first, "late": second} {
		if _, _, err := EnsureAgent(root, "t", name, "codex", at); err != nil {
			t.Fatal(err)
		}
		if err := updateAgent(root, name, func(a *Agent) { a.LaunchedAt = at }); err != nil {
			t.Fatal(err)
		}
	}
	const sidLate = "0199a1b2-c3d4-7e5f-8a9b-000000000003"
	writeCodexRollout(t, sidLate, root, second.Add(time.Second))

	agents, _ := LoadAgents(root)
	if early, _ := bindSession(root, *FindAgent(agents, "early"), m); early.SessionID == sidLate {
		t.Error("early agent took the session its later sibling started")
	}
	if late, _ := bindSession(root, *FindAgent(agents, "late"), m); late.SessionID != sidLate {
		t.Errorf("late agent bound %q, want %q", late.SessionID, sidLate)
	}
}

func TestBindLaterCodexAgentDoesNotTakeEarlierUnclaimedSession(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	root := t.TempDir()
	m, _ := codexConfig().Model("codex")
	firstLaunch := time.Now().Add(-time.Minute)
	secondLaunch := firstLaunch.Add(2 * time.Second)
	for name, at := range map[string]time.Time{"first": firstLaunch, "second": secondLaunch} {
		if _, _, err := EnsureAgent(root, "t", name, "codex", at); err != nil {
			t.Fatal(err)
		}
		if err := updateAgent(root, name, func(a *Agent) { a.LaunchedAt = at }); err != nil {
			t.Fatal(err)
		}
	}
	const sidFirst = "0199a1b2-c3d4-7e5f-8a9b-000000000004"
	writeCodexRollout(t, sidFirst, root, firstLaunch.Add(time.Second))
	agents, err := LoadAgents(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bindSession(root, *FindAgent(agents, "second"), m)
	if err != nil || second.SessionID == sidFirst {
		t.Fatalf("second bound to first's session: %+v, %v", second, err)
	}
	first, err := bindSession(root, *FindAgent(agents, "first"), m)
	if err != nil || first.SessionID != sidFirst {
		t.Errorf("first bound to %q, %v; want %q", first.SessionID, err, sidFirst)
	}
}

func TestMainAgentRejectsModelOverrideBeforeRegistration(t *testing.T) {
	inst := &Instance{Name: "t", Root: t.TempDir(), Model: defaultModelKey}
	if _, err := OpenRootAgent(inst, nil, zellij.Workspace{}, "20%", MainAgent, OpenOptions{Model: "codex"}, nil); err == nil {
		t.Error("open accepted a different model for main")
	}
	if _, err := startAgent(inst, MainAgent, "brief", "codex"); err == nil {
		t.Error("start_agent accepted a different model for main")
	}
	if agents, err := LoadAgents(inst.Root); err != nil || len(agents) != 0 {
		t.Errorf("rejected model created agents: %v, %v", agents, err)
	}
}

func TestCodexLaunchWaitsForEarlierSession(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	root := t.TempDir()
	cfg := codexConfig()
	firstLaunch := time.Now().Add(-time.Minute)
	for _, name := range []string{"first", "second"} {
		if _, _, err := EnsureAgent(root, "t", name, "codex", firstLaunch); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateAgent(root, "first", func(a *Agent) { a.LaunchedAt = firstLaunch }); err != nil {
		t.Fatal(err)
	}
	if err := bindEarlierCodexSessions(cfg, root, "second"); err == nil {
		t.Fatal("second launch was allowed before first wrote a session")
	}
	const sidFirst = "0199a1b2-c3d4-7e5f-8a9b-000000000005"
	writeCodexRollout(t, sidFirst, root, firstLaunch.Add(time.Second))
	if err := bindEarlierCodexSessions(cfg, root, "second"); err != nil {
		t.Fatalf("second launch stayed blocked after first wrote a session: %v", err)
	}
	agents, err := LoadAgents(root)
	if err != nil {
		t.Fatal(err)
	}
	first := FindAgent(agents, "first")
	if first.SessionID != sidFirst || !first.LaunchedAt.IsZero() {
		t.Errorf("first not bound before second launch: %+v", first)
	}
}

// Agents registered concurrently in one tree are all kept: agents.yml is
// read-modify-written under a lock.
func TestEnsureAgentConcurrent(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	const n = 12
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := EnsureAgent(root, "t", fmt.Sprintf("a%d", i), "claude", time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if agents, _ := LoadAgents(root); len(agents) != n {
		t.Errorf("%d agents recorded, want %d", len(agents), n)
	}
}
