package supatree

import (
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
)

// A CLI that picks its own session ID (codex) cannot be launched under the ID
// supatree registered for an agent. Its session is found after the fact
// instead: the one that started in the tree root after the agent's last fresh
// launch, and that no sibling agent already owns. Once found, its ID replaces
// the placeholder, and the agent resumes and is timed by it like any other.

// discoverSession finds the session a's last fresh launch started, without
// recording it. ok is false when a's CLI takes its ID at launch, when a
// already has a session, or when none can be found yet (codex writes its
// transcript only once the session starts).
func discoverSession(root string, a Agent, m config.Model) (string, bool) {
	if !sandbox.PicksOwnSessionID(m) || a.LaunchedAt.IsZero() || sandbox.SessionExistsFor(m, root, a.SessionID) {
		return "", false
	}
	agents, err := LoadAgents(root)
	if err != nil {
		return "", false
	}
	// Siblings' recorded sessions are theirs. And a session that started
	// after a sibling's later launch is more likely that launch's: without
	// the bound, an agent whose own session never appeared (quit before its
	// first turn) would take the next sibling's.
	claimed := map[string]bool{}
	var until time.Time
	for _, o := range agents {
		if o.Name == a.Name {
			continue
		}
		if o.SessionID != "" {
			claimed[o.SessionID] = true
		}
		if o.LaunchedAt.After(a.LaunchedAt) && (until.IsZero() || o.LaunchedAt.Before(until)) {
			until = o.LaunchedAt
		}
	}
	return sandbox.DiscoverSession(m, root, a.LaunchedAt, until, claimed)
}

// bindSession records the session a's last fresh launch started, when it
// can be found, and returns a with it.
func bindSession(root string, a Agent, m config.Model) (Agent, error) {
	sid, ok := discoverSession(root, a, m)
	if !ok {
		return a, nil
	}
	if err := updateAgent(root, a.Name, func(r *Agent) { r.SessionID = sid }); err != nil {
		return a, err
	}
	a.SessionID = sid
	return a, nil
}

// agentTranscript is the transcript a is writing to, or "" when none is known.
func agentTranscript(c *Config, root string, a Agent) string {
	var m config.Model // unresolvable: read as the default store
	if c != nil {
		if rm, err := c.Model(a.Model); err == nil {
			m = rm
		}
	}
	sid := a.SessionID
	if found, ok := discoverSession(root, a, m); ok {
		sid = found
	}
	return sandbox.SessionTranscriptFor(m, root, sid)
}
