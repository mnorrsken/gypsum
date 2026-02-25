package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mnorrsken/gypsum/internal/wiki"
)

//go:embed seed_pages/*.md
var seedPages embed.FS

func main() {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to get working directory: %v", err)
	}

	secretKey := os.Getenv("GYPSUM_SECRET_KEY")
	if secretKey == "" {
		secretKey = "change-me-in-production"
		log.Println("WARNING: GYPSUM_SECRET_KEY not set, using default key")
	}

	pagesDir := filepath.Join(workspaceRoot, "data", "pages")
	dataDir := filepath.Join(workspaceRoot, "data")
	templatesDir := filepath.Join(workspaceRoot, "web", "templates")
	staticDir := filepath.Join(workspaceRoot, "web", "static")

	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		log.Fatalf("failed to create pages directory: %v", err)
	}
	if err := seedPagesIfEmpty(pagesDir); err != nil {
		log.Fatalf("failed to seed default pages: %v", err)
	}

	store := wiki.NewPageStore(pagesDir)
	crypto := wiki.NewServerCrypto(secretKey)
	renderer := wiki.NewMarkdownRenderer()

	var remoteConfig *wiki.GitRemoteConfig
	if remoteURL := os.Getenv("GYPSUM_GIT_REMOTE_URL"); remoteURL != "" {
		authURL := injectGitAuth(remoteURL)
		remoteName := os.Getenv("GYPSUM_GIT_REMOTE_NAME")
		if remoteName == "" {
			remoteName = "origin"
		}
		remoteConfig = &wiki.GitRemoteConfig{
			RemoteName:  remoteName,
			RemoteURL:   authURL,
			CommitName:  envOrDefault("GYPSUM_GIT_COMMIT_NAME", "Gypsum"),
			CommitEmail: envOrDefault("GYPSUM_GIT_COMMIT_EMAIL", "gypsum@local"),
		}
	}

	autoCommitter := wiki.NewGitAutoCommitter(dataDir, remoteConfig)
	if remoteConfig != nil {
		pullInterval := 5 * time.Minute
		if v := os.Getenv("GYPSUM_GIT_PULL_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				pullInterval = d
			}
		}
		autoCommitter.StartPeriodicPull(pullInterval)
	}

	handler := wiki.NewHandler(store, crypto, renderer, templatesDir, autoCommitter)
	mux := http.NewServeMux()
	mux.Handle("/", handler.Routes())
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	addr := ":8080"
	log.Printf("wiki listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func seedPagesIfEmpty(pagesDir string) error {
	entries, err := os.ReadDir(pagesDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
	}

	seedEntries, err := seedPages.ReadDir("seed_pages")
	if err != nil {
		return err
	}
	for _, entry := range seedEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		body, err := seedPages.ReadFile(filepath.Join("seed_pages", entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(pagesDir, entry.Name()), body, 0o644); err != nil {
			return err
		}
	}

	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// injectGitAuth builds an authenticated URL from GYPSUM_GIT_REMOTE_URL
// using GYPSUM_GIT_TOKEN or GYPSUM_GIT_USERNAME + GYPSUM_GIT_PASSWORD.
func injectGitAuth(rawURL string) string {
	if token := os.Getenv("GYPSUM_GIT_TOKEN"); token != "" {
		return injectAuth(rawURL, token)
	}
	user := os.Getenv("GYPSUM_GIT_USERNAME")
	pass := os.Getenv("GYPSUM_GIT_PASSWORD")
	if user != "" && pass != "" {
		return injectAuth(rawURL, user+":"+pass)
	}
	return rawURL
}

func injectAuth(url, auth string) string {
	if strings.HasPrefix(url, "https://") {
		return "https://" + auth + "@" + strings.TrimPrefix(url, "https://")
	}
	return url
}
