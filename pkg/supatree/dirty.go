package supatree

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

// DirtyMaxAge is how old the shared dirty snapshot may be before a sidebar
// re-checks it. Just under the sidebar's tick, so one of them refreshes it
// each round and the rest read what it wrote.
const DirtyMaxAge = 25 * time.Second

// dirtySnapshot is which member worktrees had uncommitted changes, and when
// that was checked. Paths missing from Checked were not looked at.
type dirtySnapshot struct {
	At      time.Time       `json:"at"`
	Checked map[string]bool `json:"checked"` // member path -> dirty
}

// DirtyCachePath holds the snapshot every sidebar shares.
func DirtyCachePath() string {
	return filepath.Join(CacheDir(), "dirty.json")
}

func dirtyLockPath() string {
	return DirtyCachePath() + ".lock"
}

// DirtyMembers reports which existing members of insts have uncommitted
// changes, keyed by member path.
//
// One sidebar runs per Zellij tab, and each used to run `git status` in every
// member of every tree on every tick: with a few dozen tabs and trees that is
// hundreds of git processes every half minute, the load that made the whole
// session lag once review trees piled up. Now one process checks per round and
// the others read its snapshot — the PR cache's single-writer discipline.
//
// A snapshot younger than maxAge is used as it is; otherwise the caller checks
// under a try-lock and publishes, or, finding the lock busy, uses the snapshot
// it has. force checks regardless, waiting for the lock: someone asked.
// Members the snapshot never saw (a tree created since) are checked directly.
func DirtyMembers(insts []*Instance, maxAge time.Duration, force bool) map[string]bool {
	paths := existingMemberPaths(insts)
	snap := readDirtySnapshot()
	if !force && snap != nil && time.Since(snap.At) < maxAge {
		return dirtyFor(paths, snap)
	}
	refresh := func() error {
		snap = checkDirty(paths)
		data, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		return writeFileAtomic(DirtyCachePath(), data)
	}
	// Whatever the lock did, snap is the best there is: a fresh check, or the
	// stale snapshot when another process holds the round. With none at all,
	// dirtyFor checks every path itself.
	if force {
		_ = config.WithFileLock(dirtyLockPath(), refresh)
	} else {
		_ = config.TryFileLock(dirtyLockPath(), refresh)
	}
	return dirtyFor(paths, snap)
}

func existingMemberPaths(insts []*Instance) []string {
	var paths []string
	for _, inst := range insts {
		for _, mem := range inst.Members {
			if mem.Exists {
				paths = append(paths, mem.Path)
			}
		}
	}
	return paths
}

func checkDirty(paths []string) *dirtySnapshot {
	snap := &dirtySnapshot{At: time.Now(), Checked: make(map[string]bool, len(paths))}
	for _, p := range paths {
		snap.Checked[p] = git.IsDirty(p)
	}
	return snap
}

// dirtyFor answers for paths from snap, checking any it does not cover.
func dirtyFor(paths []string, snap *dirtySnapshot) map[string]bool {
	out := map[string]bool{}
	for _, p := range paths {
		dirty, seen := false, false
		if snap != nil {
			dirty, seen = snap.Checked[p]
		}
		if !seen {
			dirty = git.IsDirty(p)
		}
		if dirty {
			out[p] = true
		}
	}
	return out
}

func readDirtySnapshot() *dirtySnapshot {
	var snap dirtySnapshot
	if err := readJSON(DirtyCachePath(), &snap); err != nil || snap.Checked == nil {
		return nil
	}
	return &snap
}
