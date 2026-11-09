package configcheck

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/project"
)

// CheckProcessorMultipleFiles — ключ multiple действует только у параметра
// обработки type: binary (пачка файлов → Массив Структур). У других типов он
// молча игнорировался бы, а автор ждал бы массив.
func CheckProcessorMultipleFiles(proj *project.Project) []Issue {
	var warns []Issue
	for _, proc := range proj.Processors {
		if proc == nil {
			continue
		}
		for _, p := range proc.Params {
			if p.Multiple && p.Type != "binary" {
				warns = append(warns, Issue{
					File:         "processors/" + proc.Name + ".yaml",
					Object:       proc.Name,
					Kind:         "Обработка",
					Code:         "processor.multiple-type",
					Message:      fmt.Sprintf("параметр %q (type: %s): ключ multiple игнорируется — несколько файлов принимает только type: binary", p.Name, p.Type),
					SuggestedFix: "Задайте type: binary или уберите multiple.",
				})
			}
		}
	}
	return warns
}
