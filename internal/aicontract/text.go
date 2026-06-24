// Package aicontract builds machine-readable and compact prompt contracts for
// OneBase configurations.
package aicontract

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// NamedTitle is a compact name/title pair for non-data objects.
type NamedTitle struct{ Name, Title string }

// TextInput is a compact metadata slice for prompt context.
type TextInput struct {
	Entities         []*metadata.Entity
	Registers        []*metadata.Register
	InfoRegisters    []*metadata.InfoRegister
	AccountRegisters []*metadata.AccountRegister
	ChartsOfAccounts []*metadata.ChartOfAccounts
	Enums            []*metadata.Enum
	Constants        []*metadata.Constant
	Reports          []NamedTitle
	Processors       []NamedTitle
	Journals         []*metadata.Journal
	Subsystems       []*metadata.Subsystem
}

func fieldNames(fs []metadata.Field) string {
	names := make([]string, 0, len(fs))
	for _, f := range fs {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}

func nameTitle(name, title string) string {
	if title != "" && title != name {
		return name + " — " + title
	}
	return name
}

// SchemaText returns a compact human-readable configuration summary.
func SchemaText(in TextInput) string {
	var b strings.Builder
	var catalogs, documents []*metadata.Entity
	for _, e := range in.Entities {
		switch e.Kind {
		case metadata.KindCatalog:
			catalogs = append(catalogs, e)
		case metadata.KindDocument:
			documents = append(documents, e)
		}
	}
	writeEntity := func(e *metadata.Entity, markPosting bool) {
		head := "  " + e.Name
		if markPosting && e.Posting {
			head += " (проводится)"
		}
		fmt.Fprintf(&b, "%s: %s\n", head, fieldNames(e.Fields))
		for _, tp := range e.TableParts {
			fmt.Fprintf(&b, "    ТЧ %s: %s\n", tp.Name, fieldNames(tp.Fields))
		}
		if len(e.Forms) > 0 {
			parts := make([]string, 0, len(e.Forms))
			for _, f := range e.Forms {
				if f.Kind != "" {
					parts = append(parts, f.Name+" ("+f.Kind+")")
				} else {
					parts = append(parts, f.Name)
				}
			}
			fmt.Fprintf(&b, "    формы: %s\n", strings.Join(parts, ", "))
		}
	}
	if len(catalogs) > 0 {
		b.WriteString("Справочники:\n")
		for _, e := range catalogs {
			writeEntity(e, false)
		}
	}
	if len(documents) > 0 {
		b.WriteString("Документы:\n")
		for _, e := range documents {
			writeEntity(e, true)
		}
	}
	if len(in.Registers) > 0 {
		b.WriteString("Регистры накопления (доступны .Остатки/.Обороты):\n")
		for _, rg := range in.Registers {
			fmt.Fprintf(&b, "  %s: измерения [%s]; ресурсы [%s]\n", rg.Name, fieldNames(rg.Dimensions), fieldNames(rg.Resources))
		}
	}
	if len(in.InfoRegisters) > 0 {
		b.WriteString("Регистры сведений (доступен .СрезПоследних):\n")
		for _, ir := range in.InfoRegisters {
			fmt.Fprintf(&b, "  %s: измерения [%s]; ресурсы [%s]\n", ir.Name, fieldNames(ir.Dimensions), fieldNames(ir.Resources))
		}
	}
	if len(in.ChartsOfAccounts) > 0 {
		b.WriteString("Планы счетов:\n")
		for _, ch := range in.ChartsOfAccounts {
			codes := make([]string, 0, len(ch.Accounts))
			for _, a := range ch.Accounts {
				codes = append(codes, a.Code)
			}
			fmt.Fprintf(&b, "  %s: счета %s\n", nameTitle(ch.Name, ch.Title), strings.Join(codes, ", "))
		}
	}
	if len(in.AccountRegisters) > 0 {
		b.WriteString("Регистры бухгалтерии (доступны .Остатки/.Обороты по счетам и субконто):\n")
		for _, ar := range in.AccountRegisters {
			fmt.Fprintf(&b, "  %s: ресурсы [%s]; субконто [%s]; план счетов %s\n", nameTitle(ar.Name, ar.Title), fieldNames(ar.Resources), fieldNames(ar.Subconto), ar.Accounts)
		}
	}
	if len(in.Enums) > 0 {
		b.WriteString("Перечисления:\n")
		for _, en := range in.Enums {
			fmt.Fprintf(&b, "  %s: %s\n", en.Name, strings.Join(en.Values, ", "))
		}
	}
	if len(in.Constants) > 0 {
		names := make([]string, 0, len(in.Constants))
		for _, c := range in.Constants {
			names = append(names, c.Name)
		}
		fmt.Fprintf(&b, "Константы: %s\n", strings.Join(names, ", "))
	}
	if len(in.Reports) > 0 {
		b.WriteString("Отчёты (готовые, открываются в интерфейсе; не используются как таблицы в запросах):\n")
		for _, rp := range in.Reports {
			fmt.Fprintf(&b, "  %s\n", nameTitle(rp.Name, rp.Title))
		}
	}
	if len(in.Processors) > 0 {
		b.WriteString("Обработки (запускаются в интерфейсе):\n")
		for _, p := range in.Processors {
			fmt.Fprintf(&b, "  %s\n", nameTitle(p.Name, p.Title))
		}
	}
	if len(in.Journals) > 0 {
		b.WriteString("Журналы документов:\n")
		for _, j := range in.Journals {
			fmt.Fprintf(&b, "  %s: документы [%s]\n", nameTitle(j.Name, j.Title), strings.Join(j.Documents, ", "))
		}
	}
	if len(in.Subsystems) > 0 {
		b.WriteString("Подсистемы (разделы интерфейса):\n")
		for _, sub := range in.Subsystems {
			fmt.Fprintf(&b, "  %s\n", nameTitle(sub.Name, sub.Title))
		}
	}
	if b.Len() == 0 {
		return "В конфигурации нет объектов для запроса."
	}
	return b.String()
}
