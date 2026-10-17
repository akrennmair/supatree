package supatree

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// The PM's grant is the security boundary, so it gets asserted rather than
// assumed: it may write supatree's own state and must never reach a member
// repo's working tree.
func TestPMGrants(t *testing.T) {
	testutil.IsolateHome(t)
	cfg := &Config{Stacks: []Stack{{Alias: "s", Path: "/elsewhere/stacks/s"}}}
	insts := []*Instance{
		{Name: treeA, Root: "/trees/canberra"},
		{Name: treeB, Root: "/trees/darwin"},
	}
	g := PMGrants(cfg)

	allow := strings.Join(g.Allow, " ")
	for _, want := range []string{PMDir(), TreesStateDir(), OutboxDir(), "/elsewhere/stacks/s"} {
		if !strings.Contains(allow, want) {
			t.Errorf("Allow = %v, want it to include %q", g.Allow, want)
		}
	}
	// No tree root, and nothing under one, is writable: tree state is granted
	// where it really lives, outside every tree.
	for _, w := range g.Allow {
		for _, inst := range insts {
			if w == inst.Root || strings.HasPrefix(w, inst.Root+"/") {
				t.Errorf("Allow includes %q — the PM must never write inside a tree", w)
			}
		}
		if w == StateRoot() || w == CacheDir() {
			t.Errorf("Allow includes %q — that would hand the PM the watcher's own files", w)
		}
	}
	if len(g.Read) == 0 {
		t.Error("Read is empty — the PM cannot see the trees at all")
	}
	// The PM reads the roster, and can write neither it nor the registry
	// it is published from (#23).
	if !strings.Contains(strings.Join(g.Read, " "), RosterDir()) {
		t.Errorf("Read = %v, want the roster %s", g.Read, RosterDir())
	}
	for _, w := range g.Allow {
		for _, p := range []string{PMsPath(), PMRosterPath()} {
			if p == w || strings.HasPrefix(p, w+"/") {
				t.Errorf("Allow includes %q, which covers %s", w, p)
			}
		}
	}
	// Every cached clone's git dir is read — not only those of trees that
	// existed at launch, or the grant goes stale under a running PM and
	// refreshing it means relaunching (#3) — and never a clone's working tree,
	// which holds the files copy_files hands out.
	for _, key := range []string{"github.com/o/used", "github.com/o/unused"} {
		writeFile(t, filepath.Join(ReposDir(), key, ".git", "HEAD"), "ref: refs/heads/main\n")
		writeFile(t, filepath.Join(ReposDir(), key, ".env"), "SECRET=1\n")
	}
	g = PMGrants(cfg)
	read := strings.Join(g.Read, " ")
	for _, key := range []string{"github.com/o/used", "github.com/o/unused"} {
		if !strings.Contains(read, filepath.Join(ReposDir(), key, ".git")) {
			t.Errorf("Read = %v, want %s's git dir", g.Read, key)
		}
	}
	for _, r := range g.Read {
		if r == ReposDir() || (strings.HasPrefix(r, ReposDir()) && !strings.HasSuffix(r, "/.git")) {
			t.Errorf("Read includes %q — a clone's working tree, or the whole cache", r)
		}
	}
	for _, w := range g.Allow {
		if strings.HasPrefix(w, ReposDir()) {
			t.Errorf("Allow includes %q — the repo cache is read-only to the PM", w)
		}
	}
}

// A stack at the default location is already covered by the stacks dir grant
// and must not be granted twice.
func TestPMGrantsNoDuplicateStack(t *testing.T) {
	testutil.IsolateHome(t)
	cfg := &Config{Stacks: []Stack{{Alias: "s", Path: DefaultStackPath("s")}}}
	g := PMGrants(cfg)
	seen := map[string]int{}
	for _, p := range g.Allow {
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("%q granted %d times", p, n)
		}
	}
}

func TestPMEnvGatesPMToolsOnly(t *testing.T) {
	env := PMEnv(PM{Name: "research"})
	if env["SUPATREE_PM_NAME"] != "research" || env["SUPATREE_AGENT"] != "pm-research" {
		t.Errorf("env = %v, want the PM's name and agent name", env)
	}
	if env["SUPATREE_PM"] != "1" {
		t.Error("SUPATREE_PM not set — the cross-tree tools would be gated off")
	}
	// SUPATREE must stay unset: the PM is in no supatree, so the tree-scoped
	// tools would resolve nothing and fail confusingly rather than being hidden.
	if _, ok := env["SUPATREE"]; ok {
		t.Error("SUPATREE is set for the PM, which is in no supatree")
	}
}
