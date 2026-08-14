package main

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
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

func TestWaitProcessGoneAfterTerm(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
	})
	server := &trackedServer{proc: cmd.Process, pgid: cmd.Process.Pid}
	if err := killProcessGroup(server); err != nil {
		t.Fatal(err)
	}
	waitProcessGone(server, 2*time.Second)
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("process should be gone after wait")
	}
}
