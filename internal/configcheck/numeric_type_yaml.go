package configcheck

import (
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/configfmt"
	"github.com/ivantit66/onebase/internal/configyaml"
	"gopkg.in/yaml.v3"
)

// CheckNumericTypeYAML rejects the same truncated declarations as fmt before
// metadata loading can discard the stray flow-mapping keys. Other unknown
// types remain advisory field.type warnings.
func CheckNumericTypeYAML(dir string) []Issue {
	var issues []Issue
	for _, subdir := range []string{"catalogs", "documents", "registers", "inforegs", "constants", "accounts", "accountregs", "forms", "processors", "reports"} {
		files, err := configfmt.CollectYAMLFiles([]string{filepath.Join(dir, subdir)})
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			issues = append(issues, Issue{File: subdir, Message: err.Error()})
			continue
		}
		for _, path := range files {
			label, _ := filepath.Rel(dir, path)
			data, err := os.ReadFile(path)
			if err != nil {
				issues = append(issues, Issue{File: filepath.ToSlash(label), Message: err.Error()})
				continue
			}
			var node yaml.Node
			if yaml.Unmarshal(data, &node) != nil {
				continue // syntax errors belong to the existing YAML loaders
			}
			for _, problem := range configyaml.TruncatedNumericTypes(&node) {
				issues = append(issues, Issue{
					File: filepath.ToSlash(label), Kind: "Тип реквизита", Code: "field.type-truncated",
					Message: problem.Error(), SuggestedFix: configyaml.NumericTypeFix,
					Line: problem.Line, Column: problem.Column,
				})
			}
		}
	}
	return issues
}
