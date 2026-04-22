package output

import (
	"bytes"
	"strings"
	"testing"
)

// Writer tests that exercise the TTY/styled paths, the jq filter, and
// the error-path renderers. These branches accumulate reliability bugs
// if nobody's watching, so we keep coverage honest.

func TestOK_JSONModeEmitsEnvelope(t *testing.T) {
	var out bytes.Buffer
	w := &Writer{Out: &out, Format: FormatJSON, IsTTY: false}
	w.OK(map[string]int{"n": 42},
		WithSummary("hello"),
		WithBreadcrumb("next", "pura next", "do more"),
	)
	// Pretty-printed JSON → expect spaces after colons.
	s := out.String()
	if !strings.Contains(s, `"summary": "hello"`) {
		t.Errorf("envelope missing summary: %s", s)
	}
	if !strings.Contains(s, `"action": "next"`) {
		t.Errorf("envelope missing breadcrumb action: %s", s)
	}
}

func TestOK_StyledBreadcrumbsPrintedToErr(t *testing.T) {
	// Auto mode + TTY → styled output, breadcrumbs go to Err.
	var outBuf, errBuf bytes.Buffer
	w := &Writer{Out: &outBuf, Err: &errBuf, Format: FormatAuto, IsTTY: true}
	w.OK(nil,
		WithBreadcrumb("view", "pura open abc", "Open in browser"),
		WithBreadcrumb("edit", "pura chat abc \"...\"", ""),
	)
	if outBuf.Len() != 0 {
		t.Errorf("styled OK should not touch stdout: %q", outBuf.String())
	}
	s := errBuf.String()
	if !strings.Contains(s, "Next:") {
		t.Errorf("missing Next: header in: %q", s)
	}
	if !strings.Contains(s, "pura open abc") {
		t.Errorf("missing first breadcrumb: %q", s)
	}
	if !strings.Contains(s, "pura chat abc") {
		t.Errorf("missing second breadcrumb: %q", s)
	}
}

func TestOK_StyledNoBreadcrumbs_NoOp(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	w := &Writer{Out: &outBuf, Err: &errBuf, Format: FormatAuto, IsTTY: true}
	w.OK("payload") // no options → no footer
	if errBuf.Len() != 0 {
		t.Errorf("expected silent footer, got: %q", errBuf.String())
	}
}

func TestError_StyledEmitsMessageHintAndCrumbs(t *testing.T) {
	var errBuf bytes.Buffer
	w := &Writer{Out: &bytes.Buffer{}, Err: &errBuf, Format: FormatAuto, IsTTY: true}
	w.Error("unauthorized", "Missing token", "Run `pura auth login`",
		WithBreadcrumb("retry", "pura auth login", "Sign in"),
	)
	s := errBuf.String()
	if !strings.Contains(s, "Error: Missing token") {
		t.Errorf("missing Error: line: %q", s)
	}
	if !strings.Contains(s, "Hint: Run `pura auth login`") {
		t.Errorf("missing Hint: line: %q", s)
	}
	if !strings.Contains(s, "pura auth login") {
		t.Errorf("missing breadcrumb: %q", s)
	}
}

func TestError_JSONModeEnvelope(t *testing.T) {
	var out bytes.Buffer
	w := &Writer{Out: &out, Err: &bytes.Buffer{}, Format: FormatJSON, IsTTY: false}
	w.Error("not_found", "missing", "check spelling")
	// writeJSON uses SetIndent, so the output is pretty-printed. Match
	// against the pretty shape rather than the compact one.
	s := out.String()
	if !strings.Contains(s, `"ok": false`) || !strings.Contains(s, `"code": "not_found"`) {
		t.Errorf("envelope shape wrong: %s", s)
	}
}

func TestPrint_OnlyWritesInStyledMode(t *testing.T) {
	var out bytes.Buffer
	// JSON mode should suppress Print().
	w := &Writer{Out: &out, Format: FormatJSON, IsTTY: true}
	w.Print("hello %s", "world")
	if out.Len() != 0 {
		t.Errorf("Print in JSON mode should be silent, got: %q", out.String())
	}

	// Styled TTY → Print goes to stdout.
	out.Reset()
	w = &Writer{Out: &out, Format: FormatAuto, IsTTY: true}
	w.Print("hello %s", "world")
	if s := out.String(); s != "hello world" {
		t.Errorf("Print styled = %q", s)
	}
}

func TestPrintln_OnlyWritesInStyledMode(t *testing.T) {
	var out bytes.Buffer
	w := &Writer{Out: &out, Format: FormatAuto, IsTTY: true}
	w.Println("hi")
	if s := out.String(); s != "hi\n" {
		t.Errorf("Println = %q", s)
	}

	out.Reset()
	w = &Writer{Out: &out, Format: FormatJSON, IsTTY: true}
	w.Println("no")
	if out.Len() != 0 {
		t.Errorf("Println in JSON mode should be silent: %q", out.String())
	}
}

func TestJSONHelperIgnoresFormat(t *testing.T) {
	// Writer.JSON is the "always emit JSON no matter what" escape hatch.
	var out bytes.Buffer
	w := &Writer{Out: &out, Format: FormatAuto, IsTTY: true}
	w.JSON(map[string]int{"a": 1})
	if !strings.Contains(out.String(), `"a": 1`) {
		t.Errorf("JSON() should write even on styled TTY; got %q", out.String())
	}
}

func TestJQFilter_TransformsOutput(t *testing.T) {
	var out bytes.Buffer
	w := &Writer{Out: &out, Format: FormatJSON, IsTTY: false, JQFilter: ".data.n"}
	w.OK(map[string]int{"n": 42})
	// gojq prints each yield on its own line — the expected shape is a
	// single number with a trailing newline.
	got := strings.TrimSpace(out.String())
	if got != "42" {
		t.Errorf("jq filter output = %q, want 42", got)
	}
}

func TestJQFilter_InvalidExpressionReportsToErr(t *testing.T) {
	var out, errBuf bytes.Buffer
	w := &Writer{Out: &out, Err: &errBuf, Format: FormatJSON, JQFilter: "bad@@syntax"}
	w.OK(map[string]int{"n": 1})
	if !strings.Contains(errBuf.String(), "Invalid jq expression") {
		t.Errorf("expected parse error on stderr, got: %q", errBuf.String())
	}
}

func TestNewWriter_DefaultsToStderrAndStdout(t *testing.T) {
	w := NewWriter(FormatAuto)
	if w.Out == nil || w.Err == nil {
		t.Error("NewWriter should default Out/Err to os.Stdout/os.Stderr")
	}
	if w.Format != FormatAuto {
		t.Error("format round-trip failed")
	}
}
