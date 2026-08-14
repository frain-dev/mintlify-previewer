package main

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestKillProcessGroupSkipsExitedPid(t *testing.T) {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	err := killProcessGroup(&trackedServer{proc: cmd.Process, pgid: cmd.Process.Pid})
	if err != nil {
		t.Fatalf("dead pid should be a no-op, got %v", err)
	}
}

func TestKillProcessGroupNilServer(t *testing.T) {
	if err := killProcessGroup(nil); err != nil {
		t.Fatal(err)
	}
}
