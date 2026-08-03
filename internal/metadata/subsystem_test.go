package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSubsystemFile(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: Продажи
title: Продажи
order: 10
contents:
  documents: [РеализацияТоваров]
  catalogs:  [Контрагент, Номенклатура]
  reports:   [ОстаткиТоваров]
`
	path := filepath.Join(dir, "продажи.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSubsystemFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Продажи" {
		t.Errorf("name: got %q", s.Name)
	}
	if s.Order != 10 {
		t.Errorf("order: got %d", s.Order)
	}
	if len(s.Contents.Documents) != 1 || s.Contents.Documents[0] != "РеализацияТоваров" {
		t.Errorf("documents: got %v", s.Contents.Documents)
	}
	if len(s.Contents.Catalogs) != 2 {
		t.Errorf("catalogs: got %v", s.Contents.Catalogs)
	}
}

func TestLoadSubsystemFile_Roles(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: Склад
roles: [Кладовщик, Управленец]
contents:
  documents: [ПоступлениеТоваров]
`
	path := filepath.Join(dir, "склад.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSubsystemFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Roles) != 2 || s.Roles[0] != "Кладовщик" || s.Roles[1] != "Управленец" {
		t.Errorf("roles: got %v", s.Roles)
	}
}

func TestLoadSubsystemDir_Order(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"склад.yaml":   "name: Склад\norder: 30\n",
		"продажи.yaml": "name: Продажи\norder: 10\n",
		"закупки.yaml": "name: Закупки\norder: 20\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	subs, err := LoadSubsystemDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 3 {
		t.Fatalf("expected 3 subsystems, got %d", len(subs))
	}
	if subs[0].Name != "Продажи" || subs[1].Name != "Закупки" || subs[2].Name != "Склад" {
		t.Errorf("order wrong: got %v, %v, %v", subs[0].Name, subs[1].Name, subs[2].Name)
	}
}

func TestLoadSubsystemDir_Empty(t *testing.T) {
	subs, err := LoadSubsystemDir("/nonexistent/path")
	if err != nil {
		t.Errorf("expected nil error for missing dir, got: %v", err)
	}
	if subs != nil {
		t.Errorf("expected nil slice, got %v", subs)
	}
}

func TestLoadSubsystemFile_WithHomePage(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: Закупки
title: Закупки
order: 20
contents:
  documents: [ПоступлениеТоваров]
home_page:
  title: Рабочий стол закупщика
  rows:
    - widgets: [A, B]
    - widgets: [C]
`
	path := filepath.Join(dir, "закупки.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSubsystemFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.HomePage == nil {
		t.Fatal("HomePage is nil, expected non-nil")
	}
	if s.HomePage.Title != "Рабочий стол закупщика" {
		t.Errorf("HomePage.Title = %q", s.HomePage.Title)
	}
	// Несколько рядов без явного layout — осознанная раскладка по рядам:
	// applyDefaults ставит "rows", чтобы границы рядов соблюдались.
	if s.HomePage.Layout != "rows" {
		t.Errorf("HomePage.Layout = %q, want rows", s.HomePage.Layout)
	}
	names := s.HomePage.WidgetNames()
	if len(names) != 3 || names[0] != "A" || names[1] != "B" || names[2] != "C" {
		t.Errorf("WidgetNames = %v", names)
	}
}

func TestLoadSubsystemFile_NoHomePage(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: Продажи
title: Продажи
order: 10
contents:
  documents: [РеализацияТоваров]
`
	path := filepath.Join(dir, "продажи.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSubsystemFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.HomePage != nil {
		t.Errorf("HomePage should be nil, got %+v", s.HomePage)
	}
}
