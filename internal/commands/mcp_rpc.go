// Shared JSON-RPC 2.0 helpers for `pura mcp` — used by `test`, `install`,
// `rotate`, and `doctor`. All calls target Pura's `/mcp` endpoint and
// speak the MCP Streamable HTTP transport (2025-03-26 revision).

package commands

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const mcpProtocolVersion = "2025-03-26"

// httpJsonClient returns a client with a reasonable timeout for
// synchronous MCP calls. 10s is ample for initialize + tools/list; the
// proxy subprocess uses its own 60s client for long-running tool calls.
func httpJsonClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// postRpc posts a JSON-RPC 2.0 request to <base>/mcp and returns the
// decoded response. Spec-compliant with MCP 2025-03-26 Streamable HTTP:
//   - Accept advertises both application/json and text/event-stream
//   - Response Content-Type branches on:
//     text/event-stream → parse SSE frames, return first msg with matching id
//     application/json  → decode body as a single message
//
// Notifications (no id) receive no response body per JSON-RPC 2.0 §4.1;
// callers pass id=0 to signal "don't wait for a response body".
func postRpc(
	ctx context.Context,
	httpC *http.Client,
	base string,
	token string,
	id int,
	method string,
	params map[string]any,
) (map[string]any, error) {
	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if id != 0 {
		body["id"] = id
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/mcp", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpC.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if id == 0 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, nil
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "text/event-stream") {
		return readSseResponse(resp.Body, id)
	}
	var parsed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if errObj, ok := parsed["error"].(map[string]any); ok {
		code := errObj["code"]
		message := errObj["message"]
		return nil, fmt.Errorf("jsonrpc %v: %v", code, message)
	}
	return parsed, nil
}

// readSseResponse consumes a Streamable HTTP SSE stream (text/event-stream)
// and returns the first JSON-RPC message matching `wantId`. Other messages
// (server→client notifications, requests) are skipped silently. Returns
// on stream close or context end.
func readSseResponse(r io.Reader, wantId int) (map[string]any, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	var dataBuf strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if dataBuf.Len() == 0 {
				continue
			}
			payload := dataBuf.String()
			dataBuf.Reset()
			var parsed map[string]any
			if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
				continue
			}
			if rawId, ok := parsed["id"]; ok {
				if n, isNum := rawId.(float64); isNum && int(n) == wantId {
					if errObj, ok := parsed["error"].(map[string]any); ok {
						return nil, fmt.Errorf("jsonrpc %v: %v", errObj["code"], errObj["message"])
					}
					return parsed, nil
				}
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sse stream: %w", err)
	}
	return nil, fmt.Errorf("no JSON-RPC message with id=%d in SSE stream", wantId)
}

// doMcpHandshake performs the MCP lifecycle handshake:
//
//	initialize → notifications/initialized → ready for normal requests.
//
// Returns the negotiated protocolVersion on success. Callers then make
// tools/list, tools/call, etc. on the same base URL.
func doMcpHandshake(
	ctx context.Context,
	httpC *http.Client,
	base string,
	token string,
) (string, error) {
	initResp, err := postRpc(ctx, httpC, base, token, 1, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "pura-cli", "version": versionStr},
	})
	if err != nil {
		return "", fmt.Errorf("initialize: %w", err)
	}
	result, _ := initResp["result"].(map[string]any)
	protoVersion, _ := result["protocolVersion"].(string)
	if _, err := postRpc(ctx, httpC, base, token, 0, "notifications/initialized", map[string]any{}); err != nil {
		_ = err // best-effort; tolerated by Pura
	}
	return protoVersion, nil
}

// listMcpTools is a small convenience used by `test` + `doctor`. Returns
// the sorted list of tool names.
func listMcpTools(ctx context.Context, httpC *http.Client, base, token string) ([]string, error) {
	resp, err := postRpc(ctx, httpC, base, token, 3, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	result, _ := resp["result"].(map[string]any)
	rawTools, _ := result["tools"].([]any)
	names := make([]string, 0, len(rawTools))
	for _, t := range rawTools {
		if m, ok := t.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	return names, nil
}
