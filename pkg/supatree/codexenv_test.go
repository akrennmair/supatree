package supatree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/panamafrancis/workbench/pkg/setup"
	"github.com/panamafrancis/workbench/pkg/testutil"
)

const sshSock = "SSH_AUTH_SOCK"

// Codex's shell policy decides whether a codex agent's git and ssh can reach
// the SSH agent. The "core" case is the regression: a policy that lists
// SSH_AUTH_SOCK in include_only but inherits only the core variables still
// drops it, and every push then fails reading the private key.
func TestCodexShellPolicyKeepsSSHAuthSock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy codexShellPolicy
		keeps  bool
	}{
		{"default policy", codexShellPolicy{}, true},
		{"inherit all", codexShellPolicy{Inherit: "all"}, true},
		{"inherit core", codexShellPolicy{Inherit: "core"}, false},
		{"inherit none", codexShellPolicy{Inherit: "none"}, false},
		{"core, listed in include_only", codexShellPolicy{Inherit: "core", IncludeOnly: []string{"PATH", sshSock}}, false},
		{"all, include_only without it", codexShellPolicy{Inherit: "all", IncludeOnly: []string{"PATH", "HOME"}}, false},
		{"all, include_only with it", codexShellPolicy{Inherit: "all", IncludeOnly: []string{"PATH", sshSock}}, true},
		{"include_only glob", codexShellPolicy{IncludeOnly: []string{"ssh_*"}}, true},
		{"excluded", codexShellPolicy{Exclude: []string{"SSH_*"}}, false},
		{"set under core", codexShellPolicy{Inherit: "core", Set: map[string]string{sshSock: "/s"}}, true},
		{"set, filtered by include_only", codexShellPolicy{Set: map[string]string{sshSock: "/s"}, IncludeOnly: []string{"PATH"}}, false},
	} {
		if got, _ := tc.policy.keeps(sshSock); got != tc.keeps {
			t.Errorf("%s: keeps = %v, want %v", tc.name, got, tc.keeps)
		}
	}
}

func TestCodexShellCheckReadsConfig(t *testing.T) {
	home := testutil.IsolateHome(t)
	codexHome := filepath.Join(home, "codex")
	t.Setenv("CODEX_HOME", codexHome)
	if r := CodexShellCheck(); r.Status != setup.StatusOK {
		t.Errorf("no config: %+v", r)
	}
	if err := os.MkdirAll(codexHome, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("model = \"x\"\n[shell_environment_policy]\ninherit = \"core\"\ninclude_only = [\"PATH\", \"HOME\", \"SSH_AUTH_SOCK\"]\n")
	if r := CodexShellCheck(); r.Status != setup.StatusWarn || r.Hint == "" {
		t.Errorf("core policy: %+v, want a warning with a hint", r)
	}
	write("[shell_environment_policy]\ninherit = \"all\"\ninclude_only = [\"PATH\", \"HOME\", \"SSH_AUTH_SOCK\"]\n")
	if r := CodexShellCheck(); r.Status != setup.StatusOK {
		t.Errorf("fixed policy: %+v", r)
	}
}
