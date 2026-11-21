package report

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// EnumLookup supplies trusted configuration metadata, never request options.
type EnumLookup interface {
	GetEnum(string) *metadata.Enum
}

// ValidateEnumParams keeps the same canonical enum domain on every HTTP entry
// point. Empty means no selection; translated labels and arbitrary text cannot
// become enum values. Existing non-enum parameter coercion is unchanged.
func ValidateEnumParams(params []Param, values map[string]any, lookup EnumLookup) error {
	for _, p := range params {
		name, ok := strings.CutPrefix(p.Type, "enum:")
		if !ok {
			continue
		}
		var enumeration *metadata.Enum
		if lookup != nil {
			enumeration = lookup.GetEnum(name)
		}
		if enumeration == nil {
			return fmt.Errorf("report parameter %s: unknown enumeration %s", p.Name, name)
		}
		value := values[p.Name]
		if value == nil {
			continue
		}
		text, stringValue := value.(string)
		if !stringValue || text != "" && !slices.Contains(enumeration.Values, text) {
			return fmt.Errorf("report parameter %s: expected a canonical enumeration value", p.Name)
		}
	}
	return nil
}
