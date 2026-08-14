package main

import (
	"mintlify-previewer-backend/log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
)

var activeServers = make(map[string]*os.Process)
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
	cmd := exec.Command("mintlify", "dev", "--no-open", "--port", strconv.Itoa(port))
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		log.Errorf("Failed to start Mintlify: %v", err)
		_, _ = db.Exec("UPDATE deployments SET status = ? WHERE uuid = ?", "failed", uuid)
		return
	}

	mu.Lock()
	activeServers[uuid] = cmd.Process
	mu.Unlock()

	log.Infof("Mintlify running for UUID %s on port %d", uuid, port)
	_, err := db.Exec("UPDATE deployments SET status = ? WHERE uuid = ?", "running", uuid)
	if err != nil {
		log.Errorf("Failed to update running status: %v", err)
	}

	err = cmd.Wait()
	if err != nil {
		log.Errorf("Failed to start Mintlify: %v", err)
	}

	mu.Lock()
	delete(activeServers, uuid)
	mu.Unlock()
}

func stopMintlifyProcess(uuid string) {
	mu.Lock()
	process, exists := activeServers[uuid]
	if exists {
		delete(activeServers, uuid)
	}
	mu.Unlock()

	if !exists {
		return
	}

	if err := killProcessGroup(process); err != nil {
		log.Errorf("Failed to stop Mintlify server for %s: %v", uuid, err)
	}
}

func killProcessGroup(process *os.Process) error {
	pgid, err := syscall.Getpgid(process.Pid)
	if err == nil {
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	return process.Signal(syscall.SIGTERM)
}
