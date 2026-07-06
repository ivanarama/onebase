package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadWidgetFile_KPI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "w.yaml")
	writeFile(t, path, `name: Выручка
type: kpi
title: Выручка месяца
format: money
params:
  Начало: "{{today|start_of_month}}"
query: |
  ВЫБРАТЬ СУММА(Сумма) КАК Значение ИЗ Документ.Продажа ГДЕ Дата >= &Начало
`)
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w.Name != "Выручка" || w.Type != WidgetTypeKPI || w.Format != "money" {
		t.Fatalf("unexpected widget: %+v", w)
	}
	if got := w.Params["Начало"]; got != "{{today|start_of_month}}" {
		t.Errorf("Params Начало = %q, want template", got)
	}
}

func TestLoadWidgetFile_ListDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.yaml")
	writeFile(t, path, `name: Top
type: list
title: Top
query: SELECT 1`)
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w.Limit != 10 {
		t.Errorf("list default limit = %d, want 10", w.Limit)
	}
}

func TestLoadWidgetFile_ChartDefaultKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	writeFile(t, path, `name: Динамика
type: chart
title: Динамика
query: SELECT 1`)
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w.ChartKind != "bar" {
		t.Errorf("chart default kind = %q, want bar", w.ChartKind)
	}
}

func TestLoadWidgetFile_ChartTypeAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	writeFile(t, path, `name: Динамика
type: chart
title: Динамика
query: SELECT 1
chart_type: line`)
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w.ChartKind != "line" {
		t.Errorf("chart_type alias = %q, want line", w.ChartKind)
	}
}

func TestLoadWidgetFile_UnknownType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	writeFile(t, path, `name: X
type: gauge
title: ?`)
	if _, err := LoadWidgetFile(path); err == nil {
		t.Fatal("expected error on unknown widget type")
	}
}

func TestLoadWidgetFile_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	writeFile(t, path, `type: kpi
title: nope`)
	if _, err := LoadWidgetFile(path); err == nil {
		t.Fatal("expected error on missing name")
	}
}

func TestLoadWidgetDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), `name: A
type: kpi
title: A
query: SELECT 1`)
	writeFile(t, filepath.Join(dir, "b.yaml"), `name: B
type: list
title: B
query: SELECT 1`)
	// non-yaml ignored
	writeFile(t, filepath.Join(dir, "readme.md"), "ignored")

	widgets, err := LoadWidgetDir(dir)
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if len(widgets) != 2 {
		t.Fatalf("expected 2 widgets, got %d", len(widgets))
	}
}

func TestLoadWidgetDir_MissingDir(t *testing.T) {
	widgets, err := LoadWidgetDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if widgets != nil {
		t.Errorf("missing dir = %v, want nil", widgets)
	}
}
