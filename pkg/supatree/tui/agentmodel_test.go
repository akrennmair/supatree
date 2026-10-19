package tui

import (
	"testing"
	"time"

	"github.com/panamafrancis/supatree/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/testutil"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

const (
	pickTree   = "perth"
	pickClaude = "claude"
	pickCodex  = "codex"
)

// agentPickerModel builds a sidebar over one tree on claude, with claude and codex
// installed (both run sh) and a third model whose binary is missing.
func agentPickerModel(t *testing.T) (*Model, *supatree.Instance) {
	t.Helper()
	testutil.IsolateHome(t)
	cfg := supatree.DefaultConfig()
	cfg.Models = map[string]config.Model{
		pickClaude: {Binary: "sh"},
		pickCodex:  {Binary: "sh"},
		"ghost":    {Binary: "no-such-agent-cli-xyz"},
	}
	inst := &supatree.Instance{Name: pickTree, Root: t.TempDir(), Model: pickClaude}
	m := New(cfg, zellij.Workspace{})
	m.insts = []*supatree.Instance{inst}
	return m, inst
}

func TestNewAgentPicksModelAfterName(t *testing.T) {
	m, _ := agentPickerModel(t)
	if cmd := m.chooseAgentModel(pickTree, "reviewer"); cmd != nil {
		t.Fatal("a new agent opened without offering a model")
	}
	if m.mode != modeNewAgentModel {
		t.Fatalf("mode = %v, want modeNewAgentModel", m.mode)
	}
	if got := m.models[m.modelCursor]; got.key != pickClaude || !got.isDefault {
		t.Errorf("cursor starts on %+v, want the tree's model", got)
	}
	m.updateInput(key("j"))
	if got := m.models[m.modelCursor].key; got != pickCodex {
		t.Errorf("after j: %q, want codex", got)
	}
	// ghost is listed but not installed, so the cursor does not land on it.
	m.updateInput(key("j"))
	if got := m.models[m.modelCursor].key; got != pickCodex {
		t.Errorf("cursor moved onto an uninstalled model: %q", got)
	}
	_, cmd := m.updateInput(key("enter"))
	if cmd == nil || m.mode != modeNormal {
		t.Errorf("enter: mode = %v, cmd = %v; want the agent opened", m.mode, cmd)
	}
}

func TestExistingAgentSkipsModelPicker(t *testing.T) {
	m, inst := agentPickerModel(t)
	if _, _, err := supatree.EnsureAgent(inst.Root, pickTree, "reviewer", pickCodex, time.Now()); err != nil {
		t.Fatal(err)
	}
	if cmd := m.chooseAgentModel(pickTree, "reviewer"); cmd == nil || m.mode == modeNewAgentModel {
		t.Error("an existing agent was offered a model it cannot switch to")
	}
}

func TestMainAgentSkipsModelPicker(t *testing.T) {
	m, _ := agentPickerModel(t)
	if cmd := m.chooseAgentModel(pickTree, supatree.MainAgent); cmd == nil || m.mode == modeNewAgentModel {
		t.Error("main was offered a model different from its tree's")
	}
}

func TestAgentRowShowsOtherModel(t *testing.T) {
	m, inst := agentPickerModel(t)
	for name, model := range map[string]string{"reviewer": pickCodex, "helper": pickClaude} {
		if _, _, err := supatree.EnsureAgent(inst.Root, pickTree, name, model, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	// No icons: only the agent off the tree's model is named.
	m.rebuildRows()
	got := map[string]string{}
	for _, r := range m.rows {
		if r.kind == rowAgent {
			got[r.label] = r.icon + "|" + r.model
		}
	}
	if got["reviewer"] != "|"+pickCodex || got["helper"] != "|" || got[supatree.MainAgent] != "|" {
		t.Errorf("agent rows = %v; want only reviewer tagged codex", got)
	}

	// With icons: every agent shows its model's, and none is named.
	m.stCfg.Models[pickClaude] = config.Model{Binary: "sh", Icon: "✻"}
	m.stCfg.Models[pickCodex] = config.Model{Binary: "sh", Icon: "◎"}
	m.rebuildRows()
	got = map[string]string{}
	for _, r := range m.rows {
		if r.kind == rowAgent {
			got[r.label] = r.icon + "|" + r.model
		}
	}
	if got["reviewer"] != "◎|" || got["helper"] != "✻|" || got[supatree.MainAgent] != "✻|" {
		t.Errorf("agent rows = %v; want each agent's icon", got)
	}
}
