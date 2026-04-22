// Format codecs for the client config files `pura mcp` edits.
//
// Each client uses one of four formats:
//
//   json   — Claude Desktop / Claude Code / Cursor / Windsurf / Zed /
//            OpenCode / Gemini CLI. Standard encoding/json, 2-space indent.
//   jsonc  — VS Code's `mcp.json`. JSON with trailing commas and // comments.
//            Parsed + re-emitted via hujson so user comments survive edits.
//   yaml   — Goose's `config.yaml`. gopkg.in/yaml.v3 for round-trip.
//   toml   — Codex's `config.toml`. BurntSushi/toml for round-trip.
//
// The codec returns a Go `map[string]any` tree for in-memory editing;
// editEntry / editRootKey helpers in mcp_config_io.go operate on that
// tree. Saving re-serializes with best-effort comment / key-order
// preservation, falling back to a fresh marshal when the file is new.

package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
)

// loadConfigFile reads `path` and returns (tree, originalBytes, err).
// If the file doesn't exist, returns (empty map, empty slice, nil) — a
// missing config is a valid starting state for `install`.
// originalBytes is retained by callers that want to preserve comments
// on write (jsonc codec consults it).
func loadConfigFile(path string, f configFormat) (map[string]any, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil, nil
		}
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, raw, nil
	}
	tree, err := decodeFormat(raw, f)
	if err != nil {
		return nil, raw, fmt.Errorf("parse %s (%s): %w", path, f, err)
	}
	return tree, raw, nil
}

// decodeFormat turns raw bytes into a generic map tree.
func decodeFormat(raw []byte, f configFormat) (map[string]any, error) {
	switch f {
	case formatJSON:
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	case formatJSONC:
		// hujson.Standardize mutates the underlying byte buffer
		// in-place (overwrites comments with whitespace). Parse a
		// COPY so the caller's `raw` stays byte-for-byte intact —
		// the save path depends on it to preserve comments.
		cp := make([]byte, len(raw))
		copy(cp, raw)
		v, err := hujson.Parse(cp)
		if err != nil {
			return nil, err
		}
		v.Standardize()
		std := v.Pack()
		var out map[string]any
		if err := json.Unmarshal(std, &out); err != nil {
			return nil, err
		}
		return out, nil
	case formatYAML:
		var out map[string]any
		if err := yaml.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string]any{}
		}
		return normalizeYAMLMap(out), nil
	case formatTOML:
		var out map[string]any
		if _, err := toml.Decode(string(raw), &out); err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string]any{}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown format: %s", f)
}

// encodeFormat renders the tree back to bytes.
// originalBytes is used for jsonc → hujson.Patch so comments survive.
// A nil / empty `originalBytes` triggers a fresh marshal (file didn't
// exist before or had empty contents).
func encodeFormat(tree map[string]any, f configFormat, originalBytes []byte) ([]byte, error) {
	switch f {
	case formatJSON:
		return marshalJSONPretty(tree)
	case formatJSONC:
		return marshalJSONCPreserving(tree, originalBytes)
	case formatYAML:
		return marshalYAML(tree)
	case formatTOML:
		return marshalTOML(tree)
	}
	return nil, fmt.Errorf("unknown format: %s", f)
}

func marshalJSONPretty(tree map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// marshalJSONCPreserving re-emits the tree as JSONC while preserving
// as many user-authored comments + trailing commas as possible.
//
// Strategy: diff the parsed-form of originalBytes against tree, compute
// a minimal set of JSON Patch operations, apply them via hujson.Patch,
// then Pack. Comments on untouched keys and top-level comments survive
// unchanged because hujson stores extra/comment bytes on syntax nodes
// that patch ops don't traverse.
//
// If the diff can't be expressed as a clean patch set (malformed input,
// patch errors), we fall back to a fresh pretty-printed JSON — the
// file stays valid but comments are lost. Never produces bad output.
func marshalJSONCPreserving(tree map[string]any, originalBytes []byte) ([]byte, error) {
	if len(bytes.TrimSpace(originalBytes)) == 0 {
		return marshalJSONPretty(tree)
	}
	v, err := hujson.Parse(originalBytes)
	if err != nil {
		return marshalJSONPretty(tree)
	}

	// Standardize a clone so we can decode the "current" tree without
	// mutating v (which we'll patch in place).
	clone := v.Clone()
	clone.Standardize()
	var current map[string]any
	if err := json.Unmarshal(clone.Pack(), &current); err != nil {
		return marshalJSONPretty(tree)
	}

	// Build patch ops at the top level. For each key:
	//   present in both and value-equal  → skip (preserves comments)
	//   present in both and value-differ → replace (or add = "add replaces")
	//   present only in current          → remove
	//   present only in tree             → add
	//
	// Then recurse into nested objects where BOTH sides are objects —
	// that way mcpServers.pura gets a surgical patch and mcpServers
	// untouched siblings stay untouched.
	ops := diffJSONCOps("", current, tree)
	if len(ops) == 0 {
		// No differences at all — return the original bytes verbatim.
		return originalBytes, nil
	}
	patch, err := json.Marshal(ops)
	if err != nil {
		return marshalJSONPretty(tree)
	}
	if err := v.Patch(patch); err != nil {
		return marshalJSONPretty(tree)
	}
	return v.Pack(), nil
}

// diffJSONCOps produces a minimal RFC 6902 patch against `oldTree` to
// produce `newTree`. Recurses into nested objects so only the leaf
// differences are touched. Arrays are replaced wholesale (diffing them
// surgically gets ambiguous).
func diffJSONCOps(prefix string, oldTree, newTree map[string]any) []map[string]any {
	out := make([]map[string]any, 0)
	// Removals.
	for k := range oldTree {
		if _, still := newTree[k]; !still {
			out = append(out, map[string]any{
				"op":   "remove",
				"path": prefix + "/" + jsonPointerEscape(k),
			})
		}
	}
	// Additions + replacements.
	for k, newVal := range newTree {
		path := prefix + "/" + jsonPointerEscape(k)
		oldVal, existed := oldTree[k]
		if !existed {
			out = append(out, map[string]any{"op": "add", "path": path, "value": newVal})
			continue
		}
		// Recurse into nested objects.
		if om, ok := oldVal.(map[string]any); ok {
			if nm, ok := newVal.(map[string]any); ok {
				out = append(out, diffJSONCOps(path, om, nm)...)
				continue
			}
		}
		// Not both objects — byte-compare; emit a replace iff they differ.
		oj, _ := canonicalJSON(oldVal)
		nj, _ := canonicalJSON(newVal)
		if !bytes.Equal(oj, nj) {
			out = append(out, map[string]any{"op": "replace", "path": path, "value": newVal})
		}
	}
	return out
}

// jsonPointerEscape escapes "/" and "~" per RFC 6901 §3.
func jsonPointerEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func marshalYAML(tree map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(tree); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func marshalTOML(tree map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// normalizeYAMLMap converts yaml.v3's map[interface{}]interface{} children
// to map[string]any so downstream code (renderEntry / diff) can compare
// trees without type assertions. The top-level is already map[string]any
// thanks to our Unmarshal target.
func normalizeYAMLMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = normalizeYAMLValue(v)
	}
	return out
}

func normalizeYAMLValue(v any) any {
	switch x := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(x))
		for kk, vv := range x {
			m[fmt.Sprint(kk)] = normalizeYAMLValue(vv)
		}
		return m
	case map[string]any:
		return normalizeYAMLMap(x)
	case []any:
		for i := range x {
			x[i] = normalizeYAMLValue(x[i])
		}
		return x
	}
	return v
}
