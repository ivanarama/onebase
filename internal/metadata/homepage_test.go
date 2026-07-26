package metadata

import (
	"path/filepath"
	"testing"
)

func TestLoadHomePage_Rows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "home_page.yaml")
	writeFile(t, path, `title: Главная
layout: rows
rows:
  - widgets: [A, B]
  - widgets: [C]
`)
	hp, err := LoadHomePage(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if hp == nil {
		t.Fatal("hp is nil")
		return
	}
	if hp.Title != "Главная" || hp.Layout != "rows" {
		t.Errorf("unexpected hp: %+v", hp)
	}
	names := hp.WidgetNames()
	if len(names) != 3 || names[0] != "A" || names[1] != "B" || names[2] != "C" {
		t.Errorf("WidgetNames = %v", names)
	}
}

func TestLoadHomePage_Grid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "home_page.yaml")
	writeFile(t, path, `widgets:
  - { name: A, span: 1 }
  - { name: B, span: 3 }
`)
	hp, err := LoadHomePage(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if hp.Layout != "grid" {
		t.Errorf("default layout for flat widgets = %q, want grid", hp.Layout)
	}
	if len(hp.Widgets) != 2 || hp.Widgets[1].Span != 3 {
		t.Errorf("unexpected widgets: %+v", hp.Widgets)
	}
}

func TestLoadHomePage_Missing(t *testing.T) {
	hp, err := LoadHomePage(filepath.Join(t.TempDir(), "no.yaml"))
	if err != nil {
		t.Fatalf("missing should not error: %v", err)
	}
	if hp != nil {
		t.Errorf("missing returned hp = %+v, want nil", hp)
	}
}

func TestLoadHomePage_Hidden(t *testing.T) {
	dir := t.TempDir()
	// hidden: true парсится в HomePage.Hidden (issue #304).
	path := filepath.Join(dir, "home_page.yaml")
	writeFile(t, path, "hidden: true\n")
	hp, err := LoadHomePage(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if hp == nil || !hp.Hidden {
		t.Fatalf("hidden не распарсился: %+v", hp)
	}
	// Без ключа hidden главная видима по умолчанию.
	path2 := filepath.Join(dir, "home2.yaml")
	writeFile(t, path2, "title: Главная\n")
	hp2, err := LoadHomePage(path2)
	if err != nil {
		t.Fatalf("load2: %v", err)
	}
	if hp2.Hidden {
		t.Error("home_page без hidden должна быть видимой")
	}
}

func TestRowGroups(t *testing.T) {
	hp := &HomePage{Rows: []HomePageRow{
		{Widgets: []string{"A", "B"}},
		{Widgets: []string{"C"}},
	}}
	groups := hp.RowGroups()
	if len(groups) != 2 {
		t.Fatalf("RowGroups len = %d, want 2", len(groups))
	}
	if len(groups[0]) != 2 || groups[0][0] != "A" || groups[0][1] != "B" {
		t.Errorf("group 0 = %v, want [A B]", groups[0])
	}
	if len(groups[1]) != 1 || groups[1][0] != "C" {
		t.Errorf("group 1 = %v, want [C]", groups[1])
	}

	// Flat widgets append as a trailing group.
	hp2 := &HomePage{
		Rows:    []HomePageRow{{Widgets: []string{"A"}}},
		Widgets: []HomePageWidget{{Name: "X"}, {Name: "Y"}},
	}
	g2 := hp2.RowGroups()
	if len(g2) != 2 || len(g2[1]) != 2 || g2[1][0] != "X" || g2[1][1] != "Y" {
		t.Errorf("RowGroups with flat = %v", g2)
	}

	if (*HomePage)(nil).RowGroups() != nil {
		t.Error("nil HomePage RowGroups should be nil")
	}
}

func TestApplyDefaults(t *testing.T) {
	// Empty → title "Главная", layout "rows"
	h := &HomePage{}
	h.applyDefaults()
	if h.Title != "Главная" {
		t.Errorf("default title = %q, want %q", h.Title, "Главная")
	}
	if h.Layout != "auto" {
		t.Errorf("default layout (no widgets) = %q, want auto", h.Layout)
	}

	// With flat widgets → layout "grid"
	h2 := &HomePage{Widgets: []HomePageWidget{{Name: "X"}}}
	h2.applyDefaults()
	if h2.Layout != "grid" {
		t.Errorf("default layout (with widgets) = %q, want grid", h2.Layout)
	}

	// Single row, no explicit layout → "auto" (нейтральный старт)
	h4 := &HomePage{Rows: []HomePageRow{{Widgets: []string{"A", "B"}}}}
	h4.applyDefaults()
	if h4.Layout != "auto" {
		t.Errorf("default layout (single row) = %q, want auto", h4.Layout)
	}

	// Multiple rows, no explicit layout → "rows" (осознанная раскладка)
	h5 := &HomePage{Rows: []HomePageRow{
		{Widgets: []string{"A"}},
		{Widgets: []string{"B", "C"}},
	}}
	h5.applyDefaults()
	if h5.Layout != "rows" {
		t.Errorf("default layout (multi row) = %q, want rows", h5.Layout)
	}

	// Explicit values preserved
	h3 := &HomePage{Title: "Мой стол", Layout: "rows"}
	h3.applyDefaults()
	if h3.Title != "Мой стол" {
		t.Errorf("explicit title overwritten: %q", h3.Title)
	}
	if h3.Layout != "rows" {
		t.Errorf("explicit layout overwritten: %q", h3.Layout)
	}
}
