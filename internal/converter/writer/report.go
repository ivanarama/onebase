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
}

// String форматирует итоговый отчёт.
func (r *ConversionReport) String() string {
	var sb strings.Builder

	sb.WriteString("Конвертация завершена\n")
	sb.WriteString("════════════════════════════\n")
	sb.WriteString(fmt.Sprintf("Справочников:          %d → %d YAML\n", r.Catalogs, r.Catalogs))
	sb.WriteString(fmt.Sprintf("Документов:            %d → %d YAML\n", r.Documents, r.Documents))
	sb.WriteString(fmt.Sprintf("Регистров накопления:  %d → %d YAML\n", r.Registers, r.Registers))
	sb.WriteString(fmt.Sprintf("Перечислений:          %d → %d YAML\n", r.Enums, r.Enums))
	sb.WriteString(fmt.Sprintf("Констант:              %d → %d YAML\n", r.Constants, r.Constants))
	sb.WriteString(fmt.Sprintf("Регистров сведений:    %d → %d YAML\n", r.InfoRegisters, r.InfoRegisters))
	sb.WriteString(fmt.Sprintf("Регистров бухгалтерии: %d → %d YAML\n", r.AccountRegisters, r.AccountRegisters))
	sb.WriteString(fmt.Sprintf("Планов счетов:         %d → %d YAML\n", r.ChartsOfAccounts, r.ChartsOfAccounts))
	sb.WriteString(fmt.Sprintf("Регл. заданий:         %d → %d YAML\n", r.ScheduledJobs, r.ScheduledJobs))
	sb.WriteString(fmt.Sprintf("Общих модулей:         %d → %d .os\n", r.Modules, r.Modules))
	sb.WriteString(fmt.Sprintf("Обработок:             %d → %d YAML + .os\n", r.Processors, r.Processors))
	sb.WriteString(fmt.Sprintf("Форм:                  %d → %d .form.yaml\n", r.Forms, r.Forms))
	sb.WriteString(fmt.Sprintf("Шаблонов (макетов):    %d → %d printform\n", r.Templates, r.Templates))
	sb.WriteString(fmt.Sprintf("DSL-заглушки:          %d .os файлов\n", len(r.DSLStubs)))

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

	return sb.String()
}
