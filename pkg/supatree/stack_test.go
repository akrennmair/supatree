package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/testutil"
)

// stackFixture scaffolds a stack with one member and returns the config.
func stackFixture(t *testing.T) (*Config, string, string) {
	t.Helper()
	testutil.IsolateHome(t)
	gitGlobal(t, "user.email", "t@example.com")
	gitGlobal(t, "user.name", "t")
	a, b := originRepo(t), originRepo(t)
	c := &Config{}
	if _, err := Scaffold(c, "s", "", map[string]string{"a": a}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg, a, b
}

// Every stack edit is a commit of supatree.yml, and nothing else.
func TestStackEdits(t *testing.T) {
	c, a, b := stackFixture(t)
	path := c.FindStack("s").Path
	writeFile(t, filepath.Join(path, "AGENTS.md"), "someone's draft")

	if _, err := c.StackAdd("s", "b", b, true, ByHuman); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := c.StackAdd("s", "b2", b, false, ByHuman); err == nil || !strings.Contains(err.Error(), "already a member") {
		t.Errorf("adding the same repo twice: %v", err)
	}
	if _, err := c.StackDep("s", "b", "a", ByHuman); err != nil {
		t.Fatalf("dep: %v", err)
	}
	if _, err := c.StackDep("s", "a", "b", ByHuman); err == nil {
		t.Error("a dependency cycle was accepted")
	}
	spec, _ := LoadSpec(path)
	if spec.Members["b"] != b || len(spec.Deps["b"]) != 1 {
		t.Fatalf("spec = %+v", spec)
	}
	if !isClone(filepath.Join(ReposDir(), mustKey(t, b))) {
		t.Error("stack add did not clone into the cache")
	}
	if _, err := c.StackRm("s", "a", ByHuman); err != nil {
		t.Fatalf("rm: %v", err)
	}
	spec, _ = LoadSpec(path)
	if _, ok := spec.Members["a"]; ok || len(spec.Deps) != 0 {
		t.Errorf("rm left %+v", spec)
	}
	_ = a

	// Each edit committed the spec alone; the unrelated draft is untouched.
	log, _ := gitOutput(path, "log", "--oneline")
	if n := strings.Count(log, "\n") + 1; n != 4 {
		t.Errorf("commits = %d, want scaffold + add + dep + rm:\n%s", n, log)
	}
	if status, _ := gitOutput(path, "status", "--porcelain"); !strings.Contains(status, "AGENTS.md") {
		t.Error("an edit swept someone's uncommitted AGENTS.md into its commit")
	}
	writeFile(t, filepath.Join(path, SpecName), "members: {}\n")
	if _, err := c.StackAdd("s", "a", a, false, ByHuman); err == nil {
		t.Error("edited over an uncommitted supatree.yml")
	}
}

func mustKey(t *testing.T, url string) string {
	t.Helper()
	k, err := CacheKey(url)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A teammate adopts a stack by URL: registered, members cached, setup shown.
func TestStackClone(t *testing.T) {
	c, a, _ := stackFixture(t)
	src := c.FindStack("s").Path
	writeFile(t, filepath.Join(src, SetupScript), "echo hi\n")
	run(t, src, "add", "-A")
	run(t, src, "commit", "-q", "-m", "setup")
	if err := os.RemoveAll(ReposDir()); err != nil {
		t.Fatal(err)
	}

	res, err := c.StackClone(src, "team", "")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if res.Setup != "echo hi\n" {
		t.Errorf("setup not surfaced: %q", res.Setup)
	}
	cfg, _ := Load()
	if s := cfg.FindStack("team"); s == nil || s.Path != DefaultStackPath("team") {
		t.Errorf("not registered: %+v", cfg.Stacks)
	}
	if !isClone(filepath.Join(ReposDir(), mustKey(t, a))) {
		t.Error("members not cloned into the cache")
	}
}

// --from reads origins only.
func TestScanOrigins(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	withOrigin := filepath.Join(dir, "api")
	run(t, dir, "init", "-q", withOrigin)
	run(t, withOrigin, "remote", "add", "origin", "git@github.com:o/Keystone-API.git")
	run(t, dir, "init", "-q", filepath.Join(dir, "noorigin"))
	if err := os.MkdirAll(filepath.Join(dir, "notarepo"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := ScanOrigins(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["keystone-api"] != "git@github.com:o/Keystone-API.git" {
		t.Errorf("ScanOrigins = %v", got)
	}
}

// A clone a tree still uses cannot be removed from the cache.
func TestRemoveCachedRepoRefusesInUse(t *testing.T) {
	c, a, _ := stackFixture(t)
	inst, _, err := New(c, CreateOptions{Stack: "s", Name: treeLima})
	if err != nil {
		t.Fatal(err)
	}
	r, err := FindCachedRepo(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveCachedRepo(r, []*Instance{inst}); err == nil || !strings.Contains(err.Error(), treeLima) {
		t.Errorf("err = %v, want a refusal naming the tree", err)
	}
	// Even with no tree reporting it — a member pruned from the spec, a tree
	// List could not parse — the clone's own worktree list keeps it.
	if err := RemoveCachedRepo(r, nil); err == nil {
		t.Error("removed a clone that still has a worktree")
	}
	if _, err := Remove(c, treeLima, RemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCachedRepo(r, nil); err != nil {
		t.Errorf("unused clone not removed: %v", err)
	}
}

// sameOwner makes the stack's existing member and b resolve to one GitHub
// owner, so a PM may add b alongside it (ownerVouchedFor).
func sameOwner(t *testing.T, c *Config, b string) {
	t.Helper()
	spec, err := LoadSpec(c.FindStack("s").Path)
	if err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{spec.Members["a"]: "git@github.com:team/a.git", b: "git@github.com:team/b.git"}
	orig := resolveRemote
	t.Cleanup(func() { resolveRemote = orig })
	resolveRemote = func(raw string) (github.RemoteRef, bool) {
		if u, ok := urls[raw]; ok {
			return github.ParseRemote(u)
		}
		return orig(raw)
	}
}

// stackWithOrigin is stackFixture with a bare origin the stack tracks, the
// shape every shared stack has.
func stackWithOrigin(t *testing.T) (c *Config, stack, origin, b string) {
	t.Helper()
	c, _, b = stackFixture(t)
	stack = c.FindStack("s").Path
	origin = filepath.Join(t.TempDir(), "stack.git")
	run(t, stack, "clone", "-q", "--bare", stack, origin)
	run(t, stack, "remote", "add", "origin", origin)
	run(t, stack, "fetch", "-q", "origin")
	run(t, stack, "branch", "-q", "--set-upstream-to=origin/main")
	return c, stack, origin, b
}

func originSpecHas(t *testing.T, origin, alias string) bool {
	t.Helper()
	out, err := gitOutput(origin, "show", "main:"+SpecName)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(out, alias+":")
}

// #17: an edit is published to the pinned origin's default branch — that one
// commit only: what else sits unpushed in the checkout is not supatree's to
// publish. Unpinned, it is committed locally and says how to pin; the
// checkout's own remote is never adopted, since an agent can write it.
func TestStackEditPublishes(t *testing.T) {
	c, _, origin, b := stackWithOrigin(t)
	res, err := c.StackAdd("s", "early", b+"-early", false, ByHuman)
	if err != nil {
		t.Fatal(err)
	}
	if res.Live() || !strings.Contains(res.Summary(), "supatree stack pin s <url>") || originSpecHas(t, origin, "early") {
		t.Errorf("unpinned edit: %+v (%s); want a local commit naming stack pin", res, res.Summary())
	}
	if cfg, _ := Load(); cfg.FindStack("s").Origin != "" {
		t.Error("the checkout's origin was pinned without being asked")
	}

	if err := PinStackOrigin("s", origin); err != nil {
		t.Fatal(err)
	}
	c, _ = Load()
	res, err = c.StackAdd("s", "b", b, false, ByHuman)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || !res.Live() {
		t.Fatalf("result = %+v; want pushed", res)
	}
	if !originSpecHas(t, origin, "b") {
		t.Error("origin's spec lacks the edit")
	}
	if originSpecHas(t, origin, "early") {
		t.Error("an earlier local-only commit was published along with the edit")
	}
	if !strings.Contains(res.Summary(), "pull it") {
		t.Errorf("summary = %q; want it to say the checkout is behind", res.Summary())
	}
}

// A default branch that refuses pushes gets the edit as a pull request.
func TestStackEditFallsBackToAPullRequest(t *testing.T) {
	_, _, origin, b := stackWithOrigin(t)
	if err := PinStackOrigin("s", origin); err != nil {
		t.Fatal(err)
	}
	c, _ := Load()
	hook := filepath.Join(origin, "hooks", "pre-receive")
	writeFile(t, hook, "#!/bin/sh\nwhile read old new ref; do [ \"$ref\" = refs/heads/main ] && { echo protected >&2; exit 1; }; done\nexit 0\n")
	if err := os.Chmod(hook, 0755); err != nil {
		t.Fatal(err)
	}
	orig := openStackPR
	var gotBranch string
	openStackPR = func(_, branch, base, _ string) (string, error) {
		gotBranch = branch
		return "https://github.com/o/stack/pull/7", nil
	}
	t.Cleanup(func() { openStackPR = orig })

	res, err := c.StackAdd("s", "b", b, false, ByHuman)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed || res.PRURL == "" || res.Branch == "" || res.Branch != gotBranch {
		t.Fatalf("result = %+v; want a PR from the pushed branch", res)
	}
	// Published, but not on the branch trees are built from until it merges.
	if res.Live() || !strings.Contains(res.Summary(), "pull/7") {
		t.Errorf("live = %v, summary = %q; want not live yet, naming the PR", res.Live(), res.Summary())
	}
	if originSpecHas(t, origin, "b") {
		t.Error("the protected branch took the edit")
	}
}

// The watcher, editing for the PM, trusts nothing in the checkout the PM can
// write: not its remotes, not its commits. It publishes to the pinned origin
// from a private clone, and refuses a stack nobody pinned.
func TestWatcherStackEditIgnoresTheCheckout(t *testing.T) {
	c, stack, origin, b := stackWithOrigin(t)
	sameOwner(t, c, b)
	if _, err := c.StackAdd("s", "b", b, false, ByWatcher); err == nil || !strings.Contains(err.Error(), "supatree stack pin") {
		t.Fatalf("unpinned stack: err = %v; want a refusal naming stack pin", err)
	}
	if err := PinStackOrigin("s", origin); err != nil {
		t.Fatal(err)
	}
	c, _ = Load()

	// What a PM could do to the checkout: point origin elsewhere, and leave a
	// commit to be published under the human's name.
	evil := filepath.Join(t.TempDir(), "evil.git")
	run(t, filepath.Dir(evil), "init", "-q", "--bare", evil)
	run(t, stack, "remote", "set-url", "origin", evil)
	writeFile(t, filepath.Join(stack, "PLANTED.md"), "planted\n")
	run(t, stack, "add", "-A")
	run(t, stack, "commit", "-q", "-m", "planted")

	res, err := c.StackAdd("s", "b", b, false, ByWatcher)
	if err != nil || !res.Pushed {
		t.Fatalf("watcher edit: %+v, %v", res, err)
	}
	if !originSpecHas(t, origin, "b") {
		t.Error("the edit did not reach the pinned origin")
	}
	if out, _ := gitOutput(origin, "log", "--format=%s", "main"); strings.Contains(out, "planted") {
		t.Error("a commit planted in the checkout was published")
	}
	if refs, _ := gitOutput(evil, "for-each-ref"); refs != "" {
		t.Errorf("the checkout's rewritten origin was pushed to: %s", refs)
	}
}

// The PM, with no pinned origin to hand to the watcher, commits in its own
// sandbox, and says the edit went no further.
func TestSandboxStackEditSaysItIsLocal(t *testing.T) {
	c, _, origin, b := stackWithOrigin(t)
	res, err := c.StackAdd("s", "b", b, false, InSandbox)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" || res.Live() || !strings.Contains(res.Summary(), "supatree stack pin") {
		t.Errorf("result = %+v (%s); want a local commit that names stack pin", res, res.Summary())
	}
	if originSpecHas(t, origin, "b") {
		t.Error("a sandboxed edit reached origin")
	}
}

// The PM's stack edits run in the watcher, which can push and clone; one that
// the next tree would not see is answered as an error.
func TestRunStackEditOp(t *testing.T) {
	c, _, origin, b := stackWithOrigin(t)
	orig := resolveRemote
	t.Cleanup(func() { resolveRemote = orig })
	resolveRemote = func(raw string) (github.RemoteRef, bool) {
		if raw == b {
			return github.ParseRemote("git@github.com:o/b.git")
		}
		if spec, _ := LoadSpec(c.FindStack("s").Path); spec != nil && raw == spec.Members["a"] {
			return github.ParseRemote("git@github.com:o/a.git") // the existing member vouches for o
		}
		return orig(raw)
	}
	origRemote := isRemoteRepoURL
	t.Cleanup(func() { isRemoteRepoURL = origRemote })
	isRemoteRepoURL = func(raw string) bool { return raw == b || origRemote(raw) }
	if err := PinStackOrigin("s", origin); err != nil {
		t.Fatal(err)
	}
	c, _ = Load()
	pm := func(op Op) OpResult {
		op.Kind, op.Stack = OpStack, "s"
		return c.RunOp(ClaimedOp{Dir: PMDir(), Op: op}, OpHooks{})
	}

	res := pm(Op{Action: StackActionAdd, Alias: "b", URL: b})
	if res.Err != "" || !strings.Contains(res.Text, "new trees get it") {
		t.Fatalf("add: %+v", res)
	}
	if !originSpecHas(t, origin, "b") {
		t.Error("the op did not publish the edit")
	}
	if !isClone(filepath.Join(ReposDir(), mustKey(t, b))) {
		t.Error("the watcher's add did not clone the member")
	}

	// A local path would have the watcher copy any repo on the machine into
	// the cache, which the PM can read.
	if res := pm(Op{Action: StackActionAdd, Alias: "secret", URL: t.TempDir()}); res.Err == "" || !strings.Contains(res.Err, "remote repository") {
		t.Errorf("add of a local path: %+v; want a refusal", res)
	}
	// Nor one that only looks like host:owner/name: git reads a '/' before the
	// first ':' as a local path.
	if res := pm(Op{Action: StackActionAdd, Alias: "secret2", URL: t.TempDir() + "/x:o/r"}); res.Err == "" || !strings.Contains(res.Err, "remote repository") {
		t.Errorf("add of a colon path: %+v; want a refusal", res)
	}

	// An unreachable origin is an error.
	if err := PinStackOrigin("s", "/nonexistent/stack.git"); err != nil {
		t.Fatal(err)
	}
	c, _ = Load()
	if res := pm(Op{Action: StackActionRm, Alias: "b"}); res.Err == "" {
		t.Errorf("rm with an unreachable origin: %+v; want an error", res)
	}

	// Only the PM may ask.
	tree := ClaimedOp{Dir: filepath.Join(TreesStateDir(), treeLima), Op: Op{Kind: OpStack, Stack: "s", Action: StackActionRm, Alias: "a"}}
	if res := c.RunOp(tree, OpHooks{}); res.Err == "" {
		t.Error("a tree was allowed to edit its stack")
	}
}

// What a PM may hand the watcher as a member: the three remote forms, and
// nothing git would read as local or as another transport.
func TestIsRemoteRepoURL(t *testing.T) {
	testutil.IsolateHome(t)
	// A host the human has cloned from before is known; others are not.
	if err := os.MkdirAll(filepath.Join(ReposDir(), "gitlab.example.com", "o", "r", ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	for url, want := range map[string]bool{
		"git@github.com:o/r.git":         true,
		"https://github.com/o/r":         true,
		"https://github.com/o/r.git":     true,
		"ssh://git@github.com/o/r.git":   true,
		"git@gitlab.example.com:o/r.git": true,
		"git@internal.corp:o/r.git":      false, // a host nobody cloned from
		"https://169.254.169.254/o/r":    false,
		"deploy@github.com:o/r.git":      false, // ssh as anyone but git
		"https://github.com/../x":        false,
		"https://github.com/o/..":        false,
		"https://github.com/.hidden/r":   false,
		"git@github.com:-o/r.git":        false,
		"/tmp/x:o/r":                     false,
		"./x:o/r":                        false,
		"github.com:o/r":                 false,
		"file:///tmp/r":                  false,
		"ext::sh -c touch% /tmp/pwned":   false,
		"https://github.com/o/r/../../x": false,
		"-oProxyCommand=x@h:o/r":         false,
		"git@-oProxyCommand=x:o/r":       false,
		"https://github.com/o":           false,
		"/tmp/r":                         false,
	} {
		if got := isRemoteRepoURL(url); got != want {
			t.Errorf("isRemoteRepoURL(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestCacheKeyRefusesDotSegments(t *testing.T) {
	for _, url := range []string{"https://github.com/../x", "git@github.com:o/..", "https://github.com/./r"} {
		if k, err := CacheKey(url); err == nil {
			t.Errorf("CacheKey(%q) = %q, want an error", url, k)
		}
	}
}

func TestIsLocalGitURL(t *testing.T) {
	for url, want := range map[string]bool{
		"git@github.com:o/r.git":   false,
		"github.com:o/r":           false,
		"ssh://git@github.com/o/r": false,
		"https://github.com/o/r":   false,
		"file:///tmp/r":            true,
		"/tmp/r":                   true,
		"/tmp/x:o/r":               true,
		"./x:o/r":                  true,
		"../stack":                 true,
	} {
		if got := isLocalGitURL(url); got != want {
			t.Errorf("isLocalGitURL(%q) = %v, want %v", url, got, want)
		}
	}
	if got := AbsGitURL("../stack", "/a/b"); got != "/a/stack" {
		t.Errorf("AbsGitURL relative = %q", got)
	}
	if got := AbsGitURL("git@github.com:o/r.git", "/a/b"); got != "git@github.com:o/r.git" {
		t.Errorf("AbsGitURL remote = %q", got)
	}
}

// A PM may add a repository only under an owner the pinned origin's spec, or
// the origin itself, already uses: the watcher clones with the human's
// credentials, which reach every private repository they can.
func TestWatcherAddStaysWithTheStacksOwners(t *testing.T) {
	_, stack, origin, b := stackWithOrigin(t)
	orig := resolveRemote
	t.Cleanup(func() { resolveRemote = orig })
	fake := map[string]string{b: "git@github.com:team/b.git"}
	resolveRemote = func(raw string) (github.RemoteRef, bool) {
		if u, ok := fake[raw]; ok {
			raw = u
		}
		return github.ParseRemote(raw)
	}
	if err := PinStackOrigin("s", origin); err != nil {
		t.Fatal(err)
	}
	c, _ := Load()
	if _, err := c.StackAdd("s", "b", b, false, ByWatcher); err == nil || !strings.Contains(err.Error(), "not under an owner") {
		t.Fatalf("first team repo, nobody vouching: err = %v; want a refusal", err)
	}
	// A human adds one team repo; after that a PM may add its siblings.
	if _, err := c.StackAdd("s", "b", b, false, ByHuman); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "sibling")
	fake[other] = "git@github.com:team/sibling.git"
	if _, err := c.StackAdd("s", "sibling", other, false, ByWatcher); err != nil {
		t.Errorf("a sibling under a vouched-for owner: %v", err)
	}
	stranger := filepath.Join(t.TempDir(), "stranger")
	fake[stranger] = "git@github.com:someone-else/private.git"
	if _, err := c.StackAdd("s", "stranger", stranger, false, ByWatcher); err == nil {
		t.Error("a repository under an owner the stack does not use was added for the PM")
	}
	// The checkout is agent-writable: a member planted there vouches for nothing.
	spec, _ := LoadSpec(stack)
	spec.Members["planted"] = "git@github.com:someone-else/decoy.git"
	if err := SaveSpec(stack, spec); err != nil {
		t.Fatal(err)
	}
	run(t, stack, "commit", "-q", "-am", "planted")
	if _, err := c.StackAdd("s", "stranger", stranger, false, ByWatcher); err == nil {
		t.Error("a member planted in the checkout vouched for a new owner")
	}
}
