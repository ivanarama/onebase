package metadata

import (
	"crypto/sha1" //nolint:gosec // stable physical names, not a security primitive
	"fmt"
	"strings"
)

// StableEntityIndexName preserves the historical physical naming algorithm.
// Catalog ownership must never be inferred from this name.
func StableEntityIndexName(table string, columns []string, unique bool) string {
	kind := "n"
	if unique {
		kind = "u"
	}
	sum := sha1.Sum([]byte(kind + "|" + table + "|" + strings.Join(columns, "|"))) //nolint:gosec // existing name algorithm; changing it would rename every index
	return "idx_ob_" + fmt.Sprintf("%x", sum[:6])
}
