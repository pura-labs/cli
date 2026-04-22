// Config file I/O for `pura mcp` — format-agnostic load + merge + atomic
// write with a timestamped .bak-<ts> backup on every rewrite.
//
// Atomicity: write to `<path>.tmp`, `rename` onto the real path. On POSIX
// this is atomic; on Windows `os.Rename` falls back to MoveFileEx which
// is effectively atomic for same-volume operations. We don't fsync(2)
// since the worst case (crash mid-rename) leaves the user with the old
// file or the new one — never a torn one — and we've got the backup.

package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// pathAt resolves the root-key path segments. Most clients use a single
// segment ("mcpServers"); nanobot-style dotted paths would split on ".".
// All nine clients currently use single segments.
func pathAt(rootKey string) []string {
	if rootKey == "" {
		return nil
	}
	return strings.Split(rootKey, ".")
}

// getServerDict descends `tree` along `path` and returns the dict at the
// leaf. Missing intermediate nodes return nil,false.
func getServerDict(tree map[string]any, path []string) (map[string]any, bool) {
	var cur any = tree
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	m, ok := cur.(map[string]any)
	return m, ok
}

// setServerDict writes `dict` at `path` inside `tree`, creating nested
// maps as needed.
func setServerDict(tree map[string]any, path []string, dict map[string]any) {
	if len(path) == 0 {
		return
	}
	cur := tree
	for i, seg := range path {
		if i == len(path)-1 {
			cur[seg] = dict
			return
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
}

// removeServerDictKey deletes `name` from the dict at `path`. Returns
// true when a deletion occurred.
func removeServerDictKey(tree map[string]any, path []string, name string) bool {
	dict, ok := getServerDict(tree, path)
	if !ok {
		return false
	}
	if _, had := dict[name]; !had {
		return false
	}
	delete(dict, name)
	setServerDict(tree, path, dict)
	return true
}

// writeConfigAtomic writes `bytes` to `path` via .tmp + rename. The
// parent dir is created (0o755) if missing. When `path` already exists,
// a timestamped backup is created beforehand — the caller supplies `ts`
// so parallel install flows don't step on each other's backup names.
func writeConfigAtomic(path string, data []byte) (backup string, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	// Backup if file exists.
	if _, statErr := os.Stat(path); statErr == nil {
		ts := time.Now().UTC().Format("20060102-150405")
		backup = path + ".bak-" + ts
		if err := copyFileAtomic(path, backup); err != nil {
			return "", fmt.Errorf("backup %s: %w", path, err)
		}
	}
	// Ensure trailing newline for POSIX-friendliness (git diffs, etc.).
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return backup, fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return backup, fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return backup, nil
}

func copyFileAtomic(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// restoreBackup rolls the real file back to the backup produced by
// writeConfigAtomic. Used by install / rotate failure paths.
func restoreBackup(path, backup string) error {
	if backup == "" {
		// No backup means the file didn't exist before — undo by removing
		// the newly-written file entirely.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return copyFileAtomic(backup, path)
}

// canonicalJSON returns a byte-stable JSON encoding so the install-
// idempotency check (§§3.7) can compare two entries reliably.
// Implementation: JSON-marshal → round-trip through json.Decoder with
// sorted keys. Recursive map key sort via a tiny helper.
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return marshalSorted(parsed)
}

func marshalSorted(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeSorted(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeSorted(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeSorted(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeSorted(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	}
}

// entriesEqual reports whether two serialized server entries match
// under canonical JSON *after redacting secrets*. Used for install
// idempotency (short-circuit when the incoming entry matches what's on
// disk in shape) and for diffing in `--print` mode.
//
// Redacted: the __puraKeyId marker (opaque and rotates) plus the actual
// bearer token — both the URL-transport `headers.Authorization` value
// and the stdio-transport `env.PURA_API_KEY` value. The "shape" that
// matters for idempotency is {transport, url / command, host, scopes},
// not the specific token bytes. Without this, every re-run of install
// would look like a change and churn the key on disk.
func entriesEqual(a, b map[string]any) bool {
	aj, err1 := canonicalJSON(redactEntry(a))
	bj, err2 := canonicalJSON(redactEntry(b))
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

// redactEntry returns a deep-ish copy of `entry` with identity-only
// fields cleared: __puraKeyId, the Authorization bearer value, and the
// PURA_API_KEY env value. Everything else (including key presence in
// headers/env and non-secret values like X-Pura-Agent) is retained so
// shape diffs still register.
func redactEntry(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		if k == puraKeyIDField {
			continue
		}
		switch k {
		case "headers":
			if m, ok := v.(map[string]any); ok {
				hh := make(map[string]any, len(m))
				for hk, hv := range m {
					if hk == "Authorization" {
						hh[hk] = "<redacted>"
						continue
					}
					hh[hk] = hv
				}
				out[k] = hh
				continue
			}
		case "env":
			if m, ok := v.(map[string]any); ok {
				ee := make(map[string]any, len(m))
				for ek, ev := range m {
					if ek == "PURA_API_KEY" {
						ee[ek] = "<redacted>"
						continue
					}
					ee[ek] = ev
				}
				out[k] = ee
				continue
			}
		}
		out[k] = v
	}
	return out
}
