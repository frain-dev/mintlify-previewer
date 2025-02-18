package main

import (
	"fmt"
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

	if err := cmd.Start(); err != nil {
		log.Errorf("Failed to start Mintlify: %v", err)
		_, _ = db.Exec("UPDATE deployments SET status = ? WHERE uuid = ?", "failed", uuid)
		return
	}

	mu.Lock()
	activeServers[uuid] = cmd.Process
	mu.Unlock()

	log.Infof("Mintlify running for UUID %s on port %d", uuid, port)
	_, _ = db.Exec("UPDATE deployments SET status = ? WHERE uuid = ?", "running", uuid)

	cmd.Wait()

	mu.Lock()
	delete(activeServers, uuid)
	mu.Unlock()
}

func stopMintlifyServer(uuid string) error {
	mu.Lock()
	process, exists := activeServers[uuid]
	mu.Unlock()

	if !exists {
		log.Errorf("server for UUID %s not found", uuid)
		return fmt.Errorf("server for UUID %s not found", uuid)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		log.Errorf("Failed to stop Mintlify server: %v", err)
		return fmt.Errorf("failed to stop server for UUID %s: %v", uuid, err)
	}

	mu.Lock()
	delete(activeServers, uuid)
	mu.Unlock()

	log.Infof("Mintlify server for UUID %s stopped", uuid)
	_, err := db.Exec("UPDATE deployments SET status = ? WHERE uuid = ?", "stopped", uuid)

	return err
}
