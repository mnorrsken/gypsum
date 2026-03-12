// mcp-proxy is a stdio-to-HTTP proxy for Gypsum's MCP endpoint.
// Claude Desktop spawns this binary; it reads JSON-RPC from stdin,
// forwards each message to the remote /mcp endpoint, and writes
// responses back to stdout.
//
// Usage:
//
//	mcp-proxy https://wiki.example.com/mcp
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mcp-proxy <mcp-url>\n")
		fmt.Fprintf(os.Stderr, "  e.g. mcp-proxy https://wiki.example.com/mcp\n")
		os.Exit(1)
	}
	endpoint := os.Args[1]

	// Suppress log output that would corrupt the JSON-RPC stream on stdout.
	log.SetOutput(os.Stderr)

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
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

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

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("failed to read response: %v", err)
			continue
		}

		// The server's json.Encoder already appends a newline, so write
		// the body as-is. Add a trailing newline only if missing.
		os.Stdout.Write(body)
		if len(body) > 0 && body[len(body)-1] != '\n' {
			os.Stdout.Write([]byte{'\n'})
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("stdin read error: %v", err)
	}
}
