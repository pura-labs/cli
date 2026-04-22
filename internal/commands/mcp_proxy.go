// `pura mcp proxy` — stdio ⇔ HTTP bridge for MCP clients that spawn us
// as a subprocess (all stdio-transport clients).
//
// Reads JSON-RPC 2.0 requests one-per-line from stdin, forwards each to
// $PURA_URL/mcp with the resolved Bearer token, and writes the response
// (one line) to stdout. Preserves order by serializing requests.
//
// Env:
//   PURA_URL        Required. Pura origin, e.g. https://pura.so
//   PURA_API_KEY    Optional. Bearer token.
//   PURA_AGENT      Optional. X-Pura-Agent value for attestation.
//
// Clients configured via `pura mcp install` with stdio transport invoke
// this subcommand. Users shouldn't need to run it directly; the cobra
// --hidden flag keeps it out of help output.

package commands

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newMcpProxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "proxy",
		Short:  "stdio⇔HTTP bridge (invoked by MCP clients; hidden)",
		Hidden: true,
		Long: `Internal subprocess: reads JSON-RPC 2.0 requests one-per-line from
stdin, forwards each to $PURA_URL/mcp with the resolved Bearer token, and
writes the response to stdout. Used by MCP clients configured via
` + "`pura mcp install`" + ` with stdio transport.`,
		RunE: runMcpProxy,
	}
}

func runMcpProxy(cmd *cobra.Command, _ []string) error {
	url := strings.TrimRight(os.Getenv("PURA_URL"), "/")
	if url == "" {
		return errors.New("PURA_URL is not set")
	}
	apiKey := os.Getenv("PURA_API_KEY")
	agent := os.Getenv("PURA_AGENT")
	if agent == "" {
		agent = fmt.Sprintf("pura-cli-proxy/%s (session:%d)", versionStr, os.Getpid())
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	httpC := &http.Client{Timeout: 60 * time.Second}
	endpoint := url + "/mcp"

	scanner := bufio.NewScanner(os.Stdin)
	// Default scanner buffer is 64KB — MCP responses can be bigger once
	// sheet schemas grow. Lift to 8 MiB.
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		req, err := http.NewRequestWithContext(
			ctx, http.MethodPost, endpoint,
			bytes.NewReader([]byte(line)),
		)
		if err != nil {
			writeProxyErr(out, line, fmt.Sprintf("build request: %v", err))
			out.Flush()
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("X-Pura-Agent", agent)
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := httpC.Do(req)
		if err != nil {
			writeProxyErr(out, line, fmt.Sprintf("HTTP %v", err))
			out.Flush()
			continue
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			writeProxyErr(out, line, fmt.Sprintf("read body: %v", err))
			out.Flush()
			continue
		}
		body = bytes.TrimRight(body, "\n")
		out.Write(body)
		out.WriteByte('\n')
		out.Flush()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stdin: %w", err)
	}
	return nil
}

func writeProxyErr(w *bufio.Writer, reqLine, message string) {
	var id any = nil
	var peek struct {
		Id any `json:"id"`
	}
	if err := json.Unmarshal([]byte(reqLine), &peek); err == nil {
		id = peek.Id
	}
	errPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32603,
			"message": "pura-cli-proxy: " + message,
		},
	}
	b, _ := json.Marshal(errPayload)
	w.Write(b)
	w.WriteByte('\n')
}
