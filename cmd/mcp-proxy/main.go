// mcp-proxy is a stdio-to-HTTP proxy for Gypsum's MCP endpoint.
// Claude Desktop (and other MCP hosts) spawn this binary; it reads JSON-RPC
// from stdin, forwards each message to the remote /mcp endpoint, and writes
// responses back to stdout.
//
// Usage:
//
//	mcp-proxy <mcp-url>
//	mcp-proxy auth <wiki-base-url> [--password=PASS]
//
// Proxy mode reads the bearer token from the GYPSUM_TOKEN environment variable.
// Auth mode performs the full PKCE OAuth flow, prints the token to stdout, and
// exits — suitable for use in shell substitution:
//
//	export GYPSUM_TOKEN=$(mcp-proxy auth https://wiki.example.com --password=mypass)
//
// MCP JSON config example:
//
//	{
//	  "mcpServers": {
//	    "gypsum": {
//	      "command": "/path/to/mcp-proxy",
//	      "args": ["https://wiki.example.com/mcp"],
//	      "env": { "GYPSUM_TOKEN": "<token from mcp-proxy auth>" }
//	    }
//	  }
//	}
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func main() {
	// Suppress log output that would corrupt the JSON-RPC stream on stdout.
	log.SetOutput(os.Stderr)

	if len(os.Args) >= 2 && os.Args[1] == "auth" {
		runAuth(os.Args[2:])
		return
	}

	runProxy()
}

// ── Proxy mode ──────────────────────────────────────────────────────────────

func runProxy() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	endpoint := os.Args[1]
	token := os.Getenv("GYPSUM_TOKEN")

	client := &http.Client{}
	var sessionID string

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(line))
		if err != nil {
			log.Printf("failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		// Protocol revision 2026-07-28 requires the request metadata headers to
		// mirror the body, and the server rejects any mismatch. stdio carries no
		// headers, so the proxy derives them here.
		setMetadataHeaders(req, line)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("request failed: %v", err)
			continue
		}

		// Capture session ID from initialize response.
		if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
			sessionID = sid
		}

		// 202 Accepted = notification acknowledged, no body to relay.
		if resp.StatusCode == http.StatusAccepted {
			resp.Body.Close()
			continue
		}

		// Non-OK responses may still be valid JSON-RPC: since 2026-07-28 the
		// server reports unsupported protocol versions, header mismatches and
		// unknown methods as JSON-RPC errors on 400/404, and the client needs to
		// see them to negotiate. Anything else (e.g. 401) goes to stderr so the
		// stdout stream stays clean.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if isJSONRPCError(body) {
				writeLine(body)
				continue
			}
			log.Printf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			if resp.StatusCode == http.StatusUnauthorized {
				log.Printf("hint: run 'mcp-proxy auth <wiki-url>' to obtain a fresh token")
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("failed to read response: %v", err)
			continue
		}

		writeLine(body)
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("stdin read error: %v", err)
	}
}

// writeLine relays one JSON-RPC message to stdout. The server's json.Encoder
// already appends a newline, so add one only if it is missing.
func writeLine(body []byte) {
	os.Stdout.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		os.Stdout.Write([]byte{'\n'})
	}
}

// isJSONRPCError reports whether body is a JSON-RPC error response, which the
// client must see even when it arrives with a 4xx status.
func isJSONRPCError(body []byte) bool {
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		Error   json.RawMessage `json:"error"`
	}
	return json.Unmarshal(body, &msg) == nil && msg.JSONRPC == "2.0" && len(msg.Error) > 0
}

// setMetadataHeaders mirrors the request body's method, tool name and protocol
// version into the HTTP headers required by the Streamable HTTP transport.
// Legacy requests carry no per-request _meta and get only the method header,
// which older servers ignore.
func setMetadataHeaders(req *http.Request, line []byte) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Name string                     `json:"name"`
			URI  string                     `json:"uri"`
			Meta map[string]json.RawMessage `json:"_meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &msg); err != nil || msg.Method == "" {
		return
	}
	req.Header.Set("Mcp-Method", msg.Method)

	var version string
	if raw, ok := msg.Params.Meta["io.modelcontextprotocol/protocolVersion"]; ok {
		_ = json.Unmarshal(raw, &version)
	}
	if version != "" {
		req.Header.Set("MCP-Protocol-Version", version)
	}

	// Mcp-Name mirrors params.name (tools/call, prompts/get) or params.uri
	// (resources/read).
	name := msg.Params.Name
	if name == "" {
		name = msg.Params.URI
	}
	if name != "" {
		req.Header.Set("Mcp-Name", encodeHeaderValue(name))
	}
}

// encodeHeaderValue wraps a value in the "=?base64?...?=" sentinel when it
// cannot be sent as a plain ASCII header field value.
func encodeHeaderValue(v string) string {
	const sentinelPrefix, sentinelSuffix = "=?base64?", "?="
	safe := v != "" &&
		v == strings.TrimSpace(v) &&
		!(strings.HasPrefix(v, sentinelPrefix) && strings.HasSuffix(v, sentinelSuffix))
	if safe {
		for _, c := range []byte(v) {
			if c < 0x20 || c > 0x7E {
				safe = false
				break
			}
		}
	}
	if safe {
		return v
	}
	return sentinelPrefix + base64.StdEncoding.EncodeToString([]byte(v)) + sentinelSuffix
}

// ── Auth mode ───────────────────────────────────────────────────────────────

func runAuth(args []string) {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	password := fs.String("password", "", "wiki password (falls back to GYPSUM_PASSWORD env var, then prompts)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: mcp-proxy auth <wiki-base-url> [--password=PASS]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Performs the OAuth PKCE flow and prints the access token to stdout.")
		fmt.Fprintln(os.Stderr, "The wiki-base-url is the root of your Gypsum instance, e.g.")
		fmt.Fprintln(os.Stderr, "  https://wiki.example.com")
		fmt.Fprintln(os.Stderr, "not the /mcp path.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Example — store token for use in proxy mode:")
		fmt.Fprintln(os.Stderr, "  export GYPSUM_TOKEN=$(mcp-proxy auth https://wiki.example.com --password=mypass)")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Environment variables:")
		fmt.Fprintln(os.Stderr, "  GYPSUM_PASSWORD  Wiki password (alternative to --password flag)")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}
	wikiURL := strings.TrimRight(fs.Arg(0), "/")

	pass := *password
	if pass == "" {
		pass = os.Getenv("GYPSUM_PASSWORD")
	}
	if pass == "" {
		fmt.Fprint(os.Stderr, "Password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading password: %v\n", err)
			os.Exit(1)
		}
		pass = strings.TrimRight(line, "\r\n")
	}
	if pass == "" {
		fmt.Fprintln(os.Stderr, "error: password is required")
		os.Exit(1)
	}

	token, err := oauthFlow(wikiURL, pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(token)
}

// oauthFlow performs the PKCE authorization code flow without a browser:
//  1. Register a dynamic client at /oauth/register.
//  2. POST credentials to /oauth/authorize (server issues a redirect with code).
//  3. Extract the code from the Location header (redirect is not followed).
//  4. Exchange the code + PKCE verifier for an access token at /oauth/token.
func oauthFlow(baseURL, password string) (string, error) {
	// Client that captures redirects instead of following them.
	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	clientID, err := registerClient(noFollow, baseURL)
	if err != nil {
		return "", fmt.Errorf("register client: %w", err)
	}

	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", fmt.Errorf("generate PKCE: %w", err)
	}

	const redirectURI = "http://localhost/callback"

	// POST to /oauth/authorize — the server validates the password and issues
	// a 302 redirect to redirectURI?code=CODE.
	authForm := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"cli"},
		"password":              {password},
	}
	resp, err := noFollow.PostForm(baseURL+"/oauth/authorize", authForm)
	if err != nil {
		return "", fmt.Errorf("authorize request: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		if resp.StatusCode == http.StatusUnauthorized {
			return "", fmt.Errorf("incorrect password")
		}
		return "", fmt.Errorf("authorize returned HTTP %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse redirect location %q: %w", location, err)
	}
	code := parsed.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("no authorization code in redirect: %s", location)
	}

	token, err := exchangeCode(noFollow, baseURL, clientID, code, verifier, redirectURI)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	return token, nil
}

func registerClient(client *http.Client, baseURL string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":   "mcp-proxy-cli",
		"redirect_uris": []string{"http://localhost/callback"},
		"grant_types":   []string{"authorization_code"},
	})
	resp, err := client.Post(baseURL+"/oauth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("register returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode register response: %w", err)
	}
	if result.ClientID == "" {
		return "", fmt.Errorf("empty client_id in register response")
	}
	return result.ClientID, nil
}

// generatePKCE returns a random verifier and its S256 challenge.
func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

func exchangeCode(client *http.Client, baseURL, clientID, code, verifier, redirectURI string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}
	resp, err := client.PostForm(baseURL+"/oauth/token", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in response")
	}
	return result.AccessToken, nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  mcp-proxy <mcp-url>                          proxy stdin/stdout to MCP endpoint")
	fmt.Fprintln(os.Stderr, "  mcp-proxy auth <wiki-url> [--password=PASS]  obtain OAuth token and print it")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "environment variables:")
	fmt.Fprintln(os.Stderr, "  GYPSUM_TOKEN     bearer token for proxy mode")
	fmt.Fprintln(os.Stderr, "  GYPSUM_PASSWORD  wiki password for auth mode (alternative to --password)")
}
