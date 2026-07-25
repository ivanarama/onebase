package launcher

import "strings"

// environmentWithout removes every occurrence of key. Environment keys on
// Windows are case-insensitive, and duplicate KEY=value entries otherwise let
// a parent ONEBASE_WEBVIEW_PROFILE leak into a supposedly shared-profile child.
func environmentWithout(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, item := range env {
		if len(item) >= len(prefix) && strings.EqualFold(item[:len(prefix)], prefix) {
			continue
		}
		out = append(out, item)
	}
	return out
}
