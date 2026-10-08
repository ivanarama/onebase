package metadata

import "strings"

// NormalizeIconName canonicalizes Lucide names for layout metadata and UI.
func NormalizeIconName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		if r == ' ' || r == '_' || r == '-' {
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		b.WriteRune(r)
		prevDash = false
	}
	return strings.TrimRight(b.String(), "-")
}
