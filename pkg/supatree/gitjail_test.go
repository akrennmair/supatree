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
	want := []string{"nono", "run", "--silent", "--no-rollback", "--profile", AgentProfileName,
		"--read", repo, "--read", origin, "--allow", common, "--allow", wt,
		"--", "git", "-C", repo, "fetch", "origin"}
	if got := append([]string{filepath.Base(cmd.Path)}, cmd.Args[1:]...); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("command = %v\nwant      %v", got, want)
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

// With an enforcing nono and the agents' profile installed, a filter driver
// planted in the repository's config — the race RequireSafeRepo cannot close —
// runs confined: it cannot write outside what the jail grants.
func TestGitJailConfinesAPlantedFilter(t *testing.T) {
	if os.Getenv("NONO_CAP_FILE") != "" {
		t.Skip("already inside a nono sandbox; a nested one cannot start")
	}
	if err := exec.CommandContext(context.Background(), "nono", "run", "--silent", "--profile", AgentProfileName, "--", "true").Run(); err != nil {
		t.Skipf("no enforcing nono with profile %s here: %v", AgentProfileName, err)
	}
	withoutSandbox(t)
	repo, marker := filterRepo(t, "evil")
	// Straight to git, past addWorktree's pins and checks: this is git
	// meeting the filter in the window after them.
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	cmd, err := testJail.command(context.Background(), repo, []string{wt}, "worktree", "add", "-b", "t", wt, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Run()
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("a filter run by confined git wrote %s, outside the jail", marker)
	}
}
