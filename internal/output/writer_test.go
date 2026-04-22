package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestUseJSON_FormatJSON(t *testing.T) {
	w := &Writer{Format: FormatJSON, IsTTY: true}
	if !w.useJSON() {
		t.Error("FormatJSON should return true for useJSON()")
	}
}

func TestUseJSON_FormatQuiet(t *testing.T) {
	w := &Writer{Format: FormatQuiet, IsTTY: true}
	if !w.useJSON() {
		t.Error("FormatQuiet should return true for useJSON()")
	}
}

func TestUseJSON_FormatAutoTTY(t *testing.T) {
	w := &Writer{Format: FormatAuto, IsTTY: true}
	if w.useJSON() {
		t.Error("FormatAuto + IsTTY=true should return false for useJSON()")
	}
}

func TestUseJSON_FormatAutoNotTTY(t *testing.T) {
	w := &Writer{Format: FormatAuto, IsTTY: false}
	if !w.useJSON() {
		t.Error("FormatAuto + IsTTY=false should return true for useJSON()")
	}
}

func TestOK_FormatQuiet(t *testing.T) {
	var out bytes.Buffer
	w := &Writer{Out: &out, Format: FormatQuiet, IsTTY: true}

	w.OK(map[string]string{"slug": "abc123"})

	var parsed map[string]string
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed["slug"] != "abc123" {
		t.Fatalf("slug = %q, want abc123", parsed["slug"])
	}
	if _, hasOK := parsed["ok"]; hasOK {
		t.Fatal("quiet output should not include envelope fields")
	}
}

func TestError_FormatQuiet(t *testing.T) {
	var out bytes.Buffer
	w := &Writer{Out: &out, Err: &out, Format: FormatQuiet, IsTTY: true}

	w.Error("unauthorized", "No token configured", "Run config set token")

	var parsed map[string]string
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed["code"] != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized", parsed["code"])
	}
	if _, hasOK := parsed["ok"]; hasOK {
		t.Fatal("quiet error should not include envelope fields")
	}
}
