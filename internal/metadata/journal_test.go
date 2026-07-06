package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadJournalFile(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: ВсеОперации
title: Все операции
documents:
  - ПоступлениеТоваров
  - РеализацияТоваров
columns:
  - field: Дата
    label: Дата
    format: date
  - field: Контрагент
    fallback: [Поставщик, Покупатель]
filters:
  - field: Дата
    label: Период
    type: date_range
  - field: Контрагент
    labels:
      en: Counterparty
    type: reference:Контрагент
`
	path := filepath.Join(dir, "всеоперации.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	j, err := LoadJournalFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if j.Name != "ВсеОперации" {
		t.Errorf("name: got %q", j.Name)
	}
	if j.Title != "Все операции" {
		t.Errorf("title: got %q", j.Title)
	}
	if len(j.Documents) != 2 {
		t.Errorf("documents: got %d", len(j.Documents))
	}
	if len(j.Columns) != 2 {
		t.Fatalf("columns: got %d", len(j.Columns))
	}
	if j.Columns[0].Format != "date" {
		t.Errorf("column[0].format: got %q", j.Columns[0].Format)
	}
	if len(j.Columns[1].Fallback) != 2 {
		t.Errorf("column[1].fallback: got %v", j.Columns[1].Fallback)
	}
	if len(j.Filters) != 2 {
		t.Errorf("filters: got %d", len(j.Filters))
	}
	if j.Filters[1].Type != "reference:Контрагент" {
		t.Errorf("filter[1].type: got %q", j.Filters[1].Type)
	}
	if got := j.Filters[0].DisplayLabel("ru"); got != "Период" {
		t.Errorf("filter[0].label: got %q", got)
	}
	if got := j.Filters[1].DisplayLabel("en"); got != "Counterparty" {
		t.Errorf("filter[1].label en: got %q", got)
	}
}

func TestLoadJournalDir_Empty(t *testing.T) {
	journals, err := LoadJournalDir("/nonexistent/path")
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if journals != nil {
		t.Errorf("expected nil, got %v", journals)
	}
}

func TestLoadJournalFile_DefaultLabel(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: Тест
documents: [Д1]
columns:
  - field: Дата
`
	path := filepath.Join(dir, "тест.yaml")
	os.WriteFile(path, []byte(yaml), 0644)
	j, err := LoadJournalFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if j.Columns[0].Label != "Дата" {
		t.Errorf("default label: got %q", j.Columns[0].Label)
	}
}

func TestLoadJournalFile_Conditional(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: Тест
documents: [Д1]
columns:
  - field: Сумма
conditional:
  - when: Сумма < 0
    style:
      color: "#c00"
      bold: true
conditional_formatting:
  - when: Документ = "Д1"
    field: Сумма
    then:
      background: yellow
      italic: true
`
	path := filepath.Join(dir, "тест.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	j, err := LoadJournalFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Conditional) != 2 {
		t.Fatalf("conditional: got %d", len(j.Conditional))
	}
	if j.Conditional[0].Style.Color != "#c00" || !j.Conditional[0].Style.Bold {
		t.Fatalf("style not loaded: %+v", j.Conditional[0])
	}
	if j.Conditional[1].Field != "Сумма" || j.Conditional[1].Style.Background != "yellow" || !j.Conditional[1].Style.Italic {
		t.Fatalf("then/style alias not loaded: %+v", j.Conditional[1])
	}
}
