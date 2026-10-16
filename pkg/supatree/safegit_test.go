package supatree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// filterRepo is a repo whose committed .gitattributes names a filter, and
// whose config — written after any check, as an agent racing one would —
// defines it to leave a marker when it runs.
func filterRepo(t *testing.T, name string) (repo, marker string) {
	t.Helper()
	repo = originRepo(t)
	writeFile(t, filepath.Join(repo, ".gitattributes"), "*.txt filter="+name+"\n")
	writeFile(t, filepath.Join(repo, "x.txt"), "hello\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "attrs")
	marker = filepath.Join(t.TempDir(), "ran")
	run(t, repo, "config", "filter."+name+".smudge", "touch "+marker+"; cat")
	run(t, repo, "config", "filter."+name+".process", "touch "+marker)
	run(t, repo, "config", "filter."+name+".required", "true")
	return repo, marker
}

// #21: whatever the config says by the time git runs, a filter the checkout
// names is pinned off on the command line.
func TestFilterPinsNeutraliseAPlantedFilter(t *testing.T) {
	testutil.IsolateHome(t)
	repo, marker := filterRepo(t, "evil")
	pins, err := filterPins(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	args := append(pins, "-C", repo, "worktree", "add", "-q", "-b", "t", wt, "HEAD")
	if out, err := exec.CommandContext(context.Background(), "git", args...).CombinedOutput(); err != nil {
		t.Fatalf("worktree add with pins: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the planted filter ran despite the pins")
	}
	if body, _ := os.ReadFile(filepath.Join(wt, "x.txt")); string(body) != "hello\n" {
		t.Errorf("checked-out content = %q, want it passed through", body)
	}
}

// RequireSafeRepo, checked again inside addWorktree, still refuses a config
// that already defines the filter when it looks.
func TestAddWorktreeRefusesAConfiguredFilter(t *testing.T) {
	testutil.IsolateHome(t)
	repo, marker := filterRepo(t, "evil")
	if err := addWorktree(repo, filepath.Join(t.TempDir(), "wt"), "t", "HEAD"); err == nil {
		t.Error("checked out a repository whose config defines a filter")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the filter ran")
	}
}

// A filter name is spliced into a -c key, so one that could carry a value of
// its own is refused, not pinned.
func TestFilterNamesRefuseInjection(t *testing.T) {
	testutil.IsolateHome(t)
	repo := originRepo(t)
	writeFile(t, filepath.Join(repo, ".gitattributes"), "*.txt filter=a.smudge=touch%20/tmp/x\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "attrs")
	if _, err := filterNames(repo, "HEAD"); err == nil || !strings.Contains(err.Error(), "cannot pin") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

// The stack worktree is fetched from the pinned origin, not from whatever the
// checkout's own origin remote now says.
func TestCreateWorktreeFetchesFromThePinnedURL(t *testing.T) {
	testutil.IsolateHome(t)
	upstream := originRepo(t)
	writeFile(t, filepath.Join(upstream, "REAL.md"), "real\n")
	run(t, upstream, "add", "-A")
	run(t, upstream, "commit", "-q", "-m", "real")
	bare := filepath.Join(t.TempDir(), "stack.git")
	run(t, filepath.Dir(bare), "clone", "-q", "--bare", upstream, bare)

	stack := filepath.Join(t.TempDir(), "stack")
	run(t, filepath.Dir(stack), "clone", "-q", bare, stack)
	run(t, stack, "update-ref", "refs/remotes/origin/main", "HEAD~1") // stale
	run(t, stack, "remote", "set-url", "origin", "/nonexistent/decoy.git")

	wt := filepath.Join(t.TempDir(), "tree")
	offline, err := createWorktree(stack, wt, "st/tree", bare)
	if err != nil || offline {
		t.Fatalf("createWorktree = offline %v, %v", offline, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "REAL.md")); err != nil {
		t.Error("the tree was not built from the pinned origin's branch")
	}
}

// With a pinned origin, an unreachable origin is an error — never a fallback
// to the agent-writable origin/<default> ref.
func TestCreateWorktreeFailsClosedOnThePinnedURL(t *testing.T) {
	testutil.IsolateHome(t)
	upstream := originRepo(t)
	stack := filepath.Join(t.TempDir(), "stack")
	run(t, filepath.Dir(stack), "clone", "-q", upstream, stack)
	wt := filepath.Join(t.TempDir(), "tree")
	if _, err := createWorktree(stack, wt, "st/tree", "/nonexistent/stack.git"); err == nil {
		t.Fatal("built a tree from local refs when the pinned origin could not be reached")
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("a worktree was created anyway")
	}
}

// .GitAttributes is .gitattributes on a case-insensitive filesystem; its
// filters are pinned too.
func TestFilterNamesAnyCase(t *testing.T) {
	testutil.IsolateHome(t)
	repo := originRepo(t)
	writeFile(t, filepath.Join(repo, "sub", ".GitAttributes"), "*.txt filter=sneaky\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "attrs")
	names, err := filterNames(repo, "HEAD")
	if err != nil || len(names) != 1 || names[0] != "sneaky" {
		t.Errorf("filterNames = %v, %v; want sneaky", names, err)
	}
}
