package supacmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// A repeating error must not grow watch.log without bound: it rotates to one
// previous generation, and the line that tripped the rotation is not lost.
func TestWatchLogRotates(t *testing.T) {
	testutil.IsolateHome(t)
	openWatchLog()
	defer closeWatchLog()
	if watchLog.f == nil {
		t.Fatal("watch log did not open")
	}

	line := strings.Repeat("x", 1000)
	for i := 0; i < (watchLogMax/1000)+50; i++ {
		logWatch("%s", line)
	}
	logWatch("last line")

	cur, err := os.ReadFile(watchLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) > watchLogMax {
		t.Errorf("watch.log is %d bytes, over the %d cap", len(cur), watchLogMax)
	}
	if !strings.Contains(string(cur), "last line") {
		t.Error("the newest line is not in the current log")
	}
	if st, err := os.Stat(watchLogPath() + ".1"); err != nil || st.Size() == 0 {
		t.Errorf("no previous generation kept (%v)", err)
	}
}

// An upgraded binary restarts the watcher, but only once it has stopped
// changing: a binary still being copied must not be exec'd.
func TestBinaryWatchWaitsForAStableReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supatree")
	write := func(body string, mod time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-time.Hour)
	write("old", base)
	stamp, _ := statBinary(path)
	w := &binaryWatch{path: path, stamp: stamp}

	if w.replaced() {
		t.Fatal("an unchanged binary is not a replacement")
	}
	write("new, half", base.Add(time.Minute))
	if w.replaced() {
		t.Fatal("restarted on the first sight of a change")
	}
	write("new, complete", base.Add(2*time.Minute))
	if w.replaced() {
		t.Fatal("restarted while the binary was still changing")
	}
	if !w.replaced() {
		t.Fatal("a replacement stable across two checks should restart the watcher")
	}
}
