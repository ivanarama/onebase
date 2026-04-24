package writer

import (
	"fmt"
	"strings"
)

// ConversionReport собирает статистику конвертации.
type ConversionReport struct {
	Catalogs     int
	Documents    int
	Registers    int
	DSLStubs     []string
	Skipped      []string
	TypeWarnings []string
}

// String форматирует итоговый отчёт.
func (r *ConversionReport) String() string {
	var sb strings.Builder

	sb.WriteString("Конвертация завершена\n")
	sb.WriteString("════════════════════════════\n")
	sb.WriteString(fmt.Sprintf("Справочников:  %d → %d YAML\n", r.Catalogs, r.Catalogs))
	sb.WriteString(fmt.Sprintf("Документов:    %d → %d YAML\n", r.Documents, r.Documents))
	sb.WriteString(fmt.Sprintf("Регистров:     %d → %d YAML\n", r.Registers, r.Registers))
	sb.WriteString(fmt.Sprintf("DSL-заглушки:  %d .os файлов\n", len(r.DSLStubs)))

	if len(r.Skipped) > 0 {
		sb.WriteString("\nПропущено (не поддерживается):\n")
		for _, s := range r.Skipped {
			sb.WriteString("  - " + s + "\n")
		}
	}

	if len(r.TypeWarnings) > 0 {
		sb.WriteString("\nПредупреждения о типах:\n")
		for _, w := range r.TypeWarnings {
			sb.WriteString("  ⚠  " + w + "\n")
		}
	}

	if len(r.DSLStubs) > 0 {
		sb.WriteString("\nTODO: перенесите бизнес-логику из 1С вручную:\n")
		for _, name := range r.DSLStubs {
			sb.WriteString("  src/" + name + "\n")
		}
	}

	return sb.String()
}
