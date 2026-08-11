package metadata

import (
	"fmt"
	"strings"
)

// FormTableSource distinguishes persistent entity/processor table parts from
// ValueTable attributes which live only in a managed form.
type FormTableSource string

const (
	FormTableSourceTablePart  FormTableSource = "table_part"
	FormTableSourceValueTable FormTableSource = "value_table"
)

// FormTableDefinition is the canonical, browser-addressable metadata shared
// by managed-form rendering and event validation. Name and Columns preserve
// the spelling declared in metadata; all lookups are case-insensitive.
type FormTableDefinition struct {
	Name    string
	Columns []string
	Source  FormTableSource
}

// FormTableDefinitions combines entity/processor table parts with form-local
// ValueTable attributes. They share one runtime row namespace, therefore any
// case-insensitive duplicate is ambiguous and must be rejected before rows are
// parsed or handlers run. Column names are subject to the same rule so a posted
// column can always be mapped to exactly one canonical name.
func FormTableDefinitions(form *FormModule, tableParts []TablePart) ([]FormTableDefinition, error) {
	definitions := make([]FormTableDefinition, 0, len(tableParts))
	add := func(def FormTableDefinition) error {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			return fmt.Errorf("имя таблицы формы пустое")
		}
		// strings.ToLower недостаточно для case-insensitive namespace: EqualFold
		// также считает равными Unicode simple-fold пары S/ſ и Σ/ς. Все runtime-
		// lookups используют EqualFold, поэтому и неоднозначность обязана
		// проверяться тем же отношением эквивалентности.
		for _, previous := range definitions {
			if strings.EqualFold(previous.Name, name) {
				return fmt.Errorf("коллизия имён таблиц формы %q (%s) и %q (%s)",
					previous.Name, previous.Source, def.Name, def.Source)
			}
		}

		columnNames := make([]string, 0, len(def.Columns))
		for _, rawColumn := range def.Columns {
			column := strings.TrimSpace(rawColumn)
			if column == "" {
				return fmt.Errorf("таблица формы %q содержит колонку с пустым именем", name)
			}
			for _, previous := range columnNames {
				if strings.EqualFold(previous, column) {
					return fmt.Errorf("таблица формы %q содержит неоднозначные колонки %q и %q", name, previous, rawColumn)
				}
			}
			columnNames = append(columnNames, column)
		}

		def.Name = name
		definitions = append(definitions, def)
		return nil
	}

	for _, tablePart := range tableParts {
		columns := make([]string, 0, len(tablePart.Fields))
		for _, field := range tablePart.Fields {
			columns = append(columns, field.Name)
		}
		if err := add(FormTableDefinition{
			Name: tablePart.Name, Columns: columns, Source: FormTableSourceTablePart,
		}); err != nil {
			return nil, err
		}
	}
	if form != nil {
		for attributeIndex, attribute := range form.Attributes {
			if attribute == nil {
				return nil, fmt.Errorf("форма содержит пустой реквизит с индексом %d", attributeIndex)
			}
			if !strings.EqualFold(strings.TrimSpace(attribute.TypeRef), "ValueTable") {
				continue
			}
			columns := make([]string, 0, len(attribute.Columns))
			for columnIndex, column := range attribute.Columns {
				if column == nil {
					return nil, fmt.Errorf("таблица формы %q содержит пустую колонку с индексом %d", attribute.Name, columnIndex)
				}
				columns = append(columns, column.Name)
			}
			if err := add(FormTableDefinition{
				Name: attribute.Name, Columns: columns, Source: FormTableSourceValueTable,
			}); err != nil {
				return nil, err
			}
		}
	}
	return definitions, nil
}
