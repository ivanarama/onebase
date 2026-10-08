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

func TestLoadWidgetFile_RefreshOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	writeFile(t, path, `name: ЗадачиКоллЦентра
type: list
title: Задачи
limit: 30
refresh_on:
  - данные.а_задача
  - задача.переназначена
query: |
  ВЫБРАТЬ Ссылка, Тема ИЗ Документ.А_Задача
`)
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(w.RefreshOn) != 2 || w.RefreshOn[0] != "данные.а_задача" || w.RefreshOn[1] != "задача.переназначена" {
		t.Fatalf("RefreshOn = %v", w.RefreshOn)
	}
}

func TestLoadWidgetFile_NoRefreshOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.yaml")
	writeFile(t, path, "name: Выручка\ntype: kpi\nformat: money\nquery: |\n  ВЫБРАТЬ 0 КАК Значение\n")
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(w.RefreshOn) != 0 {
		t.Fatalf("RefreshOn = %v, want empty", w.RefreshOn)
	}
}

func TestLoadWidgetFile_Source(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nav.yaml")
	writeFile(t, path, `name: Задачи
type: list
limit: 30
source:
  entity: А_Задача
  id_field: Ссылка
query: |
  ВЫБРАТЬ Ссылка, Тема ИЗ Документ.А_Задача
`)
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w.Source == nil || w.Source.Entity != "А_Задача" || w.Source.IDField != "Ссылка" {
		t.Fatalf("Source = %+v", w.Source)
	}
}

func TestLoadWidgetFile_Filters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filtered.yaml")
	writeFile(t, path, `name: Задачи
type: list
filters:
  - name: Тема
    label: Тема
    type: string
    param: Тема
  - name: Статус
    type: select
    param: Статус
    values:
      - value: open
        label: Открыта
      - value: closed
        label: Закрыта
    default: open
query: |
  ВЫБРАТЬ Тема ИЗ Документ.А_Задача ГДЕ &Статус
`)
	w, err := LoadWidgetFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(w.Filters) != 2 {
		t.Fatalf("Filters = %+v", w.Filters)
	}
	if w.Filters[0].Type != "string" || w.Filters[1].Type != "select" || len(w.Filters[1].Values) != 2 || w.Filters[1].Default != "open" {
		t.Fatalf("Filters content = %+v", w.Filters)
	}
	if w.Filters[1].Values[0].DisplayLabel("ru") != "Открыта" {
		t.Fatalf("value label = %+v", w.Filters[1].Values[0])
	}
}
