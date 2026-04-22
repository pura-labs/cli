package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// MaxResponseBytes caps any non-streaming response body we'll buffer before
// parsing. 10 MiB is orders of magnitude above any realistic Pura payload
// (a 100k-line doc is ~3 MiB of raw markdown); anything larger is almost
// certainly a malicious or misbehaving server and we'd rather fail fast than
// run the CLI out of memory.
const MaxResponseBytes = 10 << 20

// MaxErrorBodyBytes bounds how much of an error response body we'll buffer
// when the endpoint returned non-2xx. Error bodies are usually <1 KiB; we
// keep a small upper bound so a 5xx HTML fallback page can't balloon us.
const MaxErrorBodyBytes = 64 << 10

// Client talks to the Pura API.
type Client struct {
	BaseURL    string
	Token      string
	Handle     string // User handle (accepts alice or @alice; defaults to "_")
	HTTPClient *http.Client
	Verbose    bool

	// Ctx is the context used for every outbound HTTP request. Nil means
	// context.Background(). Set from `cmd.Context()` in each command's
	// RunE so Ctrl-C / signal cancellation propagates into network I/O
	// instead of blocking on a 30s HTTP timeout.
	Ctx context.Context
}

// NewClient creates a Pura API client with sensible defaults.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		Handle:  AnonymousHandle,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// context returns the effective context for this request. Always non-nil.
func (c *Client) context() context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

// docPath returns the @handle/slug segment for API calls (no leading slash).
func (c *Client) docPath(slug string) string {
	return NormalizeDocPath(c.Handle, slug)
}

// DocumentURL returns the public URL for a document.
func (c *Client) DocumentURL(slug string) string {
	return PublicURL(c.BaseURL, c.Handle, slug)
}

// Create publishes a new document.
func (c *Client) Create(req CreateRequest) (*CreateResponse, error) {
	var resp ApiResponse[CreateResponse]
	if err := c.do("POST", "/api/p", req, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// Get retrieves a document by slug.
func (c *Client) Get(slug string) (*DocResponse, error) {
	var resp ApiResponse[DocResponse]
	if err := c.do("GET", "/api/p/"+c.docPath(slug), nil, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// GetRaw retrieves raw content of a document.
func (c *Client) GetRaw(slug string) (string, error) {
	status, body, err := c.doRequest("GET", "/"+c.docPath(slug)+"/raw", nil, nil, "")
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", fmt.Errorf("document not found: %s", slug)
	}
	if status >= http.StatusBadRequest {
		return "", errorFromResponse(status, body)
	}
	return string(body), nil
}

// GetContext retrieves AI context for a document.
func (c *Client) GetContext(slug string) (json.RawMessage, error) {
	status, body, err := c.doRequest("GET", "/"+c.docPath(slug)+"/ctx", nil, nil, "")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("document not found: %s", slug)
	}
	if status >= http.StatusBadRequest {
		return nil, errorFromResponse(status, body)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid JSON response from ctx endpoint")
	}
	return json.RawMessage(body), nil
}

// Update modifies an existing document.
func (c *Client) Update(slug string, req UpdateRequest) (*DocResponse, error) {
	var resp ApiResponse[DocResponse]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("PUT", "/api/p/"+c.docPath(slug), req, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// Delete removes a document.
func (c *Client) Delete(slug string) error {
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	status, body, err := c.doRequest("DELETE", "/api/p/"+c.docPath(slug), nil, headers, "")
	if err != nil {
		return err
	}
	if status == http.StatusNoContent {
		return nil
	}
	if status >= http.StatusBadRequest {
		return errorFromResponse(status, body)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var apiResp ApiResponse[any]
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("unexpected status %d", status)
	}
	if !apiResp.OK {
		return apiErr(apiResp.Error)
	}
	return nil
}

// List retrieves anonymous documents minted with `token` (a per-doc
// edit_token, NOT an API key). Authenticated users should call ListForUser
// instead, which authenticates via `Authorization: Bearer` rather than a
// query-string token.
//
// We hard-guard against accidental API-key leaks here: the server exposes
// listing-by-token in the URL per its documented contract, but `sk_pura_…`
// API keys are long-lived credentials and should never ride a URL, even
// over HTTPS (URL paths get captured by proxies, browser history,
// webserver access logs, and many Cloudflare/Sentry integrations).
func (c *Client) List(token string) ([]DocListItem, error) {
	var resp ApiResponse[[]DocListItem]
	path := "/api/p"
	if token != "" {
		if strings.HasPrefix(token, "sk_pura_") {
			return nil, fmt.Errorf("refusing to list-by-token with an API key — use ListForUser instead")
		}
		// url.Values handles the full spec escape table; manual string
		// concat would break on `+`, `&`, etc. if an edit token ever
		// contained one.
		v := url.Values{}
		v.Set("token", token)
		path += "?" + v.Encode()
	}
	if err := c.do("GET", path, nil, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return resp.Data, nil
}

// ListForUser fetches the authenticated user's docs (via Bearer / ?user=me).
// Requires c.Token to be a valid API key with docs:read scope.
func (c *Client) ListForUser() ([]DocListItem, error) {
	var resp ApiResponse[[]DocListItem]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("GET", "/api/p?user=me", nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return resp.Data, nil
}

// -------- Device-flow auth --------

// DeviceStart opens a new device-authorization session (RFC 8628).
// No bearer token required — this is the very first step of sign-in.
func (c *Client) DeviceStart(req DeviceStartRequest) (*DeviceStartResponse, error) {
	var resp ApiResponse[DeviceStartResponse]
	if err := c.do("POST", "/api/auth/device", req, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// DevicePollResult mirrors the polling state. Exactly one of Approved or
// Error is populated. Error carries RFC-8628 codes like authorization_pending,
// slow_down, access_denied, expired.
type DevicePollResult struct {
	Approved *DevicePollApproved
	Error    *ApiError
	Status   int
}

// DevicePoll polls the authorization endpoint once. The caller drives the
// polling loop so it can respect context cancellation, user ctrl-c, and the
// server-advertised interval.
func (c *Client) DevicePoll(deviceCode string) (*DevicePollResult, error) {
	body, err := json.Marshal(DevicePollRequest{DeviceCode: deviceCode})
	if err != nil {
		return nil, fmt.Errorf("marshaling poll: %w", err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	status, data, err := c.doRequest("POST", "/api/auth/device/poll", bytes.NewReader(body), headers, "application/json")
	if err != nil {
		return nil, err
	}
	var resp ApiResponse[DevicePollApproved]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decoding poll response: %w", err)
	}
	out := &DevicePollResult{Status: status}
	if resp.OK {
		out.Approved = &resp.Data
		return out, nil
	}
	if resp.Error != nil {
		out.Error = resp.Error
	} else {
		out.Error = &ApiError{Code: "server_error", Message: fmt.Sprintf("unexpected status %d", status)}
	}
	return out, nil
}

// -------- Chat (SSE) --------

// Chat streams the AI editor's responses for a doc, invoking onEvent once per
// parsed ChatSSEEvent and blocking until the server sends [DONE] or the
// context is cancelled. Returns any terminal error (network, parse, HTTP).
//
// Protocol note: the server emits newline-terminated `data: <json>\n\n`
// frames without the `event:` prefix; each JSON carries its own `type`.
// The terminator is the literal `data: [DONE]`.
func (c *Client) Chat(slug string, req ChatRequest, onEvent func(ChatSSEEvent)) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(c.context(), "POST", c.BaseURL+"/api/p/"+c.docPath(slug)+"/chat", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", "pura-cli/0.1.0")
	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}

	// Streams are open-ended; override the client-wide 30s timeout with a
	// much longer per-request deadline so long generations don't abort.
	// Cancellation still flows through c.Ctx so Ctrl-C terminates promptly.
	httpClient := *c.HTTPClient
	httpClient.Timeout = 0

	if c.Verbose {
		fmt.Fprintf(os.Stderr, "> POST %s (SSE)\n", httpReq.URL.Redacted())
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes))
		if readErr != nil && c.Verbose {
			fmt.Fprintf(os.Stderr, "< error body truncated: %s\n", readErr)
		}
		return errorFromResponse(resp.StatusCode, body)
	}

	return parseSSE(resp.Body, onEvent, c.Verbose)
}

// parseSSE reads frames from the server and invokes onEvent per JSON payload.
// Frame grammar is intentionally forgiving: we accept `data: …` lines, ignore
// blank separators, treat `data: [DONE]` as a clean terminator, and skip any
// `:heartbeat` comments. Malformed frames are counted and, in verbose mode,
// reported on stderr so a runaway server doesn't drop events silently.
func parseSSE(r io.Reader, onEvent func(ChatSSEEvent), verbose bool) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var malformed int
	for {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("reading SSE: %w", err)
		}

		if len(line) > 0 {
			// Drop the trailing "\n"; the blank-line separator is handled by
			// the ReadString loop naturally.
			line = strings.TrimRight(line, "\r\n")
			if line != "" {
				// Heartbeat comments per SSE spec — ignore.
				if strings.HasPrefix(line, ":") {
					// no-op
				} else if strings.HasPrefix(line, "event:") {
					// Some servers emit explicit `event: <name>` before `data:`; we
					// ignore those since our type is inside the JSON payload.
				} else if strings.HasPrefix(line, "data:") {
					payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if payload == "[DONE]" {
						if verbose && malformed > 0 {
							fmt.Fprintf(os.Stderr, "< sse: skipped %d malformed frame(s)\n", malformed)
						}
						return nil
					}
					var evt ChatSSEEvent
					if err := json.Unmarshal([]byte(payload), &evt); err == nil {
						onEvent(evt)
					} else {
						// Malformed frames are skipped so long-running generations
						// aren't aborted by one bad chunk; but we count them so
						// verbose mode can surface the drop and production users
						// can notice a systemic issue.
						malformed++
						if verbose {
							fmt.Fprintf(os.Stderr, "< sse: skip malformed frame (%s): %s\n", err, truncateForLog(payload, 120))
						}
					}
				}
				// Unknown lines are ignored rather than aborting the stream.
			}
		}
		if err == io.EOF {
			if verbose && malformed > 0 {
				fmt.Fprintf(os.Stderr, "< sse: skipped %d malformed frame(s) before EOF\n", malformed)
			}
			return io.ErrUnexpectedEOF
		}
	}
}

// truncateForLog clips a string to n runes plus an ellipsis so a malformed
// SSE payload doesn't dump megabytes on stderr.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// -------- Phase 4: Accept / Reject / Bootstrap --------

// MessageListItem is a row from GET /api/p/@h/s/messages. The CLI only
// needs the fields that matter for the --resolve flow; extend as needed.
type MessageListItem struct {
	ID             string `json:"id"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	Status         string `json:"status"`
	ProposalStatus string `json:"proposal_status,omitempty"`
	BeforeVersion  int    `json:"before_version,omitempty"`
	AfterVersion   int    `json:"after_version,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// ListMessages fetches recent chat messages for this doc (owner-scoped).
// Used by the CLI `--resolve` flow to find a stuck pending proposal.
func (c *Client) ListMessages(slug string, limit int) ([]MessageListItem, error) {
	path := "/api/p/" + c.docPath(slug) + "/messages"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var resp ApiResponse[[]MessageListItem]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("GET", path, nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return resp.Data, nil
}

// AcceptProposal applies a pending AI proposal. Returns the new version
// number on success. 409 (stale) / 410 (expired) / 422 (already resolved)
// come back as structured api.ApiError via errorFromResponse.
func (c *Client) AcceptProposal(slug, messageID string) (*AcceptResponse, error) {
	var resp ApiResponse[AcceptResponse]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	path := "/api/p/" + c.docPath(slug) + "/chat/" + messageID + "/accept"
	if err := c.do("POST", path, []byte("{}"), headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// RejectProposal marks a pending proposal as rejected. `kind` is "reject"
// or "discard" (both flip to terminal `rejected`; the kind is kept for
// telemetry).
func (c *Client) RejectProposal(slug, messageID, kind string) (*RejectResponse, error) {
	if kind != "reject" && kind != "discard" {
		return nil, fmt.Errorf("kind must be 'reject' or 'discard'")
	}
	var resp ApiResponse[RejectResponse]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	path := "/api/p/" + c.docPath(slug) + "/chat/" + messageID + "/" + kind
	if err := c.do("POST", path, []byte("{}"), headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// Bootstrap streams the planner events for /api/p/bootstrap. The endpoint
// does NOT write to the DB — the caller accumulates the draft and POSTs
// /api/p with a bootstrap_thread to publish.
func (c *Client) Bootstrap(req BootstrapRequest, onEvent func(BootstrapSSEEvent)) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling bootstrap request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(c.context(), "POST", c.BaseURL+"/api/p/bootstrap", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", "pura-cli/0.1.0")
	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}

	httpClient := *c.HTTPClient
	httpClient.Timeout = 0
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes))
		return errorFromResponse(resp.StatusCode, body)
	}
	return parseBootstrapSSE(resp.Body, onEvent)
}

// parseBootstrapSSE reuses the same line parser as parseSSE but decodes
// into BootstrapSSEEvent. Kept separate to avoid leaking a union type.
func parseBootstrapSSE(r io.Reader, onEvent func(BootstrapSSEEvent)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("reading SSE: %w", err)
		}
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if line != "" && strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if payload == "[DONE]" {
					return nil
				}
				var evt BootstrapSSEEvent
				if jerr := json.Unmarshal([]byte(payload), &evt); jerr == nil {
					onEvent(evt)
				}
			}
		}
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
	}
}

// -------- Stats / events --------

// GetStats returns the public view counter.
func (c *Client) GetStats(slug string) (*DocStats, error) {
	var resp ApiResponse[DocStats]
	if err := c.do("GET", "/api/p/"+c.docPath(slug)+"/stats", nil, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// GetDetailedStats requires ownership — Bearer must be the doc owner.
func (c *Client) GetDetailedStats(slug string) (*DocDetailedStats, error) {
	var resp ApiResponse[DocDetailedStats]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("GET", "/api/p/"+c.docPath(slug)+"/stats?detail=full", nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// EventsOptions — nil values use server defaults.
type EventsOptions struct {
	Since int64  // pagination cursor (event id)
	Limit int    // default 50, max 200
	Kinds string // comma-separated public kinds ("doc.updated,comment.added")
}

// ListEvents fetches one page. Follow loops are built by callers on top.
func (c *Client) ListEvents(slug string, opts EventsOptions) (*EventsResponse, error) {
	path := "/api/p/" + c.docPath(slug) + "/events"
	q := ""
	add := func(k, v string) {
		if v == "" {
			return
		}
		if q == "" {
			q = "?" + k + "=" + v
		} else {
			q += "&" + k + "=" + v
		}
	}
	if opts.Since > 0 {
		add("since", fmt.Sprintf("%d", opts.Since))
	}
	if opts.Limit > 0 {
		add("limit", fmt.Sprintf("%d", opts.Limit))
	}
	if opts.Kinds != "" {
		add("kinds", opts.Kinds)
	}

	var resp ApiResponse[EventsResponse]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("GET", path+q, nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// -------- Claim --------

// Claim transfers every anonymous doc created with `editToken` to the
// caller's account. Requires an authenticated user (Bearer API key with
// docs:write scope, or session cookie).
func (c *Client) Claim(editToken string) (*ClaimResponse, error) {
	var resp ApiResponse[ClaimResponse]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("POST", "/api/claim", ClaimRequest{EditToken: editToken}, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// -------- Versions --------

// ListVersions returns the version history for a doc.
func (c *Client) ListVersions(slug string) ([]VersionListItem, error) {
	var resp ApiResponse[[]VersionListItem]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("GET", "/api/p/"+c.docPath(slug)+"/versions", nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return resp.Data, nil
}

// GetVersion fetches one version's full body.
func (c *Client) GetVersion(slug string, version int) (*DocVersion, error) {
	var resp ApiResponse[DocVersion]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	path := fmt.Sprintf("/api/p/%s/versions/%d", c.docPath(slug), version)
	if err := c.do("GET", path, nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// RestoreVersion rolls the doc forward to target version by creating a new
// version that mirrors it. Returns the freshly-created version row.
func (c *Client) RestoreVersion(slug string, version int) (*DocVersion, error) {
	var resp ApiResponse[DocVersion]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	path := fmt.Sprintf("/api/p/%s/versions/%d/restore", c.docPath(slug), version)
	if err := c.do("POST", path, struct{}{}, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// -------- API keys --------

// ListKeys returns the user's non-revoked API keys.
func (c *Client) ListKeys() ([]ApiKeyListItem, error) {
	var resp ApiResponse[[]ApiKeyListItem]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("GET", "/api/auth/keys", nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return resp.Data, nil
}

// CreateKey mints a new API key. The plaintext token is returned in the
// response exactly once — the server stores only a hash.
func (c *Client) CreateKey(req CreateKeyRequest) (*CreateKeyResponse, error) {
	var resp ApiResponse[CreateKeyResponse]
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	if err := c.do("POST", "/api/auth/keys", req, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// RevokeKey soft-deletes a key. The server responds 204 on success.
func (c *Client) RevokeKey(id string) error {
	headers := map[string]string{"Authorization": "Bearer " + c.Token}
	status, body, err := c.doRequest("DELETE", "/api/auth/keys/"+id, nil, headers, "")
	if err != nil {
		return err
	}
	if status == 204 {
		return nil
	}
	return errorFromResponse(status, body)
}

// Me returns the currently-authenticated user. Works with either API key Bearer
// or session cookie; the CLI uses Bearer. Useful for `pura auth status --verify`.
func (c *Client) Me() (*MeResponse, error) {
	var resp ApiResponse[MeResponse]
	headers := map[string]string{}
	if c.Token != "" {
		headers["Authorization"] = "Bearer " + c.Token
	}
	if err := c.do("GET", "/api/auth/me", nil, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, apiErr(resp.Error)
	}
	return &resp.Data, nil
}

// do executes an HTTP request with JSON body/response.
func (c *Client) do(method, path string, body any, headers map[string]string, out any) error {
	var bodyReader io.Reader
	contentType := ""
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
		contentType = "application/json"
	}

	status, data, err := c.doRequest(method, path, bodyReader, headers, contentType)
	if err != nil {
		return err
	}
	if status >= http.StatusBadRequest {
		return errorFromResponse(status, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

func (c *Client) doRequest(method, path string, body io.Reader, headers map[string]string, contentType string) (int, []byte, error) {
	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(c.context(), method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("creating request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "pura-cli/0.1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "> %s %s\n", method, req.URL.Redacted())
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	// Cap buffered body at MaxResponseBytes — a 1 GB malicious or runaway
	// response would otherwise OOM the CLI. We read one byte past the limit
	// so we can detect truncation and surface a clear error rather than a
	// confusing JSON parse failure down the line.
	limited := io.LimitReader(resp.Body, MaxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return 0, nil, fmt.Errorf("reading response: %w", err)
	}
	if int64(len(data)) > MaxResponseBytes {
		return 0, nil, fmt.Errorf("response body exceeded %d bytes; aborting", MaxResponseBytes)
	}

	if c.Verbose {
		fmt.Fprintf(
			os.Stderr,
			"< %d %s (%dB, %s)\n",
			resp.StatusCode,
			http.StatusText(resp.StatusCode),
			len(data),
			time.Since(start).Round(time.Millisecond),
		)
	}

	return resp.StatusCode, data, nil
}

// errorFromResponse converts a non-2xx response into a *Error carrying the
// HTTP status. When the body isn't a Pura envelope (e.g. a CF gateway page)
// we still attach the status so the exit-code layer can route correctly.
func errorFromResponse(status int, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return &Error{Status: status, Message: fmt.Sprintf("unexpected status %d", status)}
	}

	var apiResp ApiResponse[any]
	if err := json.Unmarshal(data, &apiResp); err == nil && apiResp.Error != nil {
		return apiErrWithStatus(status, apiResp.Error)
	}

	return &Error{Status: status, Message: fmt.Sprintf("unexpected status %d", status)}
}

// apiErr is used on paths where we've already parsed a successful-shaped
// envelope but ok=false. Status is best-effort zero (caller doesn't have
// the HTTP status handy); exit-code layer falls back to ExitAPI in that
// case, which is the safest bucket for "the server spoke but refused".
func apiErr(e *ApiError) error {
	return apiErrWithStatus(0, e)
}

func apiErrWithStatus(status int, e *ApiError) error {
	if e == nil {
		return &Error{Status: status, Message: "unknown API error"}
	}
	return &Error{
		Status:  status,
		Code:    e.Code,
		Message: e.Message,
		Hint:    e.Hint,
	}
}
