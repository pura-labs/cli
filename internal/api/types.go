package api

// ApiResponse is the standard envelope from the Pura API.
type ApiResponse[T any] struct {
	OK    bool      `json:"ok"`
	Data  T         `json:"data,omitempty"`
	Meta  *ApiMeta  `json:"meta,omitempty"`
	Error *ApiError `json:"error,omitempty"`
}

type ApiMeta struct {
	Total    *int    `json:"total,omitempty"`
	TimingMs float64 `json:"timing_ms,omitempty"`
}

type ApiError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Hint       string `json:"hint,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

// CreateRequest — the POST /api/p body.
//
// `Kind` is the primitive identity (doc / sheet / page / slides / canvas /
// image / file / book). `Substrate` is the wire format (markdown / html /
// csv / json / svg / canvas / ascii / refs / image / file). Either is
// enough; the server fills in the other by sniffing content when one is
// missing.
type CreateRequest struct {
	Content   string         `json:"content"`
	Kind      string         `json:"kind,omitempty"`
	Substrate string         `json:"substrate,omitempty"`
	Title     string         `json:"title,omitempty"`
	Slug      string         `json:"slug,omitempty"`
	Theme     string         `json:"theme,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	// Phase 4 — chat-first provenance. When present, /api/p seeds the
	// thread with the describe + ai_summary so the new doc opens with
	// its conversational origin in /edit.
	BootstrapThread *BootstrapThread `json:"bootstrap_thread,omitempty"`
}

// BootstrapThread captures the two-message seed /api/p writes alongside
// the doc when a /bootstrap → Publish flow completes.
type BootstrapThread struct {
	Describe  string `json:"describe"`
	AISummary string `json:"ai_summary"`
	Model     string `json:"model,omitempty"`
	TokensIn  int    `json:"tokens_in,omitempty"`
	TokensOut int    `json:"tokens_out,omitempty"`
}

type CreateResponse struct {
	Slug      string `json:"slug"`
	Token     string `json:"token"`
	URL       string `json:"url"`
	RawURL    string `json:"raw_url"`
	CtxURL    string `json:"ctx_url"`
	Kind      string `json:"kind"`
	Substrate string `json:"substrate"`
	Title     string `json:"title"`
}

type DocResponse struct {
	Slug      string `json:"slug"`
	Kind      string `json:"kind"`
	Substrate string `json:"substrate"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Theme     string `json:"theme"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type DocListItem struct {
	Slug      string `json:"slug"`
	Kind      string `json:"kind"`
	Substrate string `json:"substrate"`
	Title     string `json:"title"`
	Theme     string `json:"theme"`
	Handle    string `json:"handle,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type UpdateRequest struct {
	Content string `json:"content,omitempty"`
	Title   string `json:"title,omitempty"`
	Theme   string `json:"theme,omitempty"`
}

// -------- Auth / device-flow --------

// DeviceStartRequest asks the server to open a new device-flow session.
type DeviceStartRequest struct {
	ClientName string   `json:"client_name,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
}

// DeviceStartResponse is the pair returned from POST /api/auth/device.
type DeviceStartResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURL         string `json:"verification_url"`
	VerificationURLComplete string `json:"verification_url_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DevicePollRequest is sent repeatedly until the user authorizes or the code expires.
type DevicePollRequest struct {
	DeviceCode string `json:"device_code"`
}

// DevicePollUser embeds the minimum user info we want to show at sign-in time.
type DevicePollUser struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Email  string `json:"email"`
}

// DevicePollApproved is the success payload from POST /api/auth/device/poll.
type DevicePollApproved struct {
	Token       string         `json:"token"`
	TokenPrefix string         `json:"token_prefix"`
	KeyID       string         `json:"key_id"`
	Scopes      []string       `json:"scopes"`
	User        DevicePollUser `json:"user"`
}

// -------- Chat (SSE) --------

// ChatRequest is the POST body for /api/p/@h/s/chat.
type ChatRequest struct {
	Instruction    string `json:"instruction"`
	SelectedText   string `json:"selectedText,omitempty"`
	SelectionStart *int   `json:"selectionStart,omitempty"`
	SelectionEnd   *int   `json:"selectionEnd,omitempty"`
	Model          string `json:"model,omitempty"`
}

// ChatSSEEvent mirrors the Phase 4 server-side event union. The old
// `content` and `version` fields are gone — /chat no longer mutates docs.
// Instead the server emits one of:
//
//	message  : {message_id, user_message_id, before_version}
//	token    : {content}  (streaming model output)
//	tool_call: {name, args, error?}  (grid tool invocations mid-stream)
//	proposal : {message_id, status:"pending", diff_summary, destructive, preview}
//	noop     : {message_id, message}  (model decided nothing should change)
//	usage    : {prompt_tokens, completion_tokens, model}
//	error    : {message_id?, message, error_code}
//
// Fields are unioned into one struct because Go has no cheap discriminated-
// union option; callers branch on `Type`. Unknown fields are tolerated so
// forward-compatible server additions don't break older CLIs.
type ChatSSEEvent struct {
	Type string `json:"type"`

	// message
	MessageID     string `json:"message_id,omitempty"`
	UserMessageID string `json:"user_message_id,omitempty"`
	BeforeVersion int    `json:"before_version,omitempty"`

	// token
	Content string `json:"content,omitempty"`

	// tool_call
	ToolName string                 `json:"name,omitempty"`
	ToolArgs map[string]interface{} `json:"args,omitempty"`
	ToolErr  string                 `json:"error,omitempty"`

	// proposal
	ProposalStatus string      `json:"status,omitempty"` // "pending"
	DiffSummary    string      `json:"diff_summary,omitempty"`
	Destructive    bool        `json:"destructive,omitempty"`
	Preview        interface{} `json:"preview,omitempty"`

	// noop
	Message string `json:"message,omitempty"`

	// usage
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	Model            string `json:"model,omitempty"`

	// error
	ErrorCode string `json:"error_code,omitempty"`
}

// AcceptResponse is returned by POST /chat/:mid/accept on success.
type AcceptResponse struct {
	MessageID     string `json:"message_id"`
	BeforeVersion int    `json:"before_version"`
	AfterVersion  int    `json:"after_version"`
	VersionID     string `json:"version_id"`
}

// RejectResponse is returned by POST /chat/:mid/{reject,discard}.
type RejectResponse struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason"`
}

// BootstrapRequest is the POST body for /api/p/bootstrap.
type BootstrapRequest struct {
	Describe string `json:"describe"`
	Starter  string `json:"starter,omitempty"`
	Model    string `json:"model,omitempty"`
}

// BootstrapSSEEvent mirrors the server-side BootstrapEvent union.
// Events: plan, content_delta, content_final, schema, title_suggestion,
// usage, error.
type BootstrapSSEEvent struct {
	Type string `json:"type"`

	// plan
	Kind           string `json:"kind,omitempty"`
	Substrate      string `json:"substrate,omitempty"`
	SlugSuggestion string `json:"slug_suggestion,omitempty"`

	// content_delta / content_final
	Content string `json:"content,omitempty"`

	// schema
	Schema interface{} `json:"schema,omitempty"`

	// title_suggestion
	Title string `json:"title,omitempty"`

	// usage
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	Model            string `json:"model,omitempty"`

	// error
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// BootstrapDraft is the accumulated state the CLI's bootstrap stream
// collector produces — ready to hand to POST /api/p with bootstrap_thread.
type BootstrapDraft struct {
	Describe         string      `json:"describe"`
	Starter          string      `json:"starter,omitempty"`
	Kind             string      `json:"kind"`
	Substrate        string      `json:"substrate"`
	Slug             string      `json:"slug,omitempty"`
	Title            string      `json:"title,omitempty"`
	Content          string      `json:"content"`
	Schema           interface{} `json:"schema,omitempty"`
	Model            string      `json:"model,omitempty"`
	AISummary        string      `json:"ai_summary"`
	PromptTokens     int         `json:"prompt_tokens,omitempty"`
	CompletionTokens int         `json:"completion_tokens,omitempty"`
}

// -------- Stats / events --------

// DocStats is the basic (public) stats payload.
type DocStats struct {
	Views int `json:"views"`
}

// DocDetailedStats matches ?detail=full (owner-only).
type DocDetailedStats struct {
	Views           int            `json:"views"`
	UniqueCountries int            `json:"unique_countries"`
	ViewTypes       map[string]int `json:"view_types"`
	BotRatio        float64        `json:"bot_ratio"`
}

// EventRow is one row from GET /api/p/@h/s/events.
type EventRow struct {
	ID        int64                  `json:"id"`
	Kind      string                 `json:"kind"`
	CreatedAt string                 `json:"created_at"`
	Props     map[string]interface{} `json:"props"`
}

// EventsResponse is the paginated envelope body.
type EventsResponse struct {
	Cursor int64      `json:"cursor"`
	Events []EventRow `json:"events"`
}

// -------- Claim --------

// ClaimRequest posts to /api/claim.
type ClaimRequest struct {
	EditToken string `json:"edit_token"`
}

// ClaimResponse — how many docs were transferred to the caller.
type ClaimResponse struct {
	Claimed int `json:"claimed"`
}

// -------- Versions --------

// VersionListItem is a row from GET /api/p/@h/s/versions.
type VersionListItem struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Shape       string `json:"shape"`
	Media       string `json:"media"`
	Instruction string `json:"instruction"`
	AISummary   string `json:"ai_summary"`
	CreatedBy   string `json:"created_by"`
	Origin      string `json:"origin"`
	CreatedAt   string `json:"created_at"`
}

// DocVersion adds the body; returned by GET /versions/:N and restore.
type DocVersion struct {
	VersionListItem
	DocID   string `json:"doc_id"`
	Content string `json:"content"`
}

// -------- API keys --------

// ApiKeyListItem is one row from GET /api/auth/keys.
//
// Origin is a free-form tag that identifies the surface that minted the key
// ("mcp:<client>", "cli", "web", "device", or "" for legacy rows). Used by
// `pura mcp ls` / `pura mcp doctor` to filter MCP-bound keys.
type ApiKeyListItem struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	Origin     string   `json:"origin,omitempty"`
	LastUsedAt string   `json:"last_used_at"`
	CreatedAt  string   `json:"created_at"`
	RevokedAt  string   `json:"revoked_at"`
}

// CreateKeyRequest is the POST /api/auth/keys payload.
type CreateKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// Origin is optional; when set, must match ^[a-z0-9:_-]{1,40}$ server-side.
	Origin string `json:"origin,omitempty"`
}

// CreateKeyResponse — the plaintext token is returned only here, only once.
type CreateKeyResponse struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Prefix    string   `json:"prefix"`
	Token     string   `json:"token"` // plaintext, show once
	Scopes    []string `json:"scopes"`
	Origin    string   `json:"origin,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// MeResponse mirrors GET /api/auth/me.
type MeResponse struct {
	ID               string `json:"id"`
	Email            string `json:"email"`
	Handle           string `json:"handle"`
	Name             string `json:"name"`
	SessionExpiresAt string `json:"session_expires_at"`
	Via              string `json:"via"` // "api_key" | "session"
}
