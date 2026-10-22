package supatree

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// agentExecs judges every pid from one ps: a process whose command names
// agent-exec counts, the test binary itself does not.
func TestAgentExecsOnePS(t *testing.T) {
	if err := exec.CommandContext(context.Background(), "ps", "-p", "1").Run(); err != nil {
		t.Skipf("ps is not usable here (inside a sandbox?): %v", err)
	}
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "sleep 30; :", "agent-exec")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	probe := cmd.Process.Pid

	got := agentExecs([]int{probe, os.Getpid()})
	if !got[probe] {
		t.Errorf("agentExecs missed the agent-exec probe (pid %d): %v", probe, got)
	}
	if got[os.Getpid()] {
		t.Errorf("agentExecs counted the test binary: %v", got)
	}
	if len(agentExecs(nil)) != 0 {
		t.Error("agentExecs(nil) is not empty")
	}
}
