package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	autoCommitter := wiki.NewGitAutoCommitter(dataDir)

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
