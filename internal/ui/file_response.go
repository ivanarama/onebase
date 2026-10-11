package ui

import (
	"net/http"
	"strings"
)

// normalizedFileMIME is used for policy checks; response headers retain MIME
// parameters such as a text file's charset.
func normalizedFileMIME(mimeType string) string {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	return m
}

// inlineSafeType preserves the published-file policy. HTML and SVG served
// from the platform's origin must be downloads with a neutral content type.
func inlineSafeType(mimeType string) bool {
	m := normalizedFileMIME(mimeType)
	switch {
	case m == "image/svg+xml":
		return false
	case strings.HasPrefix(m, "image/"), strings.HasPrefix(m, "video/"), strings.HasPrefix(m, "audio/"):
		return true
	case m == "application/pdf", m == "text/plain":
		return true
	}
	return false
}

// attachmentInlineType narrows inline viewing of private stored attachments
// to the six types agreed in slice E of plan 178. Media wildcards do not apply.
func attachmentInlineType(mimeType string) bool {
	switch normalizedFileMIME(mimeType) {
	case "application/pdf", "text/plain", "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

func setFileSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
}

// allowInline may narrow the public policy, but cannot bypass inlineSafeType.
func setFileContentHeaders(h http.Header, mimeType, name string, allowInline bool) {
	if allowInline && inlineSafeType(mimeType) {
		h.Set("Content-Type", mimeType)
		// RFC 5987 preserves UTF-8 filenames (issue #46).
		h.Set("Content-Disposition", dispositionHeader("inline", name))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition(name))
	}
}
