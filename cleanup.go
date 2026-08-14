package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mintlify-previewer-backend/log"
)

// previewTTL is how long a clone and mintlify process stay on disk.
// Each preview copies the full website tree; without a cutoff those
// directories fill the VM (71 clones were 19 GB when the 30 GB disk died).
const previewTTL = 7 * 24 * time.Hour

const cleanupInterval = time.Hour

func reposRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ".repos"
	}
	return filepath.Join(dir, ".repos")
}

func startCleanupLoop() {
	expireOldDeployments()
	go func() {
		t := time.NewTicker(cleanupInterval)
		defer t.Stop()
		for range t.C {
			expireOldDeployments()
		}
	}()
}

func expireOldDeployments() {
	expireDeployments(db, reposRoot(), previewTTL)
}

func expireDeployments(database *sql.DB, root string, ttl time.Duration) {
	if database == nil {
		return
	}

	mod := fmt.Sprintf("-%d hours", int(ttl.Hours()))
	rows, err := database.Query(`
		SELECT uuid FROM deployments
		WHERE deleted_at IS NULL
		  AND (status IN ('failed', 'stopped') OR created_at <= datetime('now', ?))
	`, mod)
	if err != nil {
		log.Errorf("Failed to query expired deployments: %v", err)
		return
	}

	var uuids []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			log.Errorf("Failed to scan expired deployment: %v", err)
			continue
		}
		uuids = append(uuids, uuid)
	}
	_ = rows.Close()

	seen := make(map[string]struct{}, len(uuids))
	for _, uuid := range uuids {
		seen[uuid] = struct{}{}
		teardownDeployment(database, root, uuid)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Errorf("Failed to list %s: %v", root, err)
		}
		return
	}

	cutoff := time.Now().Add(-ttl)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		uuid := entry.Name()
		if _, ok := seen[uuid]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		teardownDeployment(database, root, uuid)
	}
}

func teardownDeployment(database *sql.DB, root, uuid string) {
	stopMintlifyProcess(uuid)
	dir := filepath.Join(root, uuid)
	if err := os.RemoveAll(dir); err != nil {
		log.Errorf("Failed to remove preview dir %s: %v", dir, err)
	}
	if database == nil {
		return
	}
	_, err := database.Exec(`
		UPDATE deployments
		SET status = ?, deleted_at = CURRENT_TIMESTAMP
		WHERE uuid = ? AND deleted_at IS NULL
	`, "stopped", uuid)
	if err != nil {
		log.Errorf("Failed to mark deployment %s stopped: %v", uuid, err)
		return
	}
	log.Infof("Expired preview %s", uuid)
}
