package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "deployments.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE deployments (
			uuid TEXT PRIMARY KEY,
			github_url TEXT,
			branch TEXT,
			docs_path TEXT,
			deployment_url TEXT,
			deployment_proxy_url TEXT,
			status TEXT,
			error TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func insertDeployment(t *testing.T, db *sql.DB, uuid, status, createdSQL string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO deployments (uuid, status, created_at) VALUES (?, ?, `+createdSQL+`)`,
		uuid, status,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func mkdirPreview(t *testing.T, root, uuid string) string {
	t.Helper()
	dir := filepath.Join(root, uuid)
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExpireDeploymentsRemovesOldAndFailed(t *testing.T) {
	database := testDB(t)
	root := filepath.Join(t.TempDir(), ".repos")

	oldDir := mkdirPreview(t, root, "old-running")
	freshDir := mkdirPreview(t, root, "fresh-running")
	failedDir := mkdirPreview(t, root, "failed-now")
	orphanDir := mkdirPreview(t, root, "orphan-old")

	insertDeployment(t, database, "old-running", "running", "datetime('now', '-8 days')")
	insertDeployment(t, database, "fresh-running", "running", "datetime('now')")
	insertDeployment(t, database, "failed-now", "failed", "datetime('now')")

	oldMtime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(orphanDir, oldMtime, oldMtime); err != nil {
		t.Fatal(err)
	}

	expireDeployments(database, root, 7*24*time.Hour)

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old running clone should be removed, err=%v", err)
	}
	if _, err := os.Stat(failedDir); !os.IsNotExist(err) {
		t.Fatalf("failed clone should be removed, err=%v", err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("stale orphan clone should be removed, err=%v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("fresh running clone should remain: %v", err)
	}

	var oldDeleted, failedDeleted sql.NullString
	if err := database.QueryRow(`SELECT deleted_at FROM deployments WHERE uuid = ?`, "old-running").Scan(&oldDeleted); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT deleted_at FROM deployments WHERE uuid = ?`, "failed-now").Scan(&failedDeleted); err != nil {
		t.Fatal(err)
	}
	if !oldDeleted.Valid {
		t.Fatal("old running row should set deleted_at")
	}
	if !failedDeleted.Valid {
		t.Fatal("failed row should set deleted_at")
	}

	var freshStatus string
	var freshDeleted sql.NullString
	if err := database.QueryRow(`SELECT status, deleted_at FROM deployments WHERE uuid = ?`, "fresh-running").Scan(&freshStatus, &freshDeleted); err != nil {
		t.Fatal(err)
	}
	if freshStatus != "running" || freshDeleted.Valid {
		t.Fatalf("fresh row mutated: status=%s deleted=%v", freshStatus, freshDeleted.Valid)
	}
}
