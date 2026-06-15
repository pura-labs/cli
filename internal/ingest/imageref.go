// Package ingest scans document content for image references and rewrites
// their URLs in place. It is pure (no filesystem or network I/O) so the
// scanning/classification/rewrite logic stays trivially unit-testable; the
// commands layer does the file reads + uploads and hands back a replacement map.
//
// Embed-first (CLI side): local image paths in a doc being pushed are uploaded
// to the user's image host and rewritten to the host URL BEFORE the doc is
// created, so the published doc is self-contained. External http(s) URLs are
// left untouched — the server rehosts those on publish.
package ingest

import (
	"regexp"
	"sort"
	"strings"
)

// RefKind classifies an image reference URL.
type RefKind int

const (
	// LocalRelative is a path relative to the document's directory (./a.png, a.png, ../a.png).
	LocalRelative RefKind = iota
	// LocalAbsolute is a filesystem-absolute path (/Users/…/a.png). May also be a
	// site-root web path; the caller only acts on it when it resolves to a real file.
	LocalAbsolute
	// HTTP is an absolute or protocol-relative web URL — left untouched (server rehosts).
	HTTP
	// Data is a data: URI — already inline, left untouched.
	Data
)

// Syntax records how the reference appeared.
type Syntax int

const (
	MarkdownInline Syntax = iota
	HTMLImg
)

// Ref is one image reference with the byte offsets of its URL token within the
// scanned content (so RewriteRefs can splice precisely).
type Ref struct {
	URL    string
	Start  int
	End    int
	Kind   RefKind
	Syntax Syntax
}

var (
	// Markdown inline image: ![alt](URL) or ![alt](URL "title"). Captures the
	// URL token only (stops at whitespace or ')'), leaving any title intact.
	mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\(\s*([^)\s]+)`)
	// HTML <img src="URL"> / <img src='URL'>.
	htmlImgRe = regexp.MustCompile(`(?i)<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']`)
)

// Classify buckets a URL/path token. Pure string inspection — no disk access.
func Classify(u string) RefKind {
	switch {
	case strings.HasPrefix(u, "data:"):
		return Data
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "//"):
		return HTTP
	case strings.HasPrefix(u, "/"):
		return LocalAbsolute
	default:
		return LocalRelative
	}
}

// ScanImageRefs finds every markdown-inline and HTML <img> image reference,
// classified, with the URL token's byte offsets.
//
// Reference-style markdown images (![a][id] + [id]: url) are intentionally NOT
// rewritten in v1 — distinguishing image link-defs from plain link-defs is
// ambiguous; documented limitation.
func ScanImageRefs(content string) []Ref {
	var refs []Ref
	ignored := markdownCodeMask(content)
	add := func(matches [][]int, syntax Syntax) {
		for _, m := range matches {
			s, e := m[2], m[3]
			if ignored[s] {
				continue
			}
			u := content[s:e]
			refs = append(refs, Ref{URL: u, Start: s, End: e, Kind: Classify(u), Syntax: syntax})
		}
	}
	add(mdImageRe.FindAllStringSubmatchIndex(content, -1), MarkdownInline)
	add(htmlImgRe.FindAllStringSubmatchIndex(content, -1), HTMLImg)
	return refs
}

func markdownCodeMask(content string) []bool {
	mask := make([]bool, len(content))
	mark := func(start, end int) {
		if start < 0 {
			start = 0
		}
		if end > len(mask) {
			end = len(mask)
		}
		for i := start; i < end; i++ {
			mask[i] = true
		}
	}

	inFence := false
	fenceStart := 0
	fenceChar := byte(0)
	fenceLen := 0
	for lineStart := 0; lineStart < len(content); {
		next := strings.IndexByte(content[lineStart:], '\n')
		lineEnd := len(content)
		if next >= 0 {
			lineEnd = lineStart + next + 1
		}
		line := strings.TrimSuffix(content[lineStart:lineEnd], "\n")
		if !inFence {
			if ch, n, ok := fenceMarker(line); ok {
				inFence = true
				fenceStart = lineStart
				fenceChar = ch
				fenceLen = n
			}
		} else if ch, n, ok := fenceMarker(line); ok && ch == fenceChar && n >= fenceLen {
			mark(fenceStart, lineEnd)
			inFence = false
		}
		lineStart = lineEnd
	}
	if inFence {
		mark(fenceStart, len(content))
	}

	for i := 0; i < len(content); {
		if mask[i] || content[i] != '`' {
			i++
			continue
		}
		n := countRun(content, i, '`')
		if end := findBacktickRun(content, i+n, n, mask); end >= 0 {
			mark(i, end+n)
			i = end + n
			continue
		}
		i += n
	}
	return mask
}

func fenceMarker(line string) (byte, int, bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent > 3 || indent >= len(line) {
		return 0, 0, false
	}
	ch := line[indent]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	n := countRun(line, indent, ch)
	return ch, n, n >= 3
}

func countRun(s string, start int, ch byte) int {
	n := 0
	for start+n < len(s) && s[start+n] == ch {
		n++
	}
	return n
}

func findBacktickRun(content string, start, want int, mask []bool) int {
	for i := start; i < len(content); i++ {
		if mask[i] || content[i] != '`' {
			continue
		}
		if countRun(content, i, '`') == want {
			return i
		}
	}
	return -1
}

// RewriteRefs replaces each ref's URL token with replace[ref.URL] (when present),
// splicing by descending offset so earlier offsets stay valid. Offsets refer to
// the original content. A ref whose URL isn't in the map is left untouched.
func RewriteRefs(content string, refs []Ref, replace map[string]string) string {
	if len(replace) == 0 {
		return content
	}
	sorted := append([]Ref(nil), refs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start > sorted[j].Start })
	out := content
	for _, r := range sorted {
		nu, ok := replace[r.URL]
		if !ok {
			continue
		}
		out = out[:r.Start] + nu + out[r.End:]
	}
	return out
}
