package supatree

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

func dirtyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.CommandContext(context.Background(), "git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeSnapshot(t *testing.T, snap dirtySnapshot) {
	t.Helper()
	data, _ := json.Marshal(snap)
	if err := os.MkdirAll(filepath.Dir(DirtyCachePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DirtyCachePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A fresh snapshot is believed without running git; a stale one is re-checked
// and republished; force re-checks a fresh one.
func TestDirtyMembersSharesOneSnapshot(t *testing.T) {
	testutil.IsolateHome(t)
	repo := dirtyRepo(t)
	insts := []*Instance{{Name: "lima", Members: []Member{{Alias: "api", Path: repo, Exists: true}}}}

	writeSnapshot(t, dirtySnapshot{At: time.Now(), Checked: map[string]bool{repo: false}})
	if got := DirtyMembers(insts, time.Minute, false); got[repo] {
		t.Fatal("fresh snapshot was not used: git was run")
	}
	if got := DirtyMembers(insts, time.Minute, true); !got[repo] {
		t.Fatal("force did not re-check")
	}

	writeSnapshot(t, dirtySnapshot{At: time.Now().Add(-time.Hour), Checked: map[string]bool{repo: false}})
	if got := DirtyMembers(insts, time.Minute, false); !got[repo] {
		t.Fatal("stale snapshot was not re-checked")
	}
	if snap := readDirtySnapshot(); snap == nil || !snap.Checked[repo] || time.Since(snap.At) > time.Minute {
		t.Fatalf("re-check was not published: %+v", snap)
	}
}

// A member the snapshot never saw is checked directly rather than read as clean.
func TestDirtyMembersChecksUnseenPaths(t *testing.T) {
	testutil.IsolateHome(t)
	repo := dirtyRepo(t)
	writeSnapshot(t, dirtySnapshot{At: time.Now(), Checked: map[string]bool{}})
	insts := []*Instance{{Name: "lima", Members: []Member{{Alias: "api", Path: repo, Exists: true}}}}
	if got := DirtyMembers(insts, time.Minute, false); !got[repo] {
		t.Fatal("a member missing from the snapshot was reported clean")
	}
}
