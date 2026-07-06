package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEntity_DisplayName(t *testing.T) {
	tests := []struct {
		name   string
		entity Entity
		lang   string
		want   string
	}{
		{
			name:   "empty entity falls back to Name",
			entity: Entity{Name: "Контрагенты"},
			lang:   "en",
			want:   "Контрагенты",
		},
		{
			name:   "Title used when Titles is empty",
			entity: Entity{Name: "Контрагенты", Title: "Контрагенты компании"},
			lang:   "en",
			want:   "Контрагенты компании",
		},
		{
			name: "Titles[lang] wins over Title",
			entity: Entity{
				Name:   "Контрагенты",
				Title:  "Контрагенты компании",
				Titles: map[string]string{"en": "Counterparties", "sr": "Партнери"},
			},
			lang: "en",
			want: "Counterparties",
		},
		{
			name: "missing lang in Titles falls back to Title",
			entity: Entity{
				Name:   "Контрагенты",
				Title:  "Контрагенты компании",
				Titles: map[string]string{"en": "Counterparties"},
			},
			lang: "de",
			want: "Контрагенты компании",
		},
		{
			name: "empty translation in Titles is ignored (falls back)",
			entity: Entity{
				Name:   "Контрагенты",
				Title:  "Контрагенты компании",
				Titles: map[string]string{"en": ""},
			},
			lang: "en",
			want: "Контрагенты компании",
		},
		{
			name: "empty lang skips Titles lookup",
			entity: Entity{
				Name:   "Контрагенты",
				Title:  "Контрагенты компании",
				Titles: map[string]string{"en": "Counterparties"},
			},
			lang: "",
			want: "Контрагенты компании",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.entity.DisplayName(tt.lang)
			if got != tt.want {
				t.Errorf("DisplayName(%q) = %q, want %q", tt.lang, got, tt.want)
			}
		})
	}
}

func TestLoadFile_Titles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.yaml")
	yaml := `name: Контрагенты
title: Контрагенты компании
titles:
  en: Counterparties
  sr: Партнери
fields:
  - name: ИНН
    type: string
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "Контрагенты компании" {
		t.Errorf("Title = %q, want %q", e.Title, "Контрагенты компании")
	}
	if got := e.Titles["en"]; got != "Counterparties" {
		t.Errorf("Titles[en] = %q, want %q", got, "Counterparties")
	}
	if got := e.Titles["sr"]; got != "Партнери" {
		t.Errorf("Titles[sr] = %q, want %q", got, "Партнери")
	}
	if got := e.DisplayName("en"); got != "Counterparties" {
		t.Errorf("DisplayName(en) = %q, want %q", got, "Counterparties")
	}
}

func TestLoadFile_FieldLabelAliasesTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.yaml")
	yaml := `name: Оператор
fields:
  - name: Логин
    type: string
    label: Логин пользователя onebase
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Fields) != 1 {
		t.Fatalf("fields len = %d, want 1", len(e.Fields))
	}
	if e.Fields[0].Title != "Логин пользователя onebase" {
		t.Errorf("Field.Title = %q, want label", e.Fields[0].Title)
	}
}

func TestLoadFile_Description(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.yaml")
	yaml := `name: Очередь
description: Очередь колл-центра
fields:
  - name: Наименование
    type: string
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if e.Description != "Очередь колл-центра" {
		t.Errorf("Description = %q, want YAML description", e.Description)
	}
}

func TestLoadFile_BasedOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.yaml")
	yaml := `name: ВозвратОтПокупателя
based_on:
  - РеализацияТоваров
  - Контрагент
fields:
  - name: Сумма
    type: number
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(path, KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.BasedOn) != 2 {
		t.Fatalf("BasedOn len = %d, want 2", len(e.BasedOn))
	}
	if e.BasedOn[0] != "РеализацияТоваров" || e.BasedOn[1] != "Контрагент" {
		t.Errorf("BasedOn = %v, want [РеализацияТоваров Контрагент]", e.BasedOn)
	}
}

func TestLoadFile_Indexes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.yaml")
	yaml := `name: Контрагенты
fields:
  - name: ИНН
    type: string
  - name: КПП
    type: string
indexes:
  - fields: [ИНН]
    unique: true
  - fields: [ИНН, КПП]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Indexes) != 2 {
		t.Fatalf("Indexes len = %d, want 2", len(e.Indexes))
	}
	if !e.Indexes[0].Unique || len(e.Indexes[0].Fields) != 1 || e.Indexes[0].Fields[0] != "ИНН" {
		t.Fatalf("first index = %+v, want unique [ИНН]", e.Indexes[0])
	}
	if e.Indexes[1].Unique || len(e.Indexes[1].Fields) != 2 {
		t.Fatalf("second index = %+v, want non-unique [ИНН КПП]", e.Indexes[1])
	}
}

func TestValidate_IndexUnknownField(t *testing.T) {
	entities := []*Entity{{
		Name:    "Контрагенты",
		Kind:    KindCatalog,
		Fields:  []Field{{Name: "ИНН", Type: FieldTypeString}},
		Indexes: []IndexSpec{{Fields: []string{"КПП"}}},
	}}
	if err := Validate(entities, nil); err == nil {
		t.Fatal("Validate должен был отклонить индекс по неизвестному полю")
	}
}

func TestValidate_BasedOnUnknown(t *testing.T) {
	entities := []*Entity{
		{Name: "ВозвратОтПокупателя", Kind: KindDocument, BasedOn: []string{"НесуществующийТип"}},
	}
	if err := Validate(entities, nil); err == nil {
		t.Fatal("Validate должен был отклонить based_on с неизвестным типом")
	}
}

func TestValidate_BasedOnKnown(t *testing.T) {
	entities := []*Entity{
		{Name: "РеализацияТоваров", Kind: KindDocument},
		{Name: "ВозвратОтПокупателя", Kind: KindDocument, BasedOn: []string{"РеализацияТоваров"}},
	}
	if err := Validate(entities, nil); err != nil {
		t.Fatalf("Validate с корректным based_on не должен возвращать ошибку: %v", err)
	}
}
