package api

import (
	"net/url"
	"strings"
)

const AnonymousHandle = "_"

// NormalizeHandle accepts either "alice" or "@alice" and returns the canonical handle.
func NormalizeHandle(handle string) string {
	normalized := strings.TrimSpace(handle)
	normalized = strings.TrimPrefix(normalized, "@")
	normalized = strings.Trim(normalized, "/")
	if normalized == "" {
		return AnonymousHandle
	}
	return normalized
}

// NormalizeDocPath resolves a document reference into @handle/slug form.
func NormalizeDocPath(defaultHandle, slug string) string {
	cleaned := strings.TrimSpace(slug)
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "" {
		return "@" + NormalizeHandle(defaultHandle) + "/"
	}

	if strings.HasPrefix(cleaned, "@") {
		parts := strings.SplitN(strings.TrimPrefix(cleaned, "@"), "/", 2)
		if len(parts) == 2 {
			namespacedHandle := NormalizeHandle(parts[0])
			namespacedSlug := strings.Trim(parts[1], "/")
			if namespacedSlug != "" {
				return "@" + namespacedHandle + "/" + namespacedSlug
			}
		}
	}

	return "@" + NormalizeHandle(defaultHandle) + "/" + cleaned
}

// PublicURL builds a public document URL from the configured base URL.
func PublicURL(baseURL, handle, slug string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/api")
	return base + "/" + NormalizeDocPath(handle, slug)
}

// HandleFromURL extracts a handle from a public document URL.
func HandleFromURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", false
	}

	cleaned := strings.Trim(parsed.Path, "/")
	if !strings.HasPrefix(cleaned, "@") {
		return "", false
	}

	parts := strings.SplitN(strings.TrimPrefix(cleaned, "@"), "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return "", false
	}

	return NormalizeHandle(parts[0]), true
}
