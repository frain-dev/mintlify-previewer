package main

import (
	"bytes"
	"mintlify-previewer-backend/log"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	beginInflight(uuid)
	active, err := deploymentActive(db, uuid)
	if err != nil {
		endInflight(uuid)
		log.Errorf("Failed to check deployment %s: %v", uuid, err)
		return
	}
	if !active {
		endInflight(uuid)
		return
	}

	cmd := exec.Command("mintlify", "dev", "--no-open", "--port", strconv.Itoa(port))
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		endInflight(uuid)
		log.Errorf("Failed to start Mintlify: %v", err)
		_, _ = setFailedIfActive(db, uuid, err.Error())
		return
	}

	// Setpgid makes the child's pid the new process group. Record it now so
	// teardown never Getpgid's a pid that may have been reused.
	mu.Lock()
	activeServers[uuid] = &trackedServer{proc: cmd.Process, pgid: cmd.Process.Pid}
	mu.Unlock()
	endInflight(uuid)

	log.Infof("Mintlify running for UUID %s on port %d", uuid, port)
	ok, err := setStatusIfActive(db, uuid, "running")
	for i := 0; err != nil && i < 3; i++ {
		time.Sleep(50 * time.Millisecond)
		ok, err = setStatusIfActive(db, uuid, "running")
	}
	if err != nil {
		// Unknown is not cancelled: keep the process we just started.
		log.Errorf("Failed to mark running for %s: %v", uuid, err)
	} else if !ok {
		stopMintlifyProcess(uuid)
		_ = cmd.Wait()
		return
	}

	if err := cmd.Wait(); err != nil {
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
		return syscall.Kill(-server.pgid, syscall.SIGTERM)
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
		reapGroupZombies(server.pgid, pid)
		if processGroupGone(server.pgid, pid) {
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if server.pgid > 0 && !processGroupGone(server.pgid, pid) {
		_ = syscall.Kill(-server.pgid, syscall.SIGKILL)
	} else if server.pgid <= 0 {
		_ = server.proc.Signal(syscall.SIGKILL)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		reapGroupZombies(server.pgid, pid)
		if processGroupGone(server.pgid, pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	reapGroupZombies(server.pgid, pid)
}

func processGroupGone(pgid, leaderPid int) bool {
	if pgid <= 0 {
		return processReaped(leaderPid)
	}
	err := syscall.Kill(-pgid, 0)
	return err == syscall.ESRCH
}

func processReaped(pid int) bool {
	var status syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err == syscall.ECHILD {
		return true
	}
	return wpid == pid
}

func reapGroupZombies(pgid, leaderPid int) {
	_ = processReaped(leaderPid)
	for _, pid := range groupMemberPids(pgid) {
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	}
}

func groupMemberPids(pgid int) []int {
	if pgid <= 0 {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if procPgid(pid) == pgid {
			pids = append(pids, pid)
		}
	}
	return pids
}

func procPgid(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return -1
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		return -1
	}
	fields := strings.Fields(string(b[i+2:]))
	if len(fields) < 3 {
		return -1
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil {
		return -1
	}
	return pgrp
}
