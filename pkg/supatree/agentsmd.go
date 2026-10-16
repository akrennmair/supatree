package supatree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/panamafrancis/workbench/pkg/setup"
)

// Stacks scaffolded before info.md carried supatree's conventions got a copy
// of them in AGENTS.md, which supatree never touched again — so each stack
// kept whatever the conventions were the day it was made (#6: a stack whose
// copy predated named PMs, read by an agent that then could not reach one).
//
// A stack's AGENTS.md is the human's file, so it is only ever rewritten when
// it provably holds an unedited old template: its leading bytes hash to one
// that shipped. Whatever the human appended below the template's last line is
// kept. A copy that was edited is reported and left alone.

// agentsMDTail is the scaffold's last line, under which a stack's own
// instructions go.
const agentsMDTail = "Add this stack's own instructions below."

// priorAgentsMD is every scaffoldAgentsMD that has shipped, oldest first, by
// length and SHA-256. When the scaffold changes, add the outgoing one here —
// TestScaffoldAgentsMDIsRegistered fails until you do.
var priorAgentsMD = []struct {
	size int
	sum  string
}{
	{754, "e3f4ad2d6c02b5061122acb7f437131f53b8a1d44af822c96d02e18a7cf8eb0b"},  // 9a5bcc8, the first
	{957, "ec3647102dab55b47fef768277b49f6ec1741ac27f25b229ace0a73da5dfc9bb"},  // members: URLs, scripts/setup
	{1276, "bf19ade95f47422636e34aa8aff5cd568092d0b13a14001339958706dac8035f"}, // PM addresses (#6)
}

// AgentsMDState is how a stack's AGENTS.md relates to the scaffold.
type AgentsMDState int

const (
	// AgentsMDOK is the current scaffold, or a file that never came from one.
	AgentsMDOK AgentsMDState = iota
	// AgentsMDStale is an unedited old scaffold, possibly with the stack's own
	// instructions below it: UpgradeAgentsMD can replace it safely.
	AgentsMDStale
	// AgentsMDEdited still carries a copy of supatree's conventions, but not
	// one that shipped, so only a human can say which parts are theirs.
	AgentsMDEdited
)

// CheckAgentsMD classifies an AGENTS.md. For a stale one it also returns the
// upgraded content: the current scaffold followed by everything the old one
// had below its template.
func CheckAgentsMD(content []byte) (AgentsMDState, []byte) {
	if bytes.HasPrefix(content, []byte(scaffoldAgentsMD)) {
		return AgentsMDOK, nil
	}
	for _, p := range priorAgentsMD {
		if len(content) < p.size {
			continue
		}
		sum := sha256.Sum256(content[:p.size])
		if hex.EncodeToString(sum[:]) == p.sum {
			return AgentsMDStale, append([]byte(scaffoldAgentsMD), content[p.size:]...)
		}
	}
	// Every old scaffold had a Conventions section under this heading; the
	// current one has neither the section nor anything to go stale.
	s := string(content)
	if strings.HasPrefix(s, "# Supatree agent guide") && strings.Contains(s, "\n## Conventions\n") {
		return AgentsMDEdited, nil
	}
	return AgentsMDOK, nil
}

// maxAgentsMD bounds how much of an AGENTS.md is read: far more than any
// scaffold, which is all classifying one needs to see in full.
const maxAgentsMD = 1 << 20

// readAgentsMD reads a stack's AGENTS.md, refusing anything but a regular
// file and stopping at maxAgentsMD. The checkout is agent-writable: a FIFO
// there would hang doctor, and a link to /dev/zero would read forever. The
// check is made on the open descriptor, not the path, so the file cannot be
// swapped between them: O_NOFOLLOW refuses a link, and O_NONBLOCK keeps
// opening a FIFO from waiting for a writer.
func readAgentsMD(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%s is a symlink", path)
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAgentsMD+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAgentsMD {
		return nil, fmt.Errorf("%s is over %d bytes", path, maxAgentsMD)
	}
	return data, nil
}

// StackAgentsMDCheck reports on a stack checkout's AGENTS.md. It reads the
// checkout, which agents can write, and only to classify it: nothing there is
// run or trusted.
func StackAgentsMDCheck(stack *Stack) (AgentsMDState, error) {
	data, err := readAgentsMD(filepath.Join(stack.Path, AgentsMDName))
	if os.IsNotExist(err) {
		return AgentsMDOK, nil
	}
	if err != nil {
		return AgentsMDOK, err
	}
	state, _ := CheckAgentsMD(data)
	return state, nil
}

// UpgradeAgentsMD replaces a stack's unedited old scaffold with the current
// one, as one commit published like any other stack edit. An edited copy is
// refused: which of its lines are the human's is not something to guess.
func (c *Config) UpgradeAgentsMD(stackAlias string, mode StackEditMode) (StackEditResult, error) {
	stack := c.FindStack(stackAlias)
	if stack == nil {
		return StackEditResult{}, fmt.Errorf("stack %q not found", stackAlias)
	}
	return c.editStackFile(stack, mode, AgentsMDName, "AGENTS.md: point at the generated info.md (supatree stack upgrade)", func(dir string) error {
		path := filepath.Join(dir, AgentsMDName)
		data, err := readAgentsMD(path)
		if os.IsNotExist(err) {
			return errNoChange
		}
		if err != nil {
			return err
		}
		state, upgraded := CheckAgentsMD(data)
		switch state {
		case AgentsMDStale:
			return os.WriteFile(path, upgraded, 0644)
		case AgentsMDEdited:
			return fmt.Errorf("%s in stack %s has an edited copy of supatree's old conventions, so it was left alone. "+
				"Those conventions are now generated into each tree's .supatree/info.md: delete the supatree section by hand, keeping your own instructions", AgentsMDName, stack.Alias)
		case AgentsMDOK:
		}
		return errNoChange
	})
}

// AgentsMDChecks is doctor's report on every registered stack's AGENTS.md.
func AgentsMDChecks(cfg *Config) []setup.CheckResult {
	var results []setup.CheckResult
	for i := range cfg.Stacks {
		stack := &cfg.Stacks[i]
		name := "stack " + stack.Alias + " " + AgentsMDName
		state, err := StackAgentsMDCheck(stack)
		switch {
		case err != nil:
			results = append(results, setup.CheckResult{Name: name, Status: setup.StatusWarn, Message: err.Error()})
		case state == AgentsMDStale:
			results = append(results, setup.CheckResult{Name: name, Status: setup.StatusWarn,
				Message: "an unedited copy of an older template, with conventions that have since changed",
				Hint:    "supatree stack upgrade " + stack.Alias + " (keeps what you added below the template)"})
		case state == AgentsMDEdited:
			results = append(results, setup.CheckResult{Name: name, Status: setup.StatusWarn,
				Message: "an edited copy of supatree's old conventions, which may be out of date",
				Hint:    "they are now generated into each tree's .supatree/info.md: delete the supatree section by hand, keeping your own instructions"})
		default:
			results = append(results, setup.CheckResult{Name: name, Status: setup.StatusOK, Message: "current"})
		}
	}
	return results
}
