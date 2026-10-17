package supatree

import (
	"os"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// TestMain points HOME and the XDG dirs at a throwaway directory for the whole
// binary, so a test that forgets to isolate itself still cannot write into the
// real ~/.config, ~/.local/state or ~/.cache.
//
// Git that supatree confines (gitjail.go) runs unconfined here, as it would
// inside a sandbox already: tests cannot rely on an enforcing nono, nor on the
// agents' profile being installed. gitjail_test.go covers the confinement.
func TestMain(m *testing.M) {
	insideSandbox = func() bool { return true }
	cleanup := testutil.IsolateProcess()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
