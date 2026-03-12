package wiki

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

// RunMCPStdio runs the MCP server using stdio transport (JSON-RPC over
// stdin/stdout). This is used by Claude Desktop's "command" connector.
func RunMCPStdio(store *PageStore, autoCommit *GitAutoCommitter) {
	handler := NewMCPHandler(store, autoCommit)

	// Suppress any log output that would corrupt the JSON-RPC stream
	log.SetOutput(io.Discard)

	scanner := bufio.NewScanner(os.Stdin)
	// Allow large messages (16 MB)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeJSONRPCError(os.Stdout, nil, -32700, "parse error: "+err.Error())
			continue
		}

		// Notifications have no ID — no response needed
		if req.ID == nil || string(req.ID) == "" {
			handler.HandleRPC(req, nil)
			continue
		}

		resp := handler.HandleRPC(req, nil)
		if resp == nil {
			continue
		}

		out, _ := json.Marshal(resp)
		fmt.Fprintf(os.Stdout, "%s\n", out)
	}
}

func writeJSONRPCError(w io.Writer, id json.RawMessage, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	out, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", out)
}
