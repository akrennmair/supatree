package supatree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The steps of creating a worktree that can run code a repository's config
// names — the fetch (ssh command, credential helpers, url rewrites) and the
// checkout (filter drivers) — run inside nono, under the agents' own profile,
// with only that repository's git dir and the new worktree writable (#21).
//
// HardenGit, RequireSafeRepo and addWorktree's filter pins narrow what such
// config can do, but they check a repository an agent can rewrite between the
// check and git reading it, and some keys (include.*, url.*) cannot be pinned
// at all. Confined, whatever an agent planted runs with no more than the
// agent already had, so the race no longer matters.
//
// Plumbing that only reads — rev-parse, ls-tree, cat-file, config — runs no
// configured program and stays unconfined: a nono start per call would cost
// the watcher's status rounds dearly.

// gitJail is how confined git is started. The zero value is not usable: a
// jail with no profile refuses rather than run git unconfined.
type gitJail struct {
	profile string
	reads   []string
}

// gitJail is the jail for git run on behalf of trees: the nono profile their
// agents run under, so confined git can do nothing an agent could not.
func (c *Config) gitJail() (gitJail, error) {
	m, err := c.Model(c.ResolveModel(""))
	if err != nil {
		return gitJail{}, err
	}
	if m.NonoProfile == "" {
		return gitJail{}, fmt.Errorf("model %q has no nono profile to confine git with", c.ResolveModel(""))
	}
	return gitJail{profile: m.NonoProfile}, nil
}

// reading returns j with read access to each url that is a local path: a
// fetch from one reads it. Only URLs supatree holds — a stack's pinned origin,
// a member's URL from the spec — never one read from the repository's config,
// which an agent could point at anything on the machine.
func (j gitJail) reading(urls ...string) gitJail {
	reads := append([]string(nil), j.reads...)
	for _, u := range urls {
		if p, ok := localRepoPath(u); ok {
			reads = append(reads, p)
		}
	}
	j.reads = reads
	return j
}

// insideSandbox reports whether this process already runs under nono. Git it
// starts is confined by that sandbox, and a nested nono cannot start.
var insideSandbox = func() bool { return os.Getenv("NONO_CAP_FILE") != "" }

// command builds `git -C repo args…`, confined: repo readable, its common git
// dir and each of writable read-write, nothing else beyond the profile.
// Every writable path must exist, since nono grants only what is there.
func (j gitJail) command(ctx context.Context, repo string, writable []string, args ...string) (*exec.Cmd, error) {
	gitArgs := append([]string{"-C", repo}, args...)
	if insideSandbox() {
		return exec.CommandContext(ctx, "git", gitArgs...), nil
	}
	if j.profile == "" {
		return nil, errors.New("refusing to run git unconfined in a repository agents can write: no nono profile")
	}
	nonoArgs, err := j.nonoArgs(repo, writable)
	if err != nil {
		return nil, err
	}
	nonoArgs = append(append(nonoArgs, "--", "git"), gitArgs...)
	return exec.CommandContext(ctx, "nono", nonoArgs...), nil
}

// nonoArgs are the `nono run` arguments up to, not including, the "--".
func (j gitJail) nonoArgs(repo string, writable []string) ([]string, error) {
	common, err := GitCommonDir(repo)
	if err != nil {
		return nil, fmt.Errorf("find %s's git dir: %w", repo, err)
	}
	args := []string{"run", "--silent", "--no-rollback", "--profile", j.profile, "--read", filepath.Clean(repo)}
	for _, r := range j.reads {
		args = append(args, "--read", r)
	}
	args = append(args, "--allow", common)
	for _, w := range writable {
		args = append(args, "--allow", filepath.Clean(w))
	}
	return args, nil
}
