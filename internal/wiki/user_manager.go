package wiki

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GitTemplateConfig defines a URL template for per-user git remotes.
// The URLTemplate may contain {user} which is replaced with the username.
// Example: "https://git.example.com/~{user}/gypsum.git"
// Auth is shared across all users via Token or Username+Password.
type GitTemplateConfig struct {
	URLTemplate string // e.g. "https://git.example.com/~{user}/gypsum.git"
	RemoteName  string // default "origin"
	CommitEmail string // e.g. "{user}@example.com" or static email
	Token       string // shared auth token (already URL-escaped)
	Username    string // shared auth username (already URL-escaped)
	Password    string // shared auth password (already URL-escaped)
}

// ExpandRemote builds a GitRemoteConfig for the given username by substituting
// {user} in the URL template and injecting shared auth credentials.
func (t *GitTemplateConfig) ExpandRemote(username string) *GitRemoteConfig {
	if t == nil || t.URLTemplate == "" {
		return nil
	}
	rawURL := strings.ReplaceAll(t.URLTemplate, "{user}", username)
	authURL := t.injectAuth(rawURL)

	remoteName := t.RemoteName
	if remoteName == "" {
		remoteName = "origin"
	}
	email := t.CommitEmail
	if email == "" {
		email = username + "@gypsum.local"
	} else {
		email = strings.ReplaceAll(email, "{user}", username)
	}
	return &GitRemoteConfig{
		RemoteName:  remoteName,
		RemoteURL:   authURL,
		CommitName:  username,
		CommitEmail: email,
	}
}

func (t *GitTemplateConfig) injectAuth(rawURL string) string {
	if t.Token != "" {
		return injectURLAuth(rawURL, t.Token, "")
	}
	if t.Username != "" && t.Password != "" {
		return injectURLAuth(rawURL, t.Username, t.Password)
	}
	return rawURL
}

// injectURLAuth inserts credentials into an HTTPS URL.
func injectURLAuth(rawURL, user, pass string) string {
	if strings.HasPrefix(rawURL, "https://") {
		auth := user
		if pass != "" {
			auth += ":" + pass
		}
		return "https://" + auth + "@" + strings.TrimPrefix(rawURL, "https://")
	}
	return rawURL
}

// UserContext holds the per-user (or shared) wiki resources.
type UserContext struct {
	Store      *PageStore
	AutoCommit *GitAutoCommitter
	DB         *DB
}

// UserManager manages per-user wiki contexts with lazy provisioning.
// When multi-user mode is disabled, only the shared context is used.
type UserManager struct {
	mu           sync.RWMutex
	dataDir      string
	users        map[string]*UserContext
	shared       *UserContext      // shared wiki (backwards-compatible)
	gitTemplate  *GitTemplateConfig
	globalRemote *GitRemoteConfig  // fallback when git template is not set
	pullInterval time.Duration
	secretKey    string // for seeding per-user DBs
}

// UserManagerConfig holds the configuration for creating a UserManager.
type UserManagerConfig struct {
	DataDir      string
	Shared       *UserContext
	GitTemplate  *GitTemplateConfig
	GlobalRemote *GitRemoteConfig // used for per-user repos when no template is set
	PullInterval time.Duration
	SecretKey    string
}

// NewUserManager creates a new multi-user manager.
func NewUserManager(cfg UserManagerConfig) *UserManager {
	return &UserManager{
		dataDir:      cfg.DataDir,
		users:        make(map[string]*UserContext),
		shared:       cfg.Shared,
		gitTemplate:  cfg.GitTemplate,
		globalRemote: cfg.GlobalRemote,
		pullInterval: cfg.PullInterval,
		secretKey:    cfg.SecretKey,
	}
}

// Shared returns the shared (default) user context.
func (m *UserManager) Shared() *UserContext {
	return m.shared
}

// Resolve returns the UserContext for the given username, lazily provisioning
// it if necessary. Returns the shared context if username is empty.
func (m *UserManager) Resolve(username string) (*UserContext, error) {
	if username == "" {
		return m.shared, nil
	}

	// Fast path: check under read lock.
	m.mu.RLock()
	ctx, ok := m.users[username]
	m.mu.RUnlock()
	if ok {
		return ctx, nil
	}

	// Slow path: provision under write lock.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if ctx, ok := m.users[username]; ok {
		return ctx, nil
	}

	ctx, err := m.provision(username)
	if err != nil {
		return nil, fmt.Errorf("failed to provision user %q: %w", username, err)
	}
	m.users[username] = ctx
	log.Printf("multi-user: provisioned wiki for user %q", username)
	return ctx, nil
}

// provision creates the data directories, database, page store, and git
// auto-committer for a new user.
func (m *UserManager) provision(username string) (*UserContext, error) {
	userDir := filepath.Join(m.dataDir, "users", username)
	repoDir := filepath.Join(userDir, "repo")
	pagesDir := filepath.Join(repoDir, "pages")
	skillsDir := filepath.Join(repoDir, "skills")

	for _, dir := range []string{pagesDir, skillsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	db, err := OpenDB(userDir)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := NewPageStore(pagesDir)
	store.SetDB(db)

	// Resolve git remote: prefer template, fall back to global remote.
	var remote *GitRemoteConfig
	if m.gitTemplate != nil {
		remote = m.gitTemplate.ExpandRemote(username)
	}

	autoCommitter := NewGitAutoCommitter(repoDir, remote)
	autoCommitter.SetAfterPull(store.ReindexChanged)
	if remote != nil && m.pullInterval > 0 {
		autoCommitter.StartPeriodicPull(m.pullInterval)
	}

	return &UserContext{
		Store:      store,
		AutoCommit: autoCommitter,
		DB:         db,
	}, nil
}

// ListUsers returns the usernames of all provisioned users (both currently
// loaded and those with directories on disk).
func (m *UserManager) ListUsers() []string {
	usersDir := filepath.Join(m.dataDir, "users")
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// Stop shuts down all per-user auto-committers and closes databases.
func (m *UserManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, ctx := range m.users {
		ctx.AutoCommit.Stop()
		if ctx.DB != nil {
			ctx.DB.Close()
		}
		log.Printf("multi-user: stopped user %q", name)
	}
}
