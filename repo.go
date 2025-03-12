package main

import (
	"bufio"
	"fmt"
	"mintlify-previewer-backend/log"
	"os/exec"
)

// checkRepoExists checks if the repository exists and is accessible
func checkRepoExists(repoURL, prNumber string) error {
	prRef := fmt.Sprintf("refs/pull/%s/head", prNumber)
	cmd := exec.Command("git", "ls-remote", "--exit-code", repoURL, prRef)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("repository check failed: %v, output: %s", err, string(output))
	}

	log.Info("PR exists: " + prNumber)
	return nil
}

// cloneRepo clones the repository
func cloneRepo(repoURL, prNumber, dir string) (string, error) {
	log.Info("About to clone repo. Repo URL: " + repoURL)

	// Clone the repository
	cmd := exec.Command("git", "clone", repoURL, dir)
	if err := runCommandWithPipe(cmd); err != nil {
		return "", fmt.Errorf("failed to clone repo: %w", err)
	}

	// Fetch the PR branch into a temporary branch
	prBranch := "pr-" + prNumber
	cmd = exec.Command("git", "-C", dir, "fetch", "origin", "pull/"+prNumber+"/head:"+prBranch)
	if err := runCommandWithPipe(cmd); err != nil {
		return "", fmt.Errorf("failed to fetch PR branch: %w", err)
	}

	// Checkout the fetched PR branch
	cmd = exec.Command("git", "-C", dir, "checkout", prBranch)
	if err := runCommandWithPipe(cmd); err != nil {
		return "", fmt.Errorf("failed to checkout PR branch: %w", err)
	}

	log.Info("Repository cloned and PR branch checked out successfully: " + prBranch)
	return "", nil
}

func runCommandWithPipe(cmd *exec.Cmd) error {
	// Create pipes for stdout and stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Create scanners to read stdout and stderr
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
			log.Warn(stderrScanner.Text())
		}
	}()

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}

	return nil
}
