package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/itchyny/gojq"
	"golang.org/x/term"
)

// Format controls how output is rendered.
type Format int

const (
	FormatAuto  Format = iota // Styled if TTY, JSON if piped
	FormatJSON                // Always JSON envelope
	FormatQuiet               // Raw data only, no envelope
)

// Writer handles output formatting.
type Writer struct {
	Out      io.Writer
	Err      io.Writer
	Format   Format
	IsTTY    bool
	JQFilter string // jq expression to filter JSON output
}

// NewWriter creates a Writer with auto-detected TTY.
//
// PURA_FORCE_STYLED=1 forces IsTTY=true so tests can assert the styled
// human-readable output without wiring a pseudo-terminal. Opposite escape:
// normal prod detection is via term.IsTerminal on stdout's fd.
func NewWriter(format Format) *Writer {
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	if os.Getenv("PURA_FORCE_STYLED") == "1" {
		isTTY = true
	}
	return &Writer{
		Out:    os.Stdout,
		Err:    os.Stderr,
		Format: format,
		IsTTY:  isTTY,
	}
}

// OK outputs successful data. Accepts optional envelope Options (summary,
// breadcrumbs, meta) — agents consume those to decide the next step.
//
// In --quiet mode we emit just the `data` payload (no envelope) because
// quiet is meant for raw piping; the breadcrumbs are still available to
// callers who use --json.
func (w *Writer) OK(data any, opts ...Option) {
	if w.Format == FormatQuiet {
		w.writeJSON(data)
		return
	}
	if w.useJSON() {
		w.writeJSON(NewOK(data, opts...))
		return
	}
	// Styled (TTY) output is rendered by the caller via Print/Println.
	// Breadcrumbs still print to stderr as plain hints so humans see them.
	w.printStyledBreadcrumbs(opts)
}

// Error outputs an error message. Like OK, accepts options — mostly used
// to attach a "retry" breadcrumb such as `pura auth login`.
func (w *Writer) Error(code, message, hint string, opts ...Option) {
	if w.Format == FormatQuiet {
		w.writeJSON(ErrorDetail{
			Code:    code,
			Message: message,
			Hint:    hint,
		})
		return
	}
	if w.useJSON() {
		w.writeJSON(NewError(code, message, hint, opts...))
		return
	}
	fmt.Fprintf(w.Err, "Error: %s\n", message)
	if hint != "" {
		fmt.Fprintf(w.Err, "Hint: %s\n", hint)
	}
	w.printStyledBreadcrumbs(opts)
}

// printStyledBreadcrumbs surfaces breadcrumbs on a TTY as a dim footer.
// No-op when no breadcrumbs were attached.
func (w *Writer) printStyledBreadcrumbs(opts []Option) {
	if len(opts) == 0 {
		return
	}
	e := Envelope{}
	for _, o := range opts {
		o(&e)
	}
	if len(e.Breadcrumbs) == 0 {
		return
	}
	fmt.Fprintln(w.Err, "")
	fmt.Fprintln(w.Err, "  Next:")
	for _, b := range e.Breadcrumbs {
		if b.Description != "" {
			fmt.Fprintf(w.Err, "    %s\t%s\n", b.Cmd, b.Description)
		} else {
			fmt.Fprintf(w.Err, "    %s\n", b.Cmd)
		}
	}
}

// Print writes a formatted string to stdout (styled mode only).
func (w *Writer) Print(format string, args ...any) {
	if !w.useJSON() {
		fmt.Fprintf(w.Out, format, args...)
	}
}

// Println writes a line to stdout (styled mode only).
func (w *Writer) Println(args ...any) {
	if !w.useJSON() {
		fmt.Fprintln(w.Out, args...)
	}
}

// JSON outputs raw JSON data (for --quiet mode or piping).
func (w *Writer) JSON(data any) {
	w.writeJSON(data)
}

func (w *Writer) useJSON() bool {
	switch w.Format {
	case FormatJSON, FormatQuiet:
		return true
	case FormatAuto:
		return !w.IsTTY
	default:
		return false
	}
}

func (w *Writer) writeJSON(data any) {
	if w.JQFilter != "" {
		w.applyJQ(data)
		return
	}
	enc := json.NewEncoder(w.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		// Encoding errors usually mean a caller passed a value with an
		// unmarshalable type (channel, func) or a Marshaler that returned
		// an error. Surface it on stderr so it doesn't vanish silently —
		// callers still get whatever partial bytes made it through.
		fmt.Fprintf(w.Err, "output: encode error: %s\n", err)
	}
}

func (w *Writer) applyJQ(data any) {
	query, err := gojq.Parse(w.JQFilter)
	if err != nil {
		fmt.Fprintf(w.Err, "Invalid jq expression: %s\n", err)
		return
	}

	// Normalize data through JSON round-trip for gojq compatibility. We
	// surface both marshal and unmarshal failures because either means the
	// jq filter will run on a bogus/empty value, which is a lot more
	// confusing than a one-line error.
	raw, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintf(w.Err, "output: marshal before jq failed: %s\n", err)
		return
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		fmt.Fprintf(w.Err, "output: unmarshal before jq failed: %s\n", err)
		return
	}

	iter := query.Run(normalized)
	enc := json.NewEncoder(w.Out)
	enc.SetIndent("", "  ")
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			fmt.Fprintf(w.Err, "jq error: %s\n", err)
			return
		}
		if err := enc.Encode(v); err != nil {
			fmt.Fprintf(w.Err, "output: jq encode error: %s\n", err)
			return
		}
	}
}
