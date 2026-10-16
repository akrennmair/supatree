package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

// The PM's configuration repo (#11). A PM's home is runtime state — mail,
// offsets, locks, a generated AGENTS.md — so nothing that shapes how a PM
// behaves could be committed, shared, or differ between PMs. A PM config repo
// is a git repository a human owns, registered as `pm_config`:
//
//	AGENTS.md               appended to every PM's generated instructions
//	.claude/                skills, commands, settings.json, hooks — linked
//	                        into the PM's home, so every PM loads them
//	pms/<name>/AGENTS.md    instructions for that PM alone
//	pms/<name>/settings.json  Claude settings for that PM alone (--settings)
//
// The PM reads it and never writes it: it lies outside every grant that allows
// writing, so a PM cannot edit the skills and settings that govern it — the
// same rule as never widening its own permissions.

// DefaultPMConfigPath is where `supatree pm config <url>` clones to.
func DefaultPMConfigPath() string {
	return filepath.Join(WorkspaceDir(), "pm-config")
}

// pmConfigDirName is the per-PM directory inside the config repo.
const pmConfigDirName = "pms"

// SetPMConfig registers dir as the PM config repo. It must be a git repository
// a PM cannot write: not under the stacks, the trees, or supatree's state.
func SetPMConfig(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !isGitRepo(abs) {
		return fmt.Errorf("%s is not a git repository — the PM config is versioned, so it has to be one", abs)
	}
	cfg, err := Load()
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	for _, writable := range []string{StacksDir(), cfg.ResolveTreesBase(), StateRoot()} {
		w, err := filepath.EvalSymlinks(writable)
		if err != nil {
			w = writable
		}
		if real == w || underDir(w, real) {
			return fmt.Errorf("%s is inside %s, which an agent can write — the PM config must be somewhere no PM can edit", abs, writable)
		}
	}
	for _, s := range cfg.Stacks {
		if s.Path != "" && (real == s.Path || underDir(s.Path, real)) {
			return fmt.Errorf("%s is inside stack %s, which the PM can write — keep the PM config in its own repo", abs, s.Alias)
		}
	}
	return withConfig(func(c *Config) error {
		c.PMConfig = abs
		return nil
	})
}

// withConfig edits the config under the registry lock.
func withConfig(edit func(*Config) error) error {
	return config.WithFileLock(LockPath(), func() error {
		c, err := Load()
		if err != nil {
			return err
		}
		if err := edit(c); err != nil {
			return err
		}
		return c.Save()
	})
}

// pmOverlay is what the config repo adds to the generated AGENTS.md: its own
// AGENTS.md, then each PM's section, headed with that PM's name — every PM
// reads the same file (they share a home), and each knows its own name from
// SUPATREE_PM_NAME.
func pmOverlay(repo string) string {
	var b strings.Builder
	if body, err := os.ReadFile(filepath.Join(repo, AgentsMDName)); err == nil && strings.TrimSpace(string(body)) != "" {
		fmt.Fprintf(&b, "\n\n<!-- From the PM config repo %s%s. Edit it there: this file is regenerated on every launch. -->\n\n", repo, pmConfigRev(repo))
		b.Write(body)
	}
	entries, _ := os.ReadDir(filepath.Join(repo, pmConfigDirName))
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(repo, pmConfigDirName, name, AgentsMDName))
		if err != nil || strings.TrimSpace(string(body)) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n\n## Only if you are the `%s` PM\n\n(Your name is in SUPATREE_PM_NAME. Every other PM skips this section.)\n\n", name)
		b.Write(body)
	}
	return b.String()
}

// pmConfigRev is " @ <short sha>" for the repo's HEAD, or "".
func pmConfigRev(repo string) string {
	if sha := PMConfigHead(repo); sha != "" {
		return " @ " + shortSHA(sha)
	}
	return ""
}

// PMConfigHead is the config repo's HEAD commit, or "" when it has none.
func PMConfigHead(repo string) string {
	sha, err := gitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return sha
}

// linkPMClaudeDir points the PM's home .claude at the config repo's, so its
// skills, commands and settings are what every PM loads. A real .claude
// directory already there — settings someone made by hand — is left alone
// and reported, never replaced.
func linkPMClaudeDir(repo string) error {
	src := filepath.Join(repo, ".claude")
	dst := filepath.Join(PMDir(), ".claude")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil // the repo carries no .claude: nothing to link
	} else if err != nil {
		return err
	}
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s is a directory of its own, so the PM config's .claude was not linked — move what is in it into %s", dst, src)
		}
		if cur, _ := os.Readlink(dst); cur == src {
			return nil
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	return os.Symlink(src, dst)
}

// SetPMConfigAt records the config repo commit pm was launched with, so
// `pm ls` can say when the PM is running an older one.
func SetPMConfigAt(name, sha string) error {
	return config.WithFileLock(pmsLockPath(), func() error {
		pms, err := LoadPMs()
		if err != nil {
			return err
		}
		for i := range pms {
			if pms[i].Name == name {
				pms[i].ConfigAt = sha
			}
		}
		return savePMs(pms)
	})
}

// pmSettingsArgs are the extra launch arguments for pm: its own settings file
// from the config repo, for a claude model.
func pmSettingsArgs(repo, binary string, pm PM) []string {
	if repo == "" || filepath.Base(binary) != "claude" {
		return nil
	}
	path := filepath.Join(repo, pmConfigDirName, pm.Name, "settings.json")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return []string{"--settings", path}
}
