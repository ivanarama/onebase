// Package configyaml contains source checks shared by configuration tools.
package configyaml

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// NumericTypeError identifies a type whose closing parenthesis was lost, often
// because a comma in an unquoted flow scalar was parsed as a mapping separator.
type NumericTypeError struct {
	Type         string
	Line, Column int
}

const NumericTypeFix = `Внутри {...} заключите тип в кавычки: type: 'number(15,4)' или type: 'decimal(15,4)'; либо используйте блочную запись. Восстановите полный тип из исходника: оборванную запись автоматически исправить нельзя.`

func (e NumericTypeError) Error() string {
	return fmt.Sprintf("оборванная числовая спецификация type: %q (нет закрывающей скобки). %s", e.Type, NumericTypeFix)
}

// TruncatedNumericTypes checks only incomplete number/decimal specifications.
// Unknown types and complete numeric specifications retain their old behavior.
// Work on the parsed tree: text in comments, titles and DSL is not a type.
func TruncatedNumericTypes(node *yaml.Node) []NumericTypeError {
	var issues []NumericTypeError
	var visit func(*yaml.Node)
	visit = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, value := n.Content[i], n.Content[i+1]
				scalar := value
				if scalar.Kind == yaml.AliasNode {
					scalar = scalar.Alias
				}
				if key.Value == "type" && scalar != nil && scalar.Kind == yaml.ScalarNode {
					typ := strings.TrimSpace(scalar.Value)
					if open := strings.IndexByte(typ, '('); open > 0 {
						base := strings.TrimSpace(typ[:open])
						if (base == "number" || base == "decimal") && !strings.Contains(typ[open:], ")") {
							issues = append(issues, NumericTypeError{Type: scalar.Value, Line: value.Line, Column: value.Column})
						}
					}
				}
			}
		}
		// Alias targets are already in the document. Do not follow recursive
		// aliases while walking; a type scalar alias is checked above.
		for _, child := range n.Content {
			visit(child)
		}
	}
	visit(node)
	return issues
}
