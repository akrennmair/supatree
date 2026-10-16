package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// pmConfigRepo is a PM config repo with shared and per-PM parts, registered.
func pmConfigRepo(t *testing.T) string {
	t.Helper()
	testutil.IsolateHome(t)
	gitGlobal(t, "user.email", "t@example.com")
	gitGlobal(t, "user.name", "t")
	repo := filepath.Join(t.TempDir(), "pm-config")
	run(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	writeFile(t, filepath.Join(repo, AgentsMDName), "Team rule: never merge on Fridays.\n")
	writeFile(t, filepath.Join(repo, ".claude", "skills", "triage", "SKILL.md"), "---\nname: triage\n---\n")
	writeFile(t, filepath.Join(repo, pmConfigDirName, pmReviews, AgentsMDName), "Review every PR twice.\n")
	writeFile(t, filepath.Join(repo, pmConfigDirName, pmReviews, "settings.json"), "{}\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "pm config")
	if err := SetPMConfig(repo); err != nil {
		t.Fatal(err)
	}
	return repo
}

// #11: a committed skill, instruction or setting reaches the PMs on their
// next launch, and different PMs can carry different ones.
func TestPMConfigReachesThePMs(t *testing.T) {
	repo := pmConfigRepo(t)
	if err := ScaffoldPM(); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(PMDir(), AgentsMDName))
	for _, want := range []string{pmAgentsMD[:40], "never merge on Fridays", "Only if you are the `reviews` PM", "Review every PR twice"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("AGENTS.md lacks %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(PMDir(), ".claude", "skills", "triage", "SKILL.md")); err != nil {
		t.Error("the repo's .claude is not linked into the PM's home")
	}

	if got := pmSettingsArgs(repo, "/usr/local/bin/claude", PM{Name: pmReviews}); len(got) != 2 || got[0] != "--settings" {
		t.Errorf("reviews settings args = %v", got)
	}
	if got := pmSettingsArgs(repo, "claude", PM{Name: DefaultPMName}); len(got) != 0 {
		t.Errorf("a PM with no settings of its own got %v", got)
	}
	if got := pmSettingsArgs(repo, "codex", PM{Name: pmReviews}); len(got) != 0 {
		t.Errorf("a non-claude model got claude flags: %v", got)
	}
}

// The PM reads its config and never writes it.
func TestPMGrantsReadTheConfigOnly(t *testing.T) {
	repo := pmConfigRepo(t)
	cfg, _ := Load()
	g := PMGrants(cfg)
	if !strings.Contains(strings.Join(g.Read, " "), repo) {
		t.Errorf("Read = %v, want the PM config", g.Read)
	}
	for _, w := range g.Allow {
		if w == repo || strings.HasPrefix(repo, w+"/") {
			t.Errorf("Allow %q covers the PM config — a PM could edit what governs it", w)
		}
	}
}

// A config repo where an agent can write it would make the rule meaningless.
func TestSetPMConfigRefusesAgentWritablePlaces(t *testing.T) {
	testutil.IsolateHome(t)
	notGit := t.TempDir()
	if err := SetPMConfig(notGit); err == nil {
		t.Error("accepted a directory that is not a git repository")
	}
	inStacks := filepath.Join(StacksDir(), "pm")
	run(t, filepath.Dir(notGit), "init", "-q", inStacks)
	if err := SetPMConfig(inStacks); err == nil || !strings.Contains(err.Error(), "agent can write") {
		t.Errorf("under the stacks dir: err = %v", err)
	}
	// The repo cache's clones are written by tree agents.
	inCache := filepath.Join(ReposDir(), "github.com", "o", "pm")
	run(t, filepath.Dir(notGit), "init", "-q", inCache)
	if err := SetPMConfig(inCache); err == nil {
		t.Error("a clone in the repo cache was accepted")
	}
	// A link elsewhere that leads into the stacks is judged by where it leads.
	link := filepath.Join(t.TempDir(), "pm-config")
	if err := os.Symlink(inStacks, link); err != nil {
		t.Fatal(err)
	}
	if err := SetPMConfig(link); err == nil {
		t.Error("a link into the stacks dir was accepted")
	}
}

// A registered config whose path is later swapped for a link into agent-
// writable space is not used at launch.
func TestPMConfigRecheckedAtLaunch(t *testing.T) {
	repo := pmConfigRepo(t)
	cfg, _ := Load()
	if got := usablePMConfig(cfg); got == "" {
		t.Fatal("a good config was refused")
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	inStacks := filepath.Join(StacksDir(), "planted")
	run(t, t.TempDir(), "init", "-q", inStacks)
	if err := os.Symlink(inStacks, repo); err != nil {
		t.Fatal(err)
	}
	if got := usablePMConfig(cfg); got != "" {
		t.Errorf("usablePMConfig = %q; a config now leading into the stacks dir must not be used", got)
	}
	if g := PMGrants(cfg); strings.Contains(strings.Join(g.Read, " "), "planted") {
		t.Error("the PM was granted the swapped-in config")
	}
}

// Whatever stands at the PM's .claude — made by hand, or by a PM giving
// itself skills — is moved aside, kept, and the repo's link restored.
func TestPMConfigReclaimsTheClaudeDir(t *testing.T) {
	_ = pmConfigRepo(t)
	mine := filepath.Join(PMDir(), ".claude", "settings.local.json")
	writeFile(t, mine, "{\"mine\": true}\n")
	if err := ScaffoldPM(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(PMDir(), ".claude")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the PM's .claude is not the config repo's link")
	}
	moved, _ := filepath.Glob(filepath.Join(PMDir(), ".claude.moved-*", "settings.local.json"))
	if len(moved) != 1 {
		t.Errorf("the hand-made settings were not kept aside: %v", moved)
	}
}

// pms.yml is in the PM's writable home: a PM name walking out of pms/ gets no
// --settings at all.
func TestPMSettingsArgsStayInTheRepo(t *testing.T) {
	repo := pmConfigRepo(t)
	writeFile(t, filepath.Join(repo, "evil.json"), "{}\n")
	for _, name := range []string{"../evil.json/..", "..", "../..", "a/b"} {
		if got := pmSettingsArgs(repo, "claude", PM{Name: name}); len(got) != 0 {
			t.Errorf("PM name %q got %v", name, got)
		}
	}
}
