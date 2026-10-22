package supatree

import (
	"os"
	"testing"
)

// #33: a `git worktree add -b` that fails mid-checkout leaves the branch and
// no worktree. Rolling back must delete that branch too, or a retry under the
// same name fails on it.
func TestRollbackDeletesBranchOfMemberWithoutWorktree(t *testing.T) {
	c, _, _ := stackFixture(t)
	inst, _, err := New(c, CreateOptions{Stack: "s", Name: "quito"})
	if err != nil {
		t.Fatal(err)
	}
	mem := inst.FindMember("a")
	clone, ok := cloneOf(mem.Path)
	if !ok {
		t.Fatal("member a has no clone")
	}
	// What the failed checkout leaves: the branch, and no worktree.
	run(t, clone, "worktree", "remove", "--force", mem.Path)
	if !branchExists(t, clone, mem.Branch) {
		t.Fatal("fixture: the branch should outlive its worktree")
	}

	rollback(c, c.FindStack("s").Path, inst.Root, inst.Name)
	if branchExists(t, clone, mem.Branch) {
		t.Errorf("rollback left %s behind in %s", mem.Branch, clone)
	}
	if _, err := os.Stat(inst.Root); !os.IsNotExist(err) {
		t.Errorf("rollback left the tree root: %v", err)
	}
	// And the name is free again.
	if _, _, err := New(c, CreateOptions{Stack: "s", Name: "quito"}); err != nil {
		t.Fatalf("retry under the same name: %v", err)
	}
}

// A review tree's member with no PR under review costs one git call, and
// still reads its branch, so a manual checkout shows as foreign.
func TestBystanderStateReadsOnlyTheBranch(t *testing.T) {
	c, _, _ := stackFixture(t)
	inst, _, err := New(c, CreateOptions{Stack: "s", Name: "quito"})
	if err != nil {
		t.Fatal(err)
	}
	mem := *inst.FindMember("a")
	writeFile(t, mem.Path+"/notes.txt", "uncommitted")
	got := bystanderState(mem)
	if !got.Exists || got.CheckedOut != mem.Branch {
		t.Errorf("bystanderState = %+v, want it existing on %s", got, mem.Branch)
	}
	if got.Dirty || !got.LastCommit.IsZero() {
		t.Errorf("bystanderState ran more than the branch lookup: %+v", got)
	}
	if bystanderState(Member{}) != (MemberLocal{}) {
		t.Error("a missing bystander should report absent")
	}
}
