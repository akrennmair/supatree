package supatree

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/panamafrancis/workbench/pkg/setup"
)

// Codex does not hand the commands it runs its own environment: its
// [shell_environment_policy] builds a fresh one. A policy that leaves out
// SSH_AUTH_SOCK leaves a codex agent's git and ssh with no agent socket, so
// ssh falls back to reading the private key file, which the sandbox (rightly)
// refuses — "Load key ...: Operation not permitted" on every push. nono passes
// SSH_AUTH_SOCK through, so the fix is Codex's config, and doctor says so.

// codexShellPolicy is the part of Codex's config.toml that decides which
// environment variables a command Codex runs sees.
type codexShellPolicy struct {
	Inherit               string            `toml:"inherit"`
	IgnoreDefaultExcludes bool              `toml:"ignore_default_excludes"`
	Exclude               []string          `toml:"exclude"`
	IncludeOnly           []string          `toml:"include_only"`
	Set                   map[string]string `toml:"set"`
}

// codexCoreEnv is what inherit = "core" starts from.
var codexCoreEnv = []string{"HOME", "LOGNAME", "PATH", "SHELL", "USER", "USERNAME", "TMPDIR", "TEMP", "TMP"}

// codexDefaultExcludes are dropped unless ignore_default_excludes is set.
var codexDefaultExcludes = []string{"*KEY*", "*SECRET*", "*TOKEN*"}

// codexEnvMatch is Codex's case-insensitive glob match of a variable name.
func codexEnvMatch(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToUpper(p), strings.ToUpper(name)); ok {
			return true
		}
	}
	return false
}

// keeps reports whether a command Codex runs under p sees name, which is set
// in Codex's own environment, and if not, why. Codex builds the environment
// in this order: inherit, the default and configured excludes, set, then
// include_only over the result.
func (p codexShellPolicy) keeps(name string) (bool, string) {
	present := false
	switch p.Inherit {
	case "", "all":
		present = true
	case "core":
		present = codexEnvMatch(codexCoreEnv, name)
	}
	if present && !p.IgnoreDefaultExcludes && codexEnvMatch(codexDefaultExcludes, name) {
		present = false
	}
	if present && codexEnvMatch(p.Exclude, name) {
		return false, "exclude matches it"
	}
	if _, ok := p.Set[name]; ok {
		present = true
	}
	if !present {
		return false, fmt.Sprintf("inherit = %q does not include it", p.Inherit)
	}
	if len(p.IncludeOnly) > 0 && !codexEnvMatch(p.IncludeOnly, name) {
		return false, "include_only does not list it"
	}
	return true, ""
}

// codexConfigPath is Codex's config.toml: under $CODEX_HOME when that is
// absolute, else ~/.codex.
func codexConfigPath() string {
	if h := os.Getenv("CODEX_HOME"); filepath.IsAbs(h) {
		return filepath.Join(h, "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "config.toml")
}

// codexAgentEnv are the variables a codex agent's commands need from the
// environment supatree and nono launch it with, and what breaks without each.
// ifSet ones matter only when set where doctor runs: GOPATH unset means Go's
// default, which nothing needs passing to reach.
var codexAgentEnv = []struct {
	name, need string
	ifSet      bool
}{
	{"SSH_AUTH_SOCK", "git and ssh have no agent socket, fall back to reading the private key, and the sandbox refuses: pushes fail", false},
	{"NONO_CAP_FILE", "nono why --self reports NOT SANDBOXED, so the agent cannot tell a sandbox denial from any other", false},
	{"GOPATH", "Go falls back to ~/go, which the sandbox does not grant (it grants your GOPATH): no builds, tests or go install", true},
}

// CodexShellCheck reports whether the commands a codex agent runs see the
// variables in codexAgentEnv.
func CodexShellCheck() setup.CheckResult {
	const name = "codex shell environment"
	cfgPath := codexConfigPath()
	data, err := os.ReadFile(cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		return setup.CheckResult{Name: name, Status: setup.StatusOK, Message: "no Codex config: commands inherit everything"}
	}
	if err != nil {
		return setup.CheckResult{Name: name, Status: setup.StatusWarn, Message: err.Error()}
	}
	var doc struct {
		Policy codexShellPolicy `toml:"shell_environment_policy"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return setup.CheckResult{Name: name, Status: setup.StatusWarn, Message: fmt.Sprintf("%s: %v", cfgPath, err)}
	}
	var problems, missing []string
	for _, v := range codexAgentEnv {
		if v.ifSet && os.Getenv(v.name) == "" {
			continue
		}
		if ok, why := doc.Policy.keeps(v.name); !ok {
			problems = append(problems, fmt.Sprintf("%s (%s): %s", v.name, why, v.need))
			missing = append(missing, fmt.Sprintf("%q", v.name))
		}
	}
	if len(problems) > 0 {
		return setup.CheckResult{
			Name:    name,
			Status:  setup.StatusWarn,
			Message: "commands Codex runs do not see " + strings.Join(problems, "; "),
			Hint:    "in " + cfgPath + ", [shell_environment_policy]: inherit = \"all\" and add " + strings.Join(missing, ", ") + " to include_only (if you use one)",
		}
	}
	return setup.CheckResult{Name: name, Status: setup.StatusOK, Message: "commands Codex runs see what codex agents need"}
}
