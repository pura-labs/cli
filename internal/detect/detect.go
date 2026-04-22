package detect

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Type detects content type from filename and/or content.
func Type(filename, content string) string {
	// Try extension first
	if filename != "" {
		if t := fromExtension(filename); t != "" {
			return t
		}
	}
	// Then content heuristics
	return fromContent(content)
}

func fromExtension(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".md", ".markdown", ".txt":
		return "markdown"
	case ".csv":
		return "csv"
	case ".json":
		return "json"
	case ".html", ".htm":
		return "html"
	case ".svg":
		return "svg"
	case ".js":
		return "canvas"
	default:
		return ""
	}
}

func fromContent(content string) string {
	trimmed := strings.TrimSpace(content)

	// JSON
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		if json.Valid([]byte(content)) {
			return "json"
		}
	}

	// SVG
	if strings.HasPrefix(trimmed, "<svg") || strings.HasPrefix(trimmed, "<?xml") {
		if strings.Contains(trimmed, "<svg") {
			return "svg"
		}
	}

	// HTML
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return "html"
	}

	// CSV: consistent delimiter counts across first 2 lines
	lines := strings.SplitN(trimmed, "\n", 3)
	if len(lines) >= 2 {
		commas1 := strings.Count(lines[0], ",")
		commas2 := strings.Count(lines[1], ",")
		if commas1 > 0 && commas1 == commas2 {
			return "csv"
		}
		tabs1 := strings.Count(lines[0], "\t")
		tabs2 := strings.Count(lines[1], "\t")
		if tabs1 > 0 && tabs1 == tabs2 {
			return "csv"
		}
	}

	return "markdown"
}
