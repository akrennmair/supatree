package supatree

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// currentAgentsMDSum is the SHA-256 of scaffoldAgentsMD. Changing the scaffold
// strands every stack holding the old one unless the old one is registered in
// priorAgentsMD; this pin is what makes that impossible to forget.
const currentAgentsMDSum = "889890ad73c61caacbf0b4bd447e31e95ad83261a9d60583beaf88757803048f"

func TestScaffoldAgentsMDIsRegistered(t *testing.T) {
	sum := sha256.Sum256([]byte(scaffoldAgentsMD))
	if got := hex.EncodeToString(sum[:]); got != currentAgentsMDSum {
		t.Fatalf("scaffoldAgentsMD changed (sha256 %s). Add the previous one to priorAgentsMD as {<its length in bytes>, %q}, then set currentAgentsMDSum to the new sum.",
			got, currentAgentsMDSum)
	}
	if !strings.HasSuffix(scaffoldAgentsMD, agentsMDTail+"\n") {
		t.Error("scaffoldAgentsMD must end with agentsMDTail, which is where a stack's own instructions go")
	}
}

// Every template that shipped is recognised, with what the human added below
// it carried over to the current one.
func TestCheckAgentsMDUpgradesEveryShippedTemplate(t *testing.T) {
	const own = "\n## This issue\n\nRun the e2e suite before any PR.\n"
	for _, v := range []string{"v1", "v2", "v3"} {
		old, err := os.ReadFile(filepath.Join("testdata", "agentsmd", v+".md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, tail := range []string{"", own} {
			state, upgraded := CheckAgentsMD(append(append([]byte{}, old...), tail...))
			if state != AgentsMDStale {
				t.Errorf("%s (tail %q): state = %v, want stale", v, tail, state)
				continue
			}
			if string(upgraded) != scaffoldAgentsMD+tail {
				t.Errorf("%s (tail %q): upgraded =\n%s", v, tail, upgraded)
			}
			if again, _ := CheckAgentsMD(upgraded); again != AgentsMDOK {
				t.Errorf("%s: the upgrade is itself reported as %v", v, again)
			}
		}
	}
}

func TestCheckAgentsMDLeavesOthersAlone(t *testing.T) {
	v1, err := os.ReadFile(filepath.Join("testdata", "agentsmd", "v1.md"))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(v1), "Commit in each repo", "Always commit in each repo", 1)
	for name, tc := range map[string]struct {
		content string
		want    AgentsMDState
	}{
		"current":            {scaffoldAgentsMD + "\nmine\n", AgentsMDOK},
		"edited old":         {edited, AgentsMDEdited},
		"never a template":   {"# My stack\n\n## Conventions\n\nours\n", AgentsMDOK},
		"truncated template": {string(v1[:100]), AgentsMDOK},
	} {
		if got, upgraded := CheckAgentsMD([]byte(tc.content)); got != tc.want || upgraded != nil {
			t.Errorf("%s: state = %v (upgrade %v), want %v and no upgrade", name, got, upgraded != nil, tc.want)
		}
	}
}

// The upgrade is a stack edit like any other: one commit of AGENTS.md alone,
// and an edited copy is refused rather than guessed at.
func TestUpgradeAgentsMD(t *testing.T) {
	c, _, _ := stackFixture(t)
	stack := c.FindStack("s")
	v1, err := os.ReadFile(filepath.Join("testdata", "agentsmd", "v1.md"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stack.Path, AgentsMDName)
	const own = "\n## This issue\n\nours\n"
	if err := os.WriteFile(path, append(v1, own...), 0644); err != nil {
		t.Fatal(err)
	}
	run(t, stack.Path, "commit", "-qam", "an old stack")
	if state, _ := StackAgentsMDCheck(stack); state != AgentsMDStale {
		t.Fatalf("check = %v, want stale", state)
	}

	res, err := c.UpgradeAgentsMD("s", ByHuman)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" {
		t.Fatal("no commit")
	}
	got, _ := os.ReadFile(path)
	if string(got) != scaffoldAgentsMD+own {
		t.Errorf("AGENTS.md =\n%s", got)
	}
	if out, _ := gitOutput(stack.Path, "show", "--name-only", "--format=", "HEAD"); out != AgentsMDName {
		t.Errorf("commit touched %q, want AGENTS.md alone", out)
	}
	if res, err := c.UpgradeAgentsMD("s", ByHuman); err != nil || res.Commit != "" {
		t.Errorf("second upgrade: %+v, %v; want nothing to do", res, err)
	}

	edited := strings.Replace(string(v1), "Commit in each repo", "Always commit in each repo", 1)
	if err := os.WriteFile(path, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	run(t, stack.Path, "commit", "-qam", "edited")
	if _, err := c.UpgradeAgentsMD("s", ByHuman); err == nil || !strings.Contains(err.Error(), "left alone") {
		t.Errorf("edited copy: err = %v, want it refused", err)
	}
	if got, _ := os.ReadFile(path); string(got) != edited {
		t.Error("an edited AGENTS.md was rewritten")
	}
}

// A freshly scaffolded stack is current.
func TestScaffoldedStackAgentsMDIsCurrent(t *testing.T) {
	c, _, _ := stackFixture(t)
	if state, err := StackAgentsMDCheck(c.FindStack("s")); err != nil || state != AgentsMDOK {
		t.Errorf("check = %v, %v; want current", state, err)
	}
}
