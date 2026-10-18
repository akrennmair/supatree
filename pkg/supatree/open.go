package supatree

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// MainAgent is the primary agent every supatree has: the one `enter` opens, and
// the one a bare tab named after the tree belongs to.
const MainAgent = "main"

// TabName is the Zellij tab identity for an agent: "<name>" for the primary
// "main" agent, "<name>:<agent>" otherwise.
func TabName(tree, agent string) string {
	if agent == MainAgent {
		return tree
	}
	return tree + ":" + agent
}

// OpenOptions are the ways a root-agent open can differ.
type OpenOptions struct {
	Model string // model override
	// Wake is set for a launch nobody at the keyboard asked for (the watcher,
	// on a PM's start_agent). An agent that is already running is then woken
	// in place — WakePrompt typed into its pane when mail waits for it —
	// rather than focused: nobody is there to look at the tab, and the agent
	// would otherwise not read its brief until someone typed.
	Wake bool
}

// OpenRootAgent opens or resumes a named agent running at the supatree root
// (nono --allow the whole tree). Several agents share the root but resume
// independently via their session IDs. Warnings that do not stop the open go
// to startupW.
//
// The open runs under the tab's lock (openLockPath), so a sidebar enter and a
// watcher launch of the same agent cannot both find no tab and both start it;
// every open is recorded in the launch log.
func OpenRootAgent(inst *Instance, c *Config, ws zellij.Workspace, sidebarWidth, agentName string, opts OpenOptions, startupW io.Writer) (outcome LaunchOutcome, err error) {
	if agentName == "" {
		agentName = MainAgent
	}
	if err := ValidateAgentName(agentName); err != nil {
		return LaunchFailed, err
	}
	tab := TabName(inst.Name, agentName)
	lockErr := config.WithFileLock(openLockPath(tab), func() error {
		outcome, err = openRootAgentLocked(inst, c, ws, sidebarWidth, agentName, tab, opts, startupW)
		return nil
	})
	if lockErr != nil {
		return LaunchFailed, lockErr
	}
	what := string(outcome)
	if err != nil {
		what += ": " + err.Error()
	}
	logLaunch("agent "+tab, tab, what)
	return outcome, err
}

func openRootAgentLocked(inst *Instance, c *Config, ws zellij.Workspace, sidebarWidth, agentName, tab string, opts OpenOptions, startupW io.Writer) (outcome LaunchOutcome, err error) {
	if opts.Wake && zellij.TabLiveness("", tab) == zellij.LivenessAlive {
		return wakeAgent(inst.Root, tab, agentName), nil
	}
	// nono refuses a grant on a path that does not exist.
	if err := EnsureLayout(); err != nil {
		return LaunchFailed, err
	}
	// Best effort: an agent without a roster is told the address pattern.
	_ = PublishPMRoster()
	key := inst.Model
	if opts.Model != "" {
		key = opts.Model
	}
	agent, created, err := EnsureAgent(inst.Root, inst.Name, agentName, key, time.Now())
	if err != nil {
		return LaunchFailed, err
	}
	if err := checkAgentModel(agent, opts.Model); err != nil {
		return LaunchFailed, err
	}
	// A record this call made is kept only if the agent launches: one left
	// behind by a refused launch (nono rejecting its profile, say) would be
	// listed as if it were running. A record made earlier — start_agent
	// registers an agent before the watcher launches it — is not ours to drop.
	defer func() {
		if created && err != nil && !LaunchUncertain(err) {
			_, _ = forgetAgent(inst.Root, agentName)
		}
	}()
	// Several agents share this directory, which is what makes Claude's folder
	// trust never stick here (see sandbox.TrustDir). Seed it before launching;
	// failing to is a prompt the user answers, not a reason to refuse to open.
	if err := sandbox.TrustDir(inst.Root); err != nil && startupW != nil {
		_, _ = fmt.Fprintf(startupW, "warning: could not pre-trust %s with claude: %v\n", inst.Root, err)
	}
	// Resume only when this agent's session transcript actually exists; a freshly
	// created agent (or one whose id was never launched) starts a new session so
	// it never lands in another agent's chat.
	model, err := c.Model(agent.Model)
	if err != nil {
		return LaunchFailed, err
	}
	// A CLI that picked its own session ID last time is resumed by that ID
	// once it is known; until then the agent starts fresh again.
	if agent, err = bindSession(inst.Root, agent, model); err != nil {
		return LaunchFailed, err
	}
	resume := sandbox.SessionExistsFor(model, inst.Root, agent.SessionID)
	if !resume && sandbox.PicksOwnSessionID(model) {
		// The session this launch starts is found later by its start time.
		if err := updateAgent(inst.Root, agentName, func(r *Agent) { r.LaunchedAt = time.Now() }); err != nil {
			return LaunchFailed, err
		}
	}
	if err := NonoPreflight(model.NonoProfile); err != nil {
		return LaunchFailed, err
	}
	nonoArgs := sandbox.BuildGrantedAgentNonoArgs(model, TreeGrants(c, inst), inst.Root, agent.SessionID, agent.Address, resume)
	// Mail waiting means somebody briefed this agent before it was running —
	// the PM starting it, or a sibling. Without a first message it would sit
	// at an empty prompt until a human typed, and the brief would go unread.
	// Ignored when the tab is already live: OpenOrFocusTab only focuses it.
	if HasMail(inst.Root, agentName) {
		nonoArgs = sandbox.AppendPrompt(nonoArgs, model, KickoffPrompt)
	}
	env := withAgentExec(inst.AgentEnv(agentName))
	action, err := ws.OpenOrFocusTabAction(tab, inst.Root, sidebarWidth, nonoArgs, env)
	if err != nil {
		return LaunchFailed, &LaunchError{Err: err}
	}
	if action != zellij.TabFocused {
		return LaunchStarted, nil
	}
	// Focused: the agent was running after all (its liveness was unknown
	// above, or this is not a wake). Mail for it is still unread.
	if opts.Wake {
		return wakeAgent(inst.Root, tab, agentName), nil
	}
	return LaunchRunning, nil
}

// wakeAgent hands a running agent the mail waiting for it, by typing
// WakePrompt into its pane. With no mail there is nothing to wake it for, and
// with no agent pane to be sure of, nothing is typed: a mailed brief the agent
// reads on its next turn is better than text typed into the wrong terminal.
func wakeAgent(root, tab, agent string) LaunchOutcome {
	if !HasMail(root, agent) {
		return LaunchRunning
	}
	pane, ok, err := zellij.AgentPane("", tab)
	if err != nil || !ok {
		return LaunchRunning
	}
	if err := zellij.SubmitToPane("", pane.ID, WakePrompt); err != nil {
		return LaunchRunning
	}
	return LaunchWoken
}

// LaunchError is a failure in opening an agent's tab. Zellij may have opened
// it anyway — a command that timed out after doing its work — so the agent may
// be running, and its record must not be taken back.
type LaunchError struct{ Err error }

func (e *LaunchError) Error() string { return e.Err.Error() }
func (e *LaunchError) Unwrap() error { return e.Err }

// LaunchUncertain reports whether err came from opening the tab, after which
// the agent may or may not be running.
func LaunchUncertain(err error) bool {
	var le *LaunchError
	return errors.As(err, &le)
}

// requireMember resolves a member alias to a worktree that actually exists on
// disk — the shared precondition of every "open this member" entry point.
func requireMember(inst *Instance, alias string) (*Member, error) {
	m := inst.FindMember(alias)
	if m == nil {
		return nil, fmt.Errorf("repo %q is not a member of supatree %q", alias, inst.Name)
	}
	if !m.Exists {
		return nil, fmt.Errorf("member %q not created yet — run: supatree sync %s", alias, inst.Name)
	}
	return m, nil
}

// OpenMemberAgent opens an agent scoped to a single member repo (MemberGrants:
// that worktree and its clone's git dir). Member worktrees have unique paths, so directory-based
// resume works without session IDs.
func OpenMemberAgent(inst *Instance, c *Config, ws zellij.Workspace, sidebarWidth, alias, modelOverride string) (bool, error) {
	m, err := requireMember(inst, alias)
	if err != nil {
		return false, err
	}
	if err := EnsureLayout(); err != nil {
		return false, err
	}
	_ = PublishPMRoster()
	key := inst.Model
	if modelOverride != "" {
		key = modelOverride
	}
	model, err := c.Model(key)
	if err != nil {
		return false, err
	}
	if err := NonoPreflight(model.NonoProfile); err != nil {
		return false, err
	}
	nonoArgs := sandbox.BuildGrantedAgentNonoArgs(model, MemberGrants(c, inst, alias), m.Path, "", "", true)
	env := inst.AgentEnv(alias)
	env["SUPATREE_MEMBER"] = alias
	return ws.OpenOrFocusTab(TabName(inst.Name, alias), m.Path, sidebarWidth, nonoArgs, withAgentExec(env))
}

// OpenMemberShell opens a plain shell pane in the caller's current tab, rooted
// at a member repo's worktree. It is the "take me there" counterpart to
// OpenMemberAgent: no sandbox, no session, no tab — the member rows in the
// sidebar report state (branch, dirty, PR), so the obvious thing to do with one
// is stand in it.
func OpenMemberShell(inst *Instance, alias string) error {
	m, err := requireMember(inst, alias)
	if err != nil {
		return err
	}
	if !zellij.IsInZellij() {
		return fmt.Errorf("not inside zellij — cd %s", m.Path)
	}
	// Named for the location, matching the agent panes' "{repo}/{worktree}"
	// display-name convention.
	return zellij.NewPane(inst.Name+"/"+alias, m.Path)
}
