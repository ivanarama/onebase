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

const IndexNormalizerPhoneLast10 = "phone_last10"

// StableName gives the normalized expression its own index identity. Version the
// key when changing the expression so migration builds a new index.
func (idx IndexSpec) StableName(table string, columns []string) string {
	if idx.Normalizer == IndexNormalizerPhoneLast10 {
		columns = append([]string{"phone_last10:v1"}, columns...)
	}
	return StableEntityIndexName(table, columns, idx.Unique)
}

func (idx IndexSpec) ValidateNormalizer(e *Entity) error {
	if idx.Normalizer == "" {
		return nil
	}
	if idx.Normalizer != IndexNormalizerPhoneLast10 {
		return fmt.Errorf("entity %s: unknown index normalizer %q", e.Name, idx.Normalizer)
	}
	if e.Kind == KindDocument && len(idx.Fields) == 1 && !idx.Unique {
		if field := findEntityField(e, idx.Fields[0]); field != nil && field.Type == FieldTypeString {
			return nil
		}
	}
	return fmt.Errorf("entity %s: phone_last10 requires a non-unique index on one string field of a document", e.Name)
}
