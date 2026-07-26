package writer

import (
	"fmt"
	"strings"
)

// ConversionReport собирает статистику конвертации.
type ConversionReport struct {
	Catalogs         int
	Documents        int
	Registers        int
	Enums            int
	Constants        int
	InfoRegisters    int
	AccountRegisters int
	ChartsOfAccounts int
	ScheduledJobs    int
	Modules          int
	Processors       int
	Forms            int
	Templates        int
	DSLStubs         []string
	Skipped          []string
	TypeWarnings     []string
	FormWarnings     []string
	ProcessorLayouts []string // src/*.proc.layout.yaml — заготовки макетов обработок
}

// String форматирует итоговый отчёт.
func (r *ConversionReport) String() string {
	var sb strings.Builder

	sb.WriteString("Конвертация завершена\n")
	sb.WriteString("════════════════════════════\n")
	fmt.Fprintf(&sb, "Справочников:          %d → %d YAML\n", r.Catalogs, r.Catalogs)
	fmt.Fprintf(&sb, "Документов:            %d → %d YAML\n", r.Documents, r.Documents)
	fmt.Fprintf(&sb, "Регистров накопления:  %d → %d YAML\n", r.Registers, r.Registers)
	fmt.Fprintf(&sb, "Перечислений:          %d → %d YAML\n", r.Enums, r.Enums)
	fmt.Fprintf(&sb, "Констант:              %d → %d YAML\n", r.Constants, r.Constants)
	fmt.Fprintf(&sb, "Регистров сведений:    %d → %d YAML\n", r.InfoRegisters, r.InfoRegisters)
	fmt.Fprintf(&sb, "Регистров бухгалтерии: %d → %d YAML\n", r.AccountRegisters, r.AccountRegisters)
	fmt.Fprintf(&sb, "Планов счетов:         %d → %d YAML\n", r.ChartsOfAccounts, r.ChartsOfAccounts)
	fmt.Fprintf(&sb, "Регл. заданий:         %d → %d YAML\n", r.ScheduledJobs, r.ScheduledJobs)
	fmt.Fprintf(&sb, "Общих модулей:         %d → %d .os\n", r.Modules, r.Modules)
	fmt.Fprintf(&sb, "Обработок:             %d → %d YAML + .os\n", r.Processors, r.Processors)
	fmt.Fprintf(&sb, "Форм:                  %d → %d .form.yaml\n", r.Forms, r.Forms)
	fmt.Fprintf(&sb, "Шаблонов (макетов):    %d → %d printform\n", r.Templates, r.Templates)
	fmt.Fprintf(&sb, "DSL-заглушки:          %d .os файлов\n", len(r.DSLStubs))

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

	if len(r.FormWarnings) > 0 {
		sb.WriteString("\nЗамечания по формам:\n")
		for _, w := range r.FormWarnings {
			sb.WriteString("  ⚠  " + w + "\n")
		}
	}

	if len(r.DSLStubs) > 0 {
		sb.WriteString("\nTODO: перенесите бизнес-логику из 1С вручную:\n")
		for _, name := range r.DSLStubs {
			sb.WriteString("  src/" + name + "\n")
		}
	}

	if len(r.ProcessorLayouts) > 0 {
		sb.WriteString("\nМакеты обработок → заготовки макетов (перенесите оформление вручную):\n")
		for _, name := range r.ProcessorLayouts {
			sb.WriteString("  src/" + name + "\n")
		}
	}

	return sb.String()
}
