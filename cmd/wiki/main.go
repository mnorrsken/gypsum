package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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

	dataDir := filepath.Join(workspaceRoot, "data")
	repoDir := filepath.Join(dataDir, "repo")
	pagesDir := filepath.Join(repoDir, "pages")
	templatesDir := filepath.Join(workspaceRoot, "web", "templates")
	staticDir := filepath.Join(workspaceRoot, "web", "static")

	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		log.Fatalf("failed to create pages directory: %v", err)
	}
	if err := seedPagesIfEmpty(pagesDir); err != nil {
		log.Fatalf("failed to seed default pages: %v", err)
	}

	db, err := wiki.OpenDB(dataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
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

	autoCommitter := wiki.NewGitAutoCommitter(repoDir, remoteConfig)
	if remoteConfig != nil {
		pullInterval := 5 * time.Minute
		if v := os.Getenv("GYPSUM_GIT_PULL_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				pullInterval = d
			}
		}
		autoCommitter.StartPeriodicPull(pullInterval)
	}

	var oauthServer *wiki.OAuthServer
	if os.Getenv("GYPSUM_OAUTH_ENABLED") == "true" {
		password := os.Getenv("GYPSUM_OAUTH_PASSWORD")
		if password == "" {
			log.Fatal("GYPSUM_OAUTH_PASSWORD must be set when GYPSUM_OAUTH_ENABLED=true")
		}
		externalURL := os.Getenv("GYPSUM_EXTERNAL_URL")
		if externalURL == "" {
			log.Fatal("GYPSUM_EXTERNAL_URL must be set when GYPSUM_OAUTH_ENABLED=true")
		}
		tokenTTL := 24 * time.Hour
		if v := os.Getenv("GYPSUM_OAUTH_TOKEN_TTL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				tokenTTL = d
			}
		}
		oauthServer = wiki.NewOAuthServer(password, externalURL, tokenTTL, db)
		log.Printf("OAuth enabled: /mcp/external protected via OAuth (external URL: %s)", externalURL)
	}

	handler := wiki.NewHandler(store, crypto, renderer, templatesDir, autoCommitter, oauthServer, db)
	mux := http.NewServeMux()
	mux.Handle("/", handler.Routes())
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	var root http.Handler = mux
	if userHeader := os.Getenv("GYPSUM_AUTH_USER_HEADER"); userHeader != "" {
		cfg := wiki.HeaderAuthConfig{
			UserHeader:    userHeader,
			GroupHeader:   envOrDefault("GYPSUM_AUTH_GROUP_HEADER", "Remote-Group"),
			RequiredGroup: os.Getenv("GYPSUM_AUTH_REQUIRED_GROUP"),
		}
		root = wiki.HeaderAuth(cfg)(mux)
		log.Printf("Header auth enabled: user=%s group=%s required=%s", cfg.UserHeader, cfg.GroupHeader, cfg.RequiredGroup)
	}

	addr := ":8080"
	srv := &http.Server{Addr: addr, Handler: accessLog(root)}

	// Start a dedicated probe server for Kubernetes liveness/readiness checks.
	// This runs on a separate port without auth middleware so probes always work.
	probeAddr := envOrDefault("GYPSUM_PROBE_PORT", ":9091")
	probeMux := http.NewServeMux()
	probeMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	probeMux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	probeSrv := &http.Server{Addr: probeAddr, Handler: probeMux}
	go func() {
		log.Printf("probe server listening on %s", probeAddr)
		if err := probeSrv.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("probe server failed: %v", err)
		}
	}()

	// Graceful shutdown: listen for SIGINT/SIGTERM, then drain connections.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("shutting down…")
		autoCommitter.Stop()
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		probeSrv.Shutdown(ctx)
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}()

	log.Printf("wiki listening on http://localhost%s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
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
		return injectAuth(rawURL, url.PathEscape(token), "")
	}
	user := os.Getenv("GYPSUM_GIT_USERNAME")
	pass := os.Getenv("GYPSUM_GIT_PASSWORD")
	if user != "" && pass != "" {
		return injectAuth(rawURL, url.PathEscape(user), url.PathEscape(pass))
	}
	return rawURL
}

func injectAuth(rawURL, user, pass string) string {
	if strings.HasPrefix(rawURL, "https://") {
		auth := user
		if pass != "" {
			auth += ":" + pass
		}
		return "https://" + auth + "@" + strings.TrimPrefix(rawURL, "https://")
	}
	return rawURL
}

// responseRecorder wraps http.ResponseWriter to capture the status code.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

// accessLog is middleware that logs method, path, status, and duration for each request.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rr.status, time.Since(start).Round(time.Microsecond))
	})
}
