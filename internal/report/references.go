package report

import "strings"

// MeasureReference recognizes scalar context bindings in measure expressions.
// Existing measure names take priority; callers check those before using this.
func MeasureReference(name string) (scope, field string, ok bool) {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"grand_", "previous_", "first_"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSuffix(prefix, "_"), name[len(prefix):], true
		}
	}
	return "", "", false
}
