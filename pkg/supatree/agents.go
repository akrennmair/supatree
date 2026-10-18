package supatree

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// Agent is one named LLM agent attached to a supatree. Several agents share the
// supatree root directory but resume independently via SessionID.
type Agent struct {
	Name      string    `yaml:"name"`
	Model     string    `yaml:"model"`
	SessionID string    `yaml:"session_id,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`
	// Address is how sibling agents reach this one on their CLI's message bus.
	// Persisted rather than derived at read time so a supatree renamed after an
	// agent was launched still reports the name that agent is actually running
	// under — the launched process cannot be renamed.
	Address string `yaml:"address,omitempty"`
	// LaunchedAt is when the agent was last launched into a fresh session by
	// a CLI that picks its own session ID (sandbox.PicksOwnSessionID). Until
	// that session is found and its ID recorded (bindSession), SessionID is a
	// placeholder nothing was launched under.
	LaunchedAt time.Time `yaml:"launched_at,omitempty"`
}

type agentsFile struct {
	Agents []Agent `yaml:"agents"`
}

// LoadAgents reads .supatree/agents.yml, returning an empty list if absent.
func LoadAgents(root string) ([]Agent, error) {
	data, err := os.ReadFile(AgentsPath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agents: %w", err)
	}
	var f agentsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse agents: %w", err)
	}
	return f.Agents, nil
}

// saveAgents writes .supatree/agents.yml atomically.
func saveAgents(root string, agents []Agent) error {
	if err := os.MkdirAll(StateDir(root), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := yaml.Marshal(agentsFile{Agents: agents})
	if err != nil {
		return fmt.Errorf("marshal agents: %w", err)
	}
	path := AgentsPath(root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write agents: %w", err)
	}
	return os.Rename(tmp, path)
}

// checkAgentModel refuses a model asked for an agent that is already
// registered on another: its session belongs to that CLI, so the request
// could only be honoured by silently dropping it. An empty model asks for
// nothing.
func checkAgentModel(a Agent, model string) error {
	if model == "" || model == a.Model {
		return nil
	}
	return fmt.Errorf("agent %q already runs on %s, not %s — pick a new agent name to use %s", a.Name, a.Model, model, model)
}

// checkMainModel keeps the main agent on the model recorded for its tree,
// including when main has not yet been registered in agents.yml.
func checkMainModel(agent, requested, treeModel string) error {
	if agent == MainAgent && requested != "" && requested != treeModel {
		return fmt.Errorf("main agent runs on the tree's model %q, not %q", treeModel, requested)
	}
	return nil
}

// withAgentsLock runs fn holding the tree's agents.yml lock. Every
// read-modify-write of agents.yml goes through it: the watcher, the sidebars
// and the PM open agents of one tree concurrently, each under only its own
// tab's lock, and an unlocked write drops whatever another made in between.
func withAgentsLock(root string, fn func() error) error {
	if err := os.MkdirAll(StateDir(root), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	return config.WithFileLock(AgentsPath(root)+".lock", fn)
}

// updateAgent applies fn to the named agent's record and saves it.
func updateAgent(root, name string, fn func(*Agent)) error {
	return withAgentsLock(root, func() error {
		agents, err := LoadAgents(root)
		if err != nil {
			return err
		}
		a := FindAgent(agents, name)
		if a == nil {
			return fmt.Errorf("no agent named %q", name)
		}
		fn(a)
		return saveAgents(root, agents)
	})
}

// FindAgent returns the agent with the given name, or nil.
func FindAgent(agents []Agent, name string) *Agent {
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

// EnsureAgent returns the named agent for a supatree, creating and persisting it
// with a fresh session ID (via now for the timestamp) and a bus address if it
// does not yet exist. The bool result reports whether the agent was newly
// created.
func EnsureAgent(root, tree, name, model string, now time.Time) (Agent, bool, error) {
	// Agent names become part of a Zellij tab identity ("<tree>:<agent>") that is
	// spliced into layout files, so restrict them to the safe charset up front.
	if err := git.ValidateName(name, nil); err != nil {
		return Agent{}, false, fmt.Errorf("invalid agent name %q: %w", name, err)
	}
	var (
		out     Agent
		created bool
	)
	err := withAgentsLock(root, func() error {
		agents, err := LoadAgents(root)
		if err != nil {
			return err
		}
		if a := FindAgent(agents, name); a != nil {
			out = *a
			return nil
		}
		sid, err := newSessionID()
		if err != nil {
			return err
		}
		out = Agent{Name: name, Model: model, SessionID: sid, CreatedAt: now, Address: AgentAddress(tree, name)}
		created = true
		return saveAgents(root, append(agents, out))
	})
	if err != nil {
		return Agent{}, false, err
	}
	return out, created, nil
}

// ErrMainAgent is returned when removing a tree's main agent: it is the tree's
// own tab, and goes away with the tree rather than on its own.
var ErrMainAgent = errors.New("the main agent cannot be removed — delete the supatree instead")

// RemoveAgent drops a named agent from a supatree's agents.yml, returning the
// record it removed. Its session is forgotten with it, so opening an agent of
// the same name later starts a fresh conversation. Close its tab with
// CloseAgentTab afterwards.
func RemoveAgent(root, name string) (Agent, error) {
	if name == MainAgent {
		return Agent{}, ErrMainAgent
	}
	return forgetAgent(root, name)
}

// forgetAgent drops an agent's record from agents.yml, main included. Removing
// an agent on purpose goes through RemoveAgent; this is also how a launch that
// failed takes back the record it made.
func forgetAgent(root, name string) (Agent, error) {
	var gone Agent
	err := withAgentsLock(root, func() error {
		agents, err := LoadAgents(root)
		if err != nil {
			return err
		}
		for i, a := range agents {
			if a.Name != name {
				continue
			}
			gone = a
			return saveAgents(root, append(agents[:i:i], agents[i+1:]...))
		}
		return fmt.Errorf("no agent named %q", name)
	})
	if err != nil {
		return Agent{}, err
	}
	return gone, nil
}

// CloseAgentTab closes an agent's tab in every supatree session, which stops
// the agent. Best effort, like ClosePMTab: it runs after RemoveAgent, because
// the caller may be the sidebar inside that tab.
func CloseAgentTab(ws zellij.Workspace, tree, agent string) []string {
	return closeTab(ws, TabName(tree, agent), TabName(tree, agent))
}

// newSessionID returns a random RFC 4122 version-4 UUID string.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// AgentAddress is the name an agent is launched under on its CLI's message bus.
//
// Deliberately not the "<tree>:<agent>" Zellij tab identity: a colon in a bus
// name is asking for trouble, and the two namespaces should be free to diverge.
// The "st-" prefix keeps supatree agents distinguishable from the sessions a
// bare `claude` run names after its own directory.
//
// Verified 2026-09-20 (claude 2.1.261): an explicitly named session appears on
// the bus under exactly this string, with none of the two-character suffix the
// bus appends to *directory-derived* names. So addresses match exactly; there
// is no prefix correlation to do.
func AgentAddress(tree, agent string) string {
	return "st-" + tree + "-" + agent
}
