package output

import "fmt"

// Envelope v2 — the standard JSON response shape for every CLI command.
//
//   {
//     "ok":          true,
//     "data":        {...},
//     "summary":     "Published to pura.so/@you/abc123 (markdown, 1.2 KB)",
//     "breadcrumbs": [
//       {"action":"view",    "cmd":"pura open abc123",         "description":"Open in browser"},
//       {"action":"edit",    "cmd":"pura chat abc123 \"...\"", "description":"AI-edit this doc"}
//     ],
//     "error":       null
//   }
//
// Summary is a single human-readable sentence about what just happened.
// Agents use it as a quick sanity check; humans use it as the "success line"
// next to the styled output.
//
// Breadcrumbs are the *agent's superpower*: every command ends with an
// inline list of the 1–4 most likely next commands, tagged by intent. An
// AI consumer can pick the next step without keeping a mental model of the
// whole CLI surface area; a human consumer gets a subtle hint of what to
// try next.
//
// Borrowed wholesale from basecamp-cli / fizzy-cli — which both proved the
// pattern works well for agent navigation.

// Breadcrumb describes one suggested follow-up command.
type Breadcrumb struct {
	// Action is a short intent tag — agents route on this.
	// Conventions: "view", "edit", "retry", "revert", "share", "delete",
	// "list", "open", "configure", "docs".
	Action string `json:"action"`

	// Cmd is the exact shell-ready command string, including the `pura `
	// prefix. Must be runnable verbatim. Placeholders in angle brackets
	// (e.g. `<slug>`) are acceptable when a real value isn't known yet.
	Cmd string `json:"cmd"`

	// Description is a one-sentence rationale. Optional but recommended.
	Description string `json:"description,omitempty"`
}

// Envelope is the full response wrapper. Fields use `omitempty` so absent
// ones disappear from the JSON — keeps small responses small.
type Envelope struct {
	OK          bool         `json:"ok"`
	Data        any          `json:"data,omitempty"`
	Summary     string       `json:"summary,omitempty"`
	Breadcrumbs []Breadcrumb `json:"breadcrumbs,omitempty"`
	Error       any          `json:"error,omitempty"`
	Meta        any          `json:"meta,omitempty"`
}

// ErrorDetail is the structured error body.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Option mutates an Envelope during construction. Pass via NewOK / NewError
// or via the variadic params on Writer.OK / Writer.Error.
//
// Why functional options: call sites stay terse when there's nothing extra
// to say, but can layer on summary + breadcrumbs without a builder chain.
type Option func(*Envelope)

// WithSummary attaches a one-line human-readable summary.
func WithSummary(format string, args ...any) Option {
	return func(e *Envelope) { e.Summary = sprintf(format, args...) }
}

// WithBreadcrumb appends a single suggested next command.
func WithBreadcrumb(action, cmd, description string) Option {
	return func(e *Envelope) {
		e.Breadcrumbs = append(e.Breadcrumbs, Breadcrumb{
			Action:      action,
			Cmd:         cmd,
			Description: description,
		})
	}
}

// WithBreadcrumbs appends several suggestions at once.
func WithBreadcrumbs(crumbs ...Breadcrumb) Option {
	return func(e *Envelope) { e.Breadcrumbs = append(e.Breadcrumbs, crumbs...) }
}

// WithMeta attaches arbitrary meta (timings, pagination, …).
func WithMeta(meta any) Option {
	return func(e *Envelope) { e.Meta = meta }
}

// NewOK creates a success envelope with optional summary / breadcrumbs / meta.
//
// Simple case:     NewOK(data)
// With summary:    NewOK(data, WithSummary("Published %s", slug))
// Full:            NewOK(data,
//
//	WithSummary("…"),
//	WithBreadcrumb("view", "pura open abc", "Open in browser"),
//	WithBreadcrumb("edit", "pura chat abc \"…\"", "AI-edit"))
func NewOK(data any, opts ...Option) Envelope {
	e := Envelope{OK: true, Data: data}
	for _, o := range opts {
		o(&e)
	}
	return e
}

// NewError creates a failure envelope. Breadcrumbs on errors are especially
// valuable for agents — e.g. "auth_required" naturally pairs with a
// `pura auth login` breadcrumb.
func NewError(code, message, hint string, opts ...Option) Envelope {
	detail := ErrorDetail{Code: code, Message: message}
	if hint != "" {
		detail.Hint = hint
	}
	e := Envelope{OK: false, Error: detail}
	for _, o := range opts {
		o(&e)
	}
	return e
}

// sprintf lets WithSummary accept either a plain string (args == 0) or a
// Printf-style format + args. Cheaper than forcing callers to pre-format.
func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
