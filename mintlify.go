package main

import (
	"mintlify-previewer-backend/log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type trackedServer struct {
	proc *os.Process
	pgid int
}

var activeServers = make(map[string]*trackedServer)
var mu sync.Mutex

func ensureMintlifyInstalled() error {
	if _, err := exec.LookPath("mintlify"); err != nil {
		log.Errorln("Mintlify not found, installing...")
		cmd := exec.Command("npm", "install", "-g", "mintlify")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return nil
}

func startMintlifyDev(uuid string, port int, dir string) {
	if !isDeploymentActive(db, uuid) {
		return
	}

	cmd := exec.Command("mintlify", "dev", "--no-open", "--port", strconv.Itoa(port))
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		log.Errorf("Failed to start Mintlify: %v", err)
		setFailedIfActive(db, uuid, err.Error())
		return
	}

	// Setpgid makes the child's pid the new process group. Record it now so
	// teardown never Getpgid's a pid that may have been reused.
	mu.Lock()
	activeServers[uuid] = &trackedServer{proc: cmd.Process, pgid: cmd.Process.Pid}
	mu.Unlock()

	log.Infof("Mintlify running for UUID %s on port %d", uuid, port)
	if !setStatusIfActive(db, uuid, "running") {
		stopMintlifyProcess(uuid)
		return
	}

	err := cmd.Wait()
	if err != nil {
		log.Errorf("Failed to start Mintlify: %v", err)
	}

	mu.Lock()
	delete(activeServers, uuid)
	mu.Unlock()
}

func stopMintlifyProcess(uuid string) {
	mu.Lock()
	server, exists := activeServers[uuid]
	if exists {
		delete(activeServers, uuid)
	}
	mu.Unlock()

	if !exists {
		return
	}

	if err := killProcessGroup(server); err != nil {
		log.Errorf("Failed to stop Mintlify server for %s: %v", uuid, err)
	}
	waitProcessGone(server, 5*time.Second)
}

func killProcessGroup(server *trackedServer) error {
	if server == nil || server.proc == nil {
		return nil
	}
	// Fail closed on strangers: if this pid is already gone, do not signal a
	// process group that may now belong to an unrelated process.
	if err := server.proc.Signal(syscall.Signal(0)); err != nil {
		return nil
	}
	if server.pgid > 0 {
		if err := syscall.Kill(-server.pgid, syscall.SIGTERM); err != nil {
			return server.proc.Signal(syscall.SIGTERM)
		}
		return nil
	}
	return server.proc.Signal(syscall.SIGTERM)
}

func waitProcessGone(server *trackedServer, d time.Duration) {
	if server == nil || server.proc == nil {
		return
	}
	pid := server.proc.Pid
	deadline := time.Now().Add(d)
	for {
		if processReaped(pid) {
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if server.pgid > 0 {
		_ = syscall.Kill(-server.pgid, syscall.SIGKILL)
	} else {
		_ = server.proc.Signal(syscall.SIGKILL)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if processReaped(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func processReaped(pid int) bool {
	var status syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err == syscall.ECHILD {
		return true
	}
	return wpid == pid
}
