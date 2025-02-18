package main

import (
	"database/sql"
	"errors"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"mintlify-previewer-backend/log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
)

type Deployment struct {
	UUID      string `json:"uuid"`
	GitHubURL string `json:"github_url"`
	Branch    string `json:"branch"`
	DeployURL string `json:"deployment_url"`
	Status    string `json:"status"`
}

var db *sql.DB

func initDB() {
	var err error
	dbPath := "./.sqlite/deployments.db"
	dir := "./.sqlite"

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		log.Fatal("failed to create database directory: ", err)
	}

	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal("failed to open database: ", err)
	}

	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		log.Fatal("failed to create SQLite driver instance: ", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations",
		"sqlite",
		driver,
	)
	if err != nil {
		log.Fatal("failed to create migration instance: ", err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatal("failed to apply migrations: ", err)
	}

	log.Info("Migrations applied successfully!")
}

func restoreDeployments() {
	// TODO: Stop deployments after x number of days
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal("Failed to get working directory")
		return
	}

	rows, err := db.Query("SELECT uuid, github_url, branch, deployment_url, status FROM deployments WHERE status IN ('running', 'starting')")
	if err != nil {
		log.Fatalf("Failed to query deployments: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var dep Deployment
		if err := rows.Scan(&dep.UUID, &dep.GitHubURL, &dep.Branch, &dep.DeployURL, &dep.Status); err != nil {
			log.Infof("Failed to scan deployment: %v", err)
			return
		}

		go func() {

			deploymentDir := filepath.Join(dir, ".repos", dep.UUID)

			// Check if the deployment directory exists and is not empty (except for Git files)
			if isEmptyOrOnlyGitFiles(deploymentDir) {
				log.Infof("Repository not cloned or incomplete for UUID %s. Cloning now...", dep.UUID)
				_, repoURL := extractPRID(dep.GitHubURL)
				if out, err := cloneRepo(repoURL, dep.Branch, deploymentDir); err != nil {
					log.Infof("Failed to clone repository for UUID %s: %v", dep.UUID, out)
					_, err2 := db.Exec("UPDATE deployments SET status = ?, error = ? WHERE uuid = ?", "failed", err.Error(), dep.UUID)
					if err2 != nil {
						log.Infof("Failed to update status for UUID %s: %v for error %+v", dep.UUID, err2, err)
					}
					return
				}
			}

			effectiveMintJSONPath := mintJSONPath
			if effectiveMintJSONPath == "" {
				effectiveMintJSONPath = "docs/mint.json"
			}
			mintFilePath := filepath.Join(deploymentDir, effectiveMintJSONPath)
			if _, err := os.Stat(mintFilePath); os.IsNotExist(err) {
				_, err2 := db.Exec("UPDATE deployments SET status = ?, error = ? WHERE uuid = ?", "failed", "mint.json file not found", dep.UUID)
				if err2 != nil {
					log.Infof("Failed to update status for UUID %s: %v for error %+v", dep.UUID, err2, err)
				}
				return
			}

			serverDir := filepath.Dir(mintFilePath)
			port := extractPortFromURL(dep.DeployURL)

			startMintlifyDev(dep.UUID, port, serverDir)
		}()
	}
}

func isEmptyOrOnlyGitFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// If the directory doesn't exist, it's considered "empty"
		return true
	}

	// Check if the directory is empty or contains only Git files
	for _, entry := range entries {
		if entry.Name() != ".git" {
			return false // Directory contains non-Git files
		}
	}

	return true // Directory is empty or contains only Git files
}

func extractPortFromURL(deployURL string) int {
	parsedURL, err := url.Parse(deployURL)
	if err != nil {
		log.Infof("Failed to parse URL %s: %v", deployURL, err)
		return 0
	}

	hostParts := strings.Split(parsedURL.Host, ":")
	if len(hostParts) < 2 {
		log.Infof("No port found in URL %s", deployURL)
		return 0
	}

	port, err := strconv.Atoi(hostParts[1])
	if err != nil {
		log.Infof("Failed to convert port to integer: %v", err)
		return 0
	}

	return port
}
