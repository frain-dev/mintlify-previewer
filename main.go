package main

import (
	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"mintlify-previewer-backend/log"
	"net/http"
	"os"
)

type Deployment struct {
	UUID      string `json:"uuid"`
	GitHubURL string `json:"github_url"`
	Branch    string `json:"branch"`
	DeployURL string `json:"deployment_url"`
	Status    string `json:"status"`
}

func main() {
	initDB()
	restoreDeployments()

	r := chi.NewRouter()
	r.Post("/deploy", createDeploymentHandler)
	r.Get("/{uuid}", getDeploymentHandler)
	r.Delete("/{uuid}", deleteDeploymentHandler)
	r.Get("/*", proxyOrShowStatus) // Handles all paths dynamically

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Infof("Server running on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
