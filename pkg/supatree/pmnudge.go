package supatree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// PMNudgeText is what the sidebar types into a PM's prompt when mail arrives.
// It is fixed on purpose, like KickoffPrompt: the requests themselves ride in
// the queue, so nothing anyone queued ever reaches a command line or a prompt.
const PMNudgeText = "You got mail."

// nudgeEnterDelay separates the text from the Enter that submits it. Sent back
// to back, the two can arrive as one burst that the agent's input treats as a
// paste — leaving a newline in the prompt instead of submitting it.
const nudgeEnterDelay = 150 * time.Millisecond

// Seams for tests: the zellij CLI and the pause.
var (
	runZellijAction = func(args ...string) ([]byte, error) {
		return exec.Command("zellij", append([]string{"action"}, args...)...).Output()
	}
	inZellij   = zellij.IsInZellij
	nudgeSleep = time.Sleep
)

// zellijPane is the part of `zellij action list-panes --json` we read.
type zellijPane struct {
	ID       int    `json:"id"`
	IsPlugin bool   `json:"is_plugin"`
	Title    string `json:"title"`
	Exited   bool   `json:"exited"`
	TabName  string `json:"tab_name"`
}

func parsePanes(data []byte) ([]zellijPane, error) {
	var panes []zellijPane
	if err := json.Unmarshal(data, &panes); err != nil {
		return nil, fmt.Errorf("parse list-panes: %w", err)
	}
	return panes, nil
}

// pmPaneID finds the agent pane of the tab named tab, as a --pane-id argument.
// The tab layout names the agent pane after its tab and the other pane
// "sidebar"; a pane whose title has since changed is still the agent if it is
// the only other live terminal there.
func pmPaneID(panes []zellijPane, tab string) (string, bool) {
	var others []zellijPane
	for _, p := range panes {
		if p.IsPlugin || p.Exited || p.TabName != tab {
			continue
		}
		if p.Title == tab {
			return "terminal_" + strconv.Itoa(p.ID), true
		}
		if p.Title != "sidebar" {
			others = append(others, p)
		}
	}
	if len(others) == 1 {
		return "terminal_" + strconv.Itoa(others[0].ID), true
	}
	return "", false
}

// nudgeState maps a PM name to the time of the newest request it was nudged
// about. Keying on the PM's own newest request means mail for another PM never
// re-nudges it, a rotated queue cannot confuse it, and a drained queue has no
// pending request to compare.
type nudgeState map[string]time.Time

func shouldNudge(state nudgeState, pm string, pending []Request) bool {
	if len(pending) == 0 {
		return false
	}
	return pending[len(pending)-1].At.After(state[pm])
}

// NudgePMs types PMNudgeText + Enter into the pane of every PM that has mail
// it has not been nudged about, so it starts a turn and drains `requests`
// rather than waiting for a human to say something.
//
// One sidebar runs per tab, so the round is elected by a try-lock and recorded
// in PMNudgeStatePath: a busy lock means another sidebar is doing it. A PM whose
// tab is not in this session is skipped — and retried next round, since its
// state is not advanced. A focused tab is not skipped: the nudge can land in a
// draft the human is typing, which is why the whole thing is opt-in
// (Config.PMNudge). Telling focus apart from the outside proved unreliable.
func NudgePMs(pms []PM, pending map[string][]Request) error {
	if !inZellij() {
		return nil
	}
	err := config.TryFileLock(PMNudgeLockPath(), func() error {
		state := nudgeState{}
		_ = readJSON(PMNudgeStatePath(), &state) // missing or torn: at most one extra nudge
		var due []PM
		for _, p := range pms {
			if shouldNudge(state, p.Name, pending[p.Name]) {
				due = append(due, p)
			}
		}
		if len(due) == 0 {
			return nil
		}
		out, err := runZellijAction("list-panes", "--json", "--tab")
		if err != nil {
			return fmt.Errorf("list panes: %w", err)
		}
		panes, err := parsePanes(out)
		if err != nil {
			return err
		}
		var errs []error
		changed := false
		for _, p := range due {
			id, ok := pmPaneID(panes, p.Tab())
			if !ok {
				continue
			}
			if err := typeInto(id, PMNudgeText); err != nil {
				errs = append(errs, fmt.Errorf("nudge %s: %w", p.Name, err))
				continue
			}
			reqs := pending[p.Name]
			state[p.Name] = reqs[len(reqs)-1].At
			changed = true
		}
		if changed {
			errs = append(errs, writeJSONAtomic(PMNudgeStatePath(), state))
		}
		return errors.Join(errs...)
	})
	if errors.Is(err, config.ErrLockBusy) {
		return nil
	}
	return err
}

// typeInto writes text into a pane and submits it, without focusing the pane.
func typeInto(paneID, text string) error {
	if _, err := runZellijAction("write-chars", "--pane-id", paneID, text); err != nil {
		return err
	}
	nudgeSleep(nudgeEnterDelay)
	_, err := runZellijAction("send-keys", "--pane-id", paneID, "Enter")
	return err
}
