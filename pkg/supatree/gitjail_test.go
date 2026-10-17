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

// testJail is a jail as the watcher builds one; under TestMain git runs
// unconfined, as inside a sandbox.
var testJail = gitJail{profile: AgentProfileName}

func withoutSandbox(t *testing.T) {
	t.Helper()
	insideSandbox = func() bool { return false }
	t.Cleanup(func() { insideSandbox = func() bool { return true } })
}

// Confined git gets the repository to read, its git dir and the given paths
// to write, under the agents' profile, and nothing more.
func TestGitJailCommand(t *testing.T) {
	testutil.IsolateHome(t)
	withoutSandbox(t)
	repo := originRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	origin := t.TempDir()
	cmd, err := testJail.reading(origin, "git@github.com:o/r.git").command(context.Background(), repo, []string{wt}, "fetch", "origin")
	if err != nil {
		t.Fatal(err)
	}
	common, err := GitCommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	// A local origin is readable, a remote one needs nothing.
	want := []string{"nono", "run", "--silent", "--no-rollback", "--allow-cwd", "--profile", AgentProfileName,
		"--read", repo, "--read", origin, "--allow", common, "--allow", wt,
		"--", "git", "-C", repo, "fetch", "origin"}
	if got := append([]string{filepath.Base(cmd.Path)}, cmd.Args[1:]...); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("command = %v\nwant      %v", got, want)
	}
	if cmd.Dir != repo {
		t.Errorf("runs from %q, want the repository: nono grants the working directory", cmd.Dir)
	}
}

// A jail with no profile refuses: confinement that silently turns itself off
// is the gap #21 is about.
func TestGitJailWithoutAProfileRefuses(t *testing.T) {
	withoutSandbox(t)
	if _, err := (gitJail{}).command(context.Background(), t.TempDir(), nil, "status"); err == nil {
		t.Fatal("a jail with no profile ran git unconfined")
	}
	c := &Config{Models: DefaultModels()}
	for k, m := range c.Models {
		m.NonoProfile = ""
		c.Models[k] = m
	}
	if _, err := c.gitJail(); err == nil {
		t.Error("gitJail() accepted a model with no nono profile")
	}
}

// With an enforcing nono, a filter driver planted in the repository's config
// — the race RequireSafeRepo cannot close — runs confined: it cannot write
// outside what the jail grants.
//
// It uses nono's built-in default profile: tests run with an isolated HOME,
// where the agents' profile is not installed. Outside $TMPDIR — in the package
// directory — go the marker, since nono's base profiles leave $TMPDIR
// writable, and nono's own HOME, since nono refuses to start with its state
// under a system path it grants (/private/var on macOS).
func TestGitJailConfinesAPlantedFilter(t *testing.T) {
	if os.Getenv("NONO_CAP_FILE") != "" {
		t.Skip("already inside a nono sandbox; a nested one cannot start")
	}
	outside, err := os.MkdirTemp(".", ".jailtest-")
	if err != nil {
		t.Fatal(err)
	}
	if outside, err = filepath.Abs(outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	marker := filepath.Join(outside, "ran")
	home := filepath.Join(outside, "home")
	env := append(os.Environ(), "HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"))

	jail := gitJail{profile: "default"}
	probe := exec.CommandContext(context.Background(), "nono", "run", "--silent", "--allow-cwd", "--profile", jail.profile, "--", "true")
	probe.Dir = t.TempDir()
	probe.Env = env
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("no usable nono here: %v: %s", err, out)
	}
	withoutSandbox(t)

	repo, _ := filterRepo(t, "evil")
	// The filter still passes the content through, so a checkout that runs
	// at all completes, and the test can tell confinement from git failing.
	run(t, repo, "config", "filter.evil.smudge", "touch "+marker+"; cat")
	run(t, repo, "config", "--unset", "filter.evil.process")
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	// Straight to git, past addWorktree's pins and checks: this is git
	// meeting the filter in the window after them.
	cmd, err := jail.command(context.Background(), repo, []string{wt}, "worktree", "add", "-b", "t", wt, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// The user's git config is outside the default profile, and git stops
	// at a config file it may not read.
	cmd.Env = append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("confined git worktree add: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(wt, "x.txt")); err != nil {
		t.Fatalf("the checkout did not happen, so confinement is unproven: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("a filter run by confined git wrote %s, outside the jail", marker)
	}
}
