package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
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
	cfg, err := Load()
	if err != nil {
		return err
	}
	real, err := checkPMConfig(cfg, dir)
	if err != nil {
		return err
	}
	return withConfig(func(c *Config) error {
		c.PMConfig = real
		return nil
	})
}

// checkPMConfig resolves dir and refuses it unless it is a git repository
// outside everything an agent can write. Both sides are compared resolved, and
// the check runs again at every launch (usablePMConfig): a link changed after
// registration must not hand the PMs a config an agent wrote.
func checkPMConfig(cfg *Config, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !isGitRepo(real) {
		return "", fmt.Errorf("%s is not a git repository — the PM config is versioned, so it has to be one", abs)
	}
	writable := []string{StacksDir(), cfg.ResolveTreesBase(), StateRoot(), CacheDir()}
	for _, s := range cfg.Stacks {
		if s.Path != "" {
			writable = append(writable, s.Path)
		}
	}
	for _, w := range writable {
		rw, err := filepath.EvalSymlinks(w)
		if err != nil {
			rw = filepath.Clean(w)
		}
		if real == rw || underDir(rw, real) || underDir(real, rw) {
			return "", fmt.Errorf("%s overlaps %s, which an agent can write — the PM config must be somewhere no PM can edit", abs, w)
		}
	}
	return real, nil
}

// usablePMConfig is the registered PM config repo, re-checked, or "" — with
// the refusal logged — when it no longer passes.
func usablePMConfig(cfg *Config) string {
	if cfg == nil || cfg.PMConfig == "" {
		return ""
	}
	real, err := checkPMConfig(cfg, cfg.PMConfig)
	if err != nil {
		logLaunch("PM config", PMTab, "not used: "+err.Error())
		return ""
	}
	return real
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
// skills, commands and settings are what every PM loads. The PM's home is the
// PM's to write, so whatever else stands there — a directory made by hand, or
// by a PM giving itself skills of its own — is moved aside (kept, under a
// timestamped name) and the link restored on every launch: with a config repo
// registered, that repo is what governs the PMs.
func linkPMClaudeDir(repo string) error {
	src := filepath.Join(repo, ".claude")
	dst := filepath.Join(PMDir(), ".claude")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil // the repo carries no .claude: nothing to link
	} else if err != nil {
		return err
	}
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if cur, _ := os.Readlink(dst); cur == src {
				return nil
			}
			if err := os.Remove(dst); err != nil {
				return err
			}
		} else {
			aside := dst + ".moved-" + time.Now().UTC().Format("20060102-150405")
			if err := os.Rename(dst, aside); err != nil {
				return err
			}
			logLaunch("PM config", PMTab, fmt.Sprintf("moved %s aside to %s; the PM config repo's .claude governs the PMs — merge what you need into %s", dst, aside, src))
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
//
// The name comes from pms.yml, which sits in the PM's own (writable) home, so
// it is validated and the path held inside the repo's pms/ — a name like
// "../../x" must not point --settings at a file a PM wrote.
func pmSettingsArgs(repo, binary string, pm PM) []string {
	if repo == "" || filepath.Base(binary) != "claude" || git.ValidateName(pm.Name, nil) != nil {
		return nil
	}
	base := filepath.Join(repo, pmConfigDirName)
	path := filepath.Join(base, pm.Name, "settings.json")
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil || !underDir(realBase, real) {
		return nil
	}
	return []string{"--settings", real}
}
