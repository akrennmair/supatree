package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/panamafrancis/supatree/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/testutil"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// The stamp moves when a tree's state appears and when a member's worktree
// lands, which are the two halves of a tree being created elsewhere.
func TestStateStampSeesTreesAndMembers(t *testing.T) {
	testutil.IsolateHome(t)
	worktree := filepath.Join(t.TempDir(), "repos", "api")
	insts := []*supatree.Instance{{Name: "quito", Members: []supatree.Member{{Alias: "api", Path: worktree}}}}

	before := stateStamp(insts)
	if err := os.MkdirAll(filepath.Join(supatree.TreesStateDir(), "quito"), 0o755); err != nil {
		t.Fatal(err)
	}
	afterTree := stateStamp(insts)
	if afterTree == before {
		t.Fatal("stamp unchanged by a new tree's state dir")
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if stateStamp(insts) == afterTree {
		t.Fatal("stamp unchanged by a member's worktree appearing")
	}
	settled := stateStamp(insts)
	if again := stateStamp(insts); again != settled {
		t.Fatal("stamp not stable while nothing changes")
	}
}

// A changed stamp reloads the rows, and an input mode defers the reload rather
// than losing it.
func TestStampMsgReloads(t *testing.T) {
	testutil.IsolateHome(t)
	m := New(supatree.DefaultConfig(), zellij.Workspace{})
	if m.stamp != stateStamp(m.insts) {
		t.Fatalf("New took no baseline stamp: %q", m.stamp)
	}
	m.Update(stampMsg{stamp: "a"})
	if m.stamp != "a" {
		t.Fatalf("stamp = %q, want a", m.stamp)
	}
	// A synthetic tree the next reload drops proves whether a reload ran.
	m.insts = []*supatree.Instance{{Name: "ghost"}}

	m.mode = modeNewTreeName
	m.Update(stampMsg{stamp: "b"})
	if m.stamp != "a" || m.instance("ghost") == nil {
		t.Fatal("stamp taken or rows reloaded while an input mode was open")
	}

	m.mode = modeNormal
	m.Update(stampMsg{stamp: "b"})
	if m.stamp != "b" {
		t.Fatalf("stamp = %q after a change, want b", m.stamp)
	}
	if m.instance("ghost") != nil {
		t.Fatal("a changed stamp did not reload the trees")
	}
}
