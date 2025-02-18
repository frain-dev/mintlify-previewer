package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"html/template"
	"math/rand"
	"mintlify-previewer-backend/log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"
)

func createDeploymentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req Deployment
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	effectiveMintJSONPath, err := getValidDocsPath(req.DocsPath)
	if err != nil {
		http.Error(w, "Invalid docs path: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.DocsPath = effectiveMintJSONPath

	dir, err := os.Getwd()
	if err != nil {
		http.Error(w, "Failed to get working directory", http.StatusInternalServerError)
		return
	}

	newUUID := ulid.Make().String()
	req.UUID = newUUID

	deploymentDir := filepath.Join(dir, ".repos", req.UUID)
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		http.Error(w, "Failed to create deployment directory", http.StatusInternalServerError)
		return
	}

	_, repoURL := extractPRID(req.GitHubURL)
	if err := checkRepoExists(repoURL, req.Branch); err != nil {
		http.Error(w, fmt.Sprintf("Repository check failed: %v", err), http.StatusInternalServerError)
		return
	}

	port := getUniquePort()
	deployURL := fmt.Sprintf("http://localhost:%d", port)
	reverseProxyURL := fmt.Sprintf("http://%s.%s", newUUID, r.Host)

	_, err = db.Exec("INSERT INTO deployments (uuid, github_url, branch, docs_path, deployment_url, deployment_proxy_url, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		newUUID, req.GitHubURL, req.Branch, req.DocsPath, deployURL, reverseProxyURL, "starting")
	if err != nil {
		log.Info("failed to create deployment:", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	response := Deployment{UUID: newUUID, GitHubURL: req.GitHubURL, Branch: req.Branch, DeployURL: reverseProxyURL, Status: "starting"}
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		log.Errorf("Failed to encode response: %v", err)
	}

	startProcessing(newUUID, repoURL, req, deploymentDir, port)
}

func startProcessing(newUUID string, repoURL string, req Deployment, deploymentDir string, port int) {
	go func() {
		if err := ensureMintlifyInstalled(); err != nil {
			log.Infof("Failed to install Mintlify: %v", err)
			_, _ = db.Exec("UPDATE deployments SET status = ?, error = ? WHERE uuid = ?", "failed", err.Error(), newUUID)
			return
		}

		if _, err := cloneRepo(repoURL, req.Branch, deploymentDir); err != nil {
			log.Errorln(err)
			_, _ = db.Exec("UPDATE deployments SET status = ?, error = ? WHERE uuid = ?", "failed", err.Error(), newUUID)
			return
		}

		mintFilePath := filepath.Join(deploymentDir, req.DocsPath)
		if _, err := os.Stat(mintFilePath); os.IsNotExist(err) {
			_, _ = db.Exec("UPDATE deployments SET status = ?, error = ? WHERE uuid = ?", "failed", "mint.json file not found", newUUID)
			return
		}

		serverDir := filepath.Dir(mintFilePath)

		startMintlifyDev(newUUID, port, serverDir)
	}()
}

func getValidDocsPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("docs_path cannot be empty")
	}
	if !strings.HasSuffix(path, ".json") {
		return "", errors.New("docs_path must end with .json")
	}
	return path, nil
}

func getDeploymentHandler(w http.ResponseWriter, r *http.Request) {
	uuidParam := chi.URLParam(r, "uuid")
	var dep Deployment

	err := db.QueryRow("SELECT  uuid, github_url, branch, deployment_proxy_url as deployment_url, status FROM deployments WHERE uuid = ?",
		uuidParam).Scan(&dep.UUID, &dep.GitHubURL, &dep.Branch, &dep.DeployURL, &dep.Status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		log.Info("Failed to query deployment:", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(dep)
	if err != nil {
		log.Error("Failed to encode response:", err)
	}
}

func getUniquePort() int {
	for {
		port := 5000 + rand.Intn(1000)
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			err2 := ln.Close()
			if err2 != nil {
				log.Error("Failed to close listener:", err2)
			}
			return port
		}
	}
}

func extractPRID(githubURL string) (string, string) {
	// Extract the PR ID and the repo URL
	// Assuming URL is in format https://github.com/username/repository/pull/42
	parts := strings.Split(githubURL, "/")
	prID := parts[len(parts)-1]

	// Reconstruct the repo URL (without the PR part)
	repoURL := strings.Join(parts[:len(parts)-2], "/") + ".git"

	return prID, repoURL
}

func deleteDeploymentHandler(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")

	err := stopMintlifyServer(uuid)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	_, err = fmt.Fprintf(w, "Mintlify server for UUID %s stopped", uuid)
	if err != nil {
		log.Error("Failed to write response:", err)
	}
}

func proxyOrShowStatus(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	hostParts := strings.Split(host, ".")
	if len(hostParts) < 2 {
		http.Error(w, "Invalid hostname", http.StatusBadRequest)
		return
	}
	uuid := strings.ToUpper(hostParts[0]) // Extract UUID

	var status string
	var deploymentUrl string
	err := db.QueryRow("SELECT status, deployment_url FROM deployments WHERE uuid = ?", uuid).Scan(&status, &deploymentUrl)
	if err != nil {
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	// Handle proxying for running deployments
	if status == "running" {
		parsedUrl, err := url.Parse(deploymentUrl)
		if err != nil {
			http.Error(w, "Invalid deployment URL", http.StatusInternalServerError)
			return
		}

		if parsedUrl.Scheme == "" {
			parsedUrl.Scheme = "http"
		}

		log.Infof("Incoming request URL: %s", r.URL.String())
		log.Infof("Incoming request URL (parsedPath): %s", parsedUrl)
		proxy := httputil.NewSingleHostReverseProxy(parsedUrl)
		proxy.ServeHTTP(w, r)
		return
	} else if status == "starting" {
		http.ServeFile(w, r, "static/loading.html")
		return
	}

	// Define the status page data for other states
	var data struct {
		Title   string
		Message string
		Icon    string
		Color   string
		Refresh bool
	}

	switch status {
	case "failed":
		data = struct {
			Title   string
			Message string
			Icon    string
			Color   string
			Refresh bool
		}{
			Title:   "Deployment Failed",
			Message: "Something went wrong while starting the server. If this issue persists, please contact support.",
			Icon:    "⚠️",
			Color:   "#ef4444",
			Refresh: false,
		}
	case "stopped":
		data = struct {
			Title   string
			Message string
			Icon    string
			Color   string
			Refresh bool
		}{
			Title:   "Deployment Stopped",
			Message: "The documentation preview is currently unavailable.",
			Icon:    "🛑",
			Color:   "#6366f1",
			Refresh: false,
		}
	default:
		data = struct {
			Title   string
			Message string
			Icon    string
			Color   string
			Refresh bool
		}{
			Title:   "Unknown Deployment State",
			Message: "We're unable to determine the current state of your deployment. Please check back later or contact support if this persists.",
			Icon:    "❓",
			Color:   "#eab308",
			Refresh: false,
		}
	}

	// Load and render the template
	tmpl, err := template.ParseFiles("static/status.html")
	if err != nil {
		http.Error(w, "Failed to load template", http.StatusInternalServerError)
		return
	}

	err = tmpl.Execute(w, data)
	if err != nil {
		log.Error("Failed to load template:", err)
	}
}
