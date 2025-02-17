package main

import (
	"bufio"
	"fmt"
	"mintlify-previewer-backend/log"
	"os/exec"
)

// checkRepoExists checks if the repository exists and is accessible
func checkRepoExists(repoURL, branch string) error {
	cmd := exec.Command("git", "ls-remote", "--heads", repoURL, branch)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("repository check failed: %v, output: %s", err, string(output))
	}

	// If the branch exists, the output will contain the branch reference
	if len(output) == 0 {
		return fmt.Errorf("branch %s not found in repository", branch)
	}

	return nil
}

// cloneRepo clones the repository
func cloneRepo(repoURL, branch, dir string) (string, error) {
	log.Info("About to clone repo. Repo url is " + repoURL)

	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", branch, repoURL, dir)

	// Create pipes for stdout and stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	// Create a scanner to read stdout and stderr line by line
	stdoutScanner := bufio.NewScanner(stdoutPipe)
	stderrScanner := bufio.NewScanner(stderrPipe)

	// Log stdout in real-time
	go func() {
		for stdoutScanner.Scan() {
			log.Info(stdoutScanner.Text())
		}
	}()

	// Log stderr in real-time
	go func() {
		for stderrScanner.Scan() {
			log.Info(stderrScanner.Text())
		}
	}()

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("failed to clone repo %s on branch %s: %w", repoURL, branch, err)
	}

	log.Info("Repository cloned successfully")
	return "", nil
}
