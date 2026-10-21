package supatree

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// keepFocus puts the focus back on prev whenever the launched tab takes it —
// including late, as zellij does once it has applied the new tab's layout —
// and never moves it off a tab the human has gone to since.
func TestKeepFocus(t *testing.T) {
	origFocused, origGoTo, origSettle, origPoll := focusedTab, goToTab, focusSettle, focusPoll
	t.Cleanup(func() { focusedTab, goToTab, focusSettle, focusPoll = origFocused, origGoTo, origSettle, origPoll })
	focusSettle, focusPoll = 30*time.Millisecond, time.Millisecond

	// The launched tab has the focus, gets it back once after the restore,
	// then the human moves to a third tab.
	reads := []string{"lima", "lima", "pm", "elsewhere"}
	var gone []string
	focusedTab = func() string {
		if len(reads) == 0 {
			return "elsewhere"
		}
		cur := reads[0]
		reads = reads[1:]
		return cur
	}
	goToTab = func(name string) error {
		gone = append(gone, name)
		return nil
	}
	keepFocus("pm", "lima")
	if len(gone) != 2 || gone[0] != "pm" || gone[1] != "pm" {
		t.Fatalf("went to %v, want pm twice (once per time lima had the focus)", gone)
	}
}

// prevTab asks current-tab-info, falls back to the dumped layout, and retries
// before giving up — and then says what each attempt got.
func TestPrevTab(t *testing.T) {
	origFocused, origCurrent, origTries, origRetry := focusedTab, currentTab, prevTabTries, prevTabRetry
	t.Cleanup(func() {
		focusedTab, currentTab, prevTabTries, prevTabRetry = origFocused, origCurrent, origTries, origRetry
	})
	prevTabRetry = time.Millisecond

	calls := 0
	currentTab = func() (string, error) {
		calls++
		if calls < 2 {
			return "", errors.New("busy")
		}
		return "pm", nil
	}
	focusedTab = func() string { return "" }
	if got, why := prevTab(); got != "pm" || why != "" {
		t.Errorf("prevTab = %q, %q; want pm on the retry", got, why)
	}

	currentTab = func() (string, error) { return "", errors.New("busy") }
	focusedTab = func() string { return "layout-pm" }
	if got, _ := prevTab(); got != "layout-pm" {
		t.Errorf("prevTab = %q, want the layout's answer when current-tab-info fails", got)
	}

	focusedTab = func() string { return "" }
	if got, why := prevTab(); got != "" || !strings.Contains(why, "busy") || !strings.Contains(why, "dump-layout") {
		t.Errorf("prevTab = %q, %q; want nothing, and why naming both reads", got, why)
	}
}
