package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func reportSettingsTestReport(name string) *reportpkg.Report {
	return &reportpkg.Report{
		Name: name,
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
}

func TestEffectiveComposition(t *testing.T) {
	main := &reportpkg.Composition{Groupings: []string{"Основной"}}
	variantComp := &reportpkg.Composition{Groupings: []string{"Вариант"}}
	rep := &reportpkg.Report{
		Composition: main,
		Variants:    []reportpkg.ReportVariant{{Name: "V", Composition: variantComp}},
	}

	// 1) settings.Composition применяется ПРЕЗЕНТАЦИОННО поверх доверенной базы
	//    (вариант по имени), а не подменяет её целиком (issue #1).
	override := &reportpkg.Composition{Groupings: []string{"Override"}}
	got := effectiveComposition(rep, &reportpkg.UserReportSettings{Variant: "V", Composition: override})
	if len(got.Groupings) != 1 || got.Groupings[0] != "Override" {
		t.Fatalf("презентационные правки не применены: %+v", got.Groupings)
	}
	// 2) settings без Composition → активный вариант по имени.
	if got := effectiveComposition(rep, &reportpkg.UserReportSettings{Variant: "V"}); got != variantComp {
		t.Fatalf("variant: %+v", got)
	}
	// 3) settings == nil → основной composition.
	if got := effectiveComposition(rep, nil); got != main {
		t.Fatalf("main: %+v", got)
	}
}

func TestEffectiveCompositionRequiresTrustedBase(t *testing.T) {
	rep := &reportpkg.Report{Name: "plain"}
	settings := &reportpkg.UserReportSettings{
		Composition: &reportpkg.Composition{
			Groupings:  []string{},
			Measures:   []reportpkg.Measure{},
			Appearance: reportpkg.Appearance{Lines: "both"},
		},
		Filters: []reportpkg.Filter{{Field: "Сумма", Op: "gt", Value: "100"}},
	}
	if got := effectiveComposition(rep, settings); got != nil {
		t.Fatalf("отчёт без YAML-composition не должен переходить в режим СКД: %+v", got)
	}
	if raw := reportSettingsPanelJSON(rep, settings); raw != "" {
		t.Fatalf("панель настроек для плоского отчёта должна быть скрыта, got %s", raw)
	}
}

func TestReportSettingsWithRequestVariant(t *testing.T) {
	main := &reportpkg.Composition{Groupings: []string{"Основной"}}
	variantComp := &reportpkg.Composition{Groupings: []string{"Вариант"}}
	rep := &reportpkg.Report{
		Composition: main,
		Variants:    []reportpkg.ReportVariant{{Name: "V", Composition: variantComp}},
	}

	req := httptest.NewRequest(http.MethodPost, "/ui/report/sales", strings.NewReader("__variant=V"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settings := reportSettingsWithRequestVariant(req, nil)
	if settings == nil || settings.Variant != "V" {
		t.Fatalf("__variant не попал в настройки: %+v", settings)
	}
	if got := effectiveComposition(rep, settings); got != variantComp {
		t.Fatalf("__variant должен выбрать вариант компоновки, got %+v", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/ui/report/sales", strings.NewReader("__variant="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settings = reportSettingsWithRequestVariant(req, &reportpkg.UserReportSettings{Variant: "V"})
	if settings == nil || settings.Variant != "" {
		t.Fatalf("явный пустой __variant должен сбрасывать на основной, got %+v", settings)
	}
	if got := effectiveComposition(rep, settings); got != main {
		t.Fatalf("пустой __variant должен выбрать основную компоновку, got %+v", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/ui/report/sales", nil)
	settings = reportSettingsWithRequestVariant(req, &reportpkg.UserReportSettings{Variant: "V"})
	if settings == nil || settings.Variant != "V" {
		t.Fatalf("без __variant сохранённые настройки не должны меняться, got %+v", settings)
	}
}

func TestReportSettingsForRequestPresetKeepsPresetVariant(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preset-variant.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rep := reportSettingsTestReport("Продажи")
	id, err := db.SaveReportPreset(ctx, storage.ReportPreset{
		Report:       "Продажи",
		User:         "",
		Name:         "Вариант пользователя",
		SettingsJSON: `{"variant":"YAML-V"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{store: db}
	req := httptest.NewRequest(http.MethodPost, "/ui/report/Продажи", strings.NewReader("__preset="+url.QueryEscape(id)+"&__variant="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	got := srv.reportSettingsForRequest(req, rep)
	if got.ActivePresetID != id {
		t.Fatalf("active preset: %q", got.ActivePresetID)
	}
	if got.Settings == nil || got.Settings.Variant != "YAML-V" {
		t.Fatalf("preset variant должен сохраниться, got %+v", got.Settings)
	}
}

// TestEffectiveCompositionNoDSLExecution: ключевой тест безопасности (issue #1).
// Пользователь присылает __settings с вычисляемым показателем (Expr) и условием
// оформления (When), содержащими вредоносное DSL. Эффективная компоновка НЕ
// должна нести этот Expr/When — исполняемые выражения берутся только из
// доверенной конфигурации (или обнуляются для показателей, которых там нет).
func TestEffectiveCompositionNoDSLExecution(t *testing.T) {
	const evil = `ЗапуститьПриложение("calc.exe")`
	// Доверенная конфигурация: один обычный показатель «Сумма» и один доверенный
	// вычисляемый «Маржа». Условие оформления — безопасное.
	trusted := &reportpkg.Composition{
		Groupings: []string{"Товар"},
		Measures: []reportpkg.Measure{
			{Field: "Сумма", Agg: "sum"},
			{Field: "Маржа", Expr: "Сумма * 0.2"},
		},
		Conditional: []reportpkg.CondRule{
			{When: "Сумма < 0", Field: "", Style: reportpkg.CellStyle{Color: "#c00"}},
		},
	}
	rep := &reportpkg.Report{Composition: trusted}

	// Пользовательский ввод: пытается (а) внедрить новый показатель с вредоносным
	// Expr, (б) подменить Expr доверенного показателя, (в) подменить When условия.
	userComp := &reportpkg.Composition{
		Groupings: []string{"Товар"},
		Measures: []reportpkg.Measure{
			{Field: "Сумма", Agg: "sum"},
			{Field: "Маржа", Expr: evil},           // подмена доверенного Expr
			{Field: "Хак", Agg: "sum", Expr: evil}, // внедрённый показатель с Expr
		},
		Conditional: []reportpkg.CondRule{
			{When: evil, Field: "", Style: reportpkg.CellStyle{Color: "red"}},
		},
	}
	eff := effectiveComposition(rep, &reportpkg.UserReportSettings{Composition: userComp})

	// Ни один Expr/When эффективной компоновки не должен равняться вредоносному.
	for _, m := range eff.Measures {
		if strings.Contains(m.Expr, evil) {
			t.Fatalf("вредоносный Expr протёк в показатель %q: %q", m.Field, m.Expr)
		}
	}
	for _, c := range eff.Conditional {
		if strings.Contains(c.When, evil) {
			t.Fatalf("вредоносное условие When протекло: %q", c.When)
		}
	}
	// Доверенный Expr «Маржа» сохранён из конфигурации (а не обнулён/подменён).
	var marja *reportpkg.Measure
	for i := range eff.Measures {
		if eff.Measures[i].Field == "Маржа" {
			marja = &eff.Measures[i]
		}
	}
	if marja == nil || marja.Expr != "Сумма * 0.2" {
		t.Fatalf("доверенный Expr «Маржа» должен быть из конфигурации, got %+v", marja)
	}
	// Внедрённый показатель «Хак» (нет в доверенной) — без Expr (не исполняется).
	for _, m := range eff.Measures {
		if m.Field == "Хак" && m.Expr != "" {
			t.Fatalf("внедрённый показатель «Хак» не должен иметь Expr, got %q", m.Expr)
		}
	}
	// Условия оформления (When+Style) целиком из доверенной конфигурации.
	if len(eff.Conditional) != 1 || eff.Conditional[0].When != "Сумма < 0" {
		t.Fatalf("Conditional должен быть из доверенной конфигурации, got %+v", eff.Conditional)
	}
}

// TestEffectiveCompositionAppearance: оформление (Appearance) — презентация,
// поэтому берётся из пользовательского ввода (в отличие от Conditional/Expr).
// Недопустимое значение Lines канонизируется в "" (safeAppearance), чтобы мусор
// не уходил в CSS-класс рендера и не оседал в _settings.
func TestEffectiveCompositionAppearance(t *testing.T) {
	trusted := &reportpkg.Composition{
		Groupings:  []string{"Товар"},
		Measures:   []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		Appearance: reportpkg.Appearance{Lines: "horizontal"}, // дефолт отчёта
	}
	rep := &reportpkg.Report{Composition: trusted}

	// 1) Пользователь включает вертикальные линии и зебру — применяется.
	userComp := &reportpkg.Composition{
		Groupings:  []string{"Товар"},
		Measures:   []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		Appearance: reportpkg.Appearance{Lines: "both", Zebra: true},
	}
	eff := effectiveComposition(rep, &reportpkg.UserReportSettings{Composition: userComp})
	if eff.Appearance.Lines != "both" || !eff.Appearance.Zebra {
		t.Fatalf("пользовательское оформление не применено: %+v", eff.Appearance)
	}

	// 2) Мусорный Lines из __settings канонизируется в "".
	evilComp := &reportpkg.Composition{
		Groupings:  []string{"Товар"},
		Measures:   []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		Appearance: reportpkg.Appearance{Lines: "evil; }body{display:none"},
	}
	eff2 := effectiveComposition(rep, &reportpkg.UserReportSettings{Composition: evilComp})
	if eff2.Appearance.Lines != "" {
		t.Fatalf("мусорный Lines должен канонизироваться в \"\": %q", eff2.Appearance.Lines)
	}
}

func TestEffectiveCompositionIgnoresEmptyMeasureOverride(t *testing.T) {
	trusted := &reportpkg.Composition{
		Groupings: []string{"Организация", "Номенклатура"},
		Columns:   []string{"Месяц"},
		Measures: []reportpkg.Measure{
			{Field: "Выручка", Agg: "sum", Title: "Выручка"},
			{Field: "ВаловаяПрибыль", Agg: "sum", Title: "Валовая прибыль"},
		},
		Totals: reportpkg.Totals{Grand: true, Subtotals: true},
		Sort:   []reportpkg.SortKey{{Field: "ВаловаяПрибыль", Dir: "desc"}},
	}
	rep := &reportpkg.Report{Composition: trusted}

	// Такой JSON сохранял старый UI, когда чекбоксы не были предзаполнены:
	// пустые Groupings/Measures перекрывали базовую composition и отчёт
	// переставал нормально формироваться.
	userComp := &reportpkg.Composition{
		Groupings: []string{},
		Measures:  []reportpkg.Measure{},
	}
	eff := effectiveComposition(rep, &reportpkg.UserReportSettings{Composition: userComp})
	if strings.Join(eff.Groupings, ",") != "Организация,Номенклатура" {
		t.Fatalf("пустой override не должен стирать группировки: %+v", eff.Groupings)
	}
	if strings.Join(eff.Columns, ",") != "Месяц" {
		t.Fatalf("пустой override не должен стирать колонки: %+v", eff.Columns)
	}
	if len(eff.Measures) != 2 || eff.Measures[1].Field != "ВаловаяПрибыль" {
		t.Fatalf("пустой override не должен стирать показатели: %+v", eff.Measures)
	}
	if !eff.Totals.Grand || !eff.Totals.Subtotals {
		t.Fatalf("пустой override не должен стирать итоги: %+v", eff.Totals)
	}
	if len(eff.Sort) != 1 || eff.Sort[0].Field != "ВаловаяПрибыль" {
		t.Fatalf("пустой override не должен стирать сортировку: %+v", eff.Sort)
	}

	userComp = &reportpkg.Composition{
		Groupings: []string{},
	}
	eff = effectiveComposition(rep, &reportpkg.UserReportSettings{Composition: userComp})
	if strings.Join(eff.Groupings, ",") != "Организация,Номенклатура" {
		t.Fatalf("пустые группировки без Measures тоже не должны стирать YAML: %+v", eff.Groupings)
	}
	if len(eff.Measures) != 2 || eff.Measures[0].Field != "Выручка" {
		t.Fatalf("повреждённый частичный override не должен стирать показатели: %+v", eff.Measures)
	}
}

func TestEffectiveCompositionInheritsMeasurePresentationDefaults(t *testing.T) {
	trusted := &reportpkg.Composition{
		Groupings: []string{"Организация"},
		Measures: []reportpkg.Measure{
			{Field: "ВаловаяПрибыль", Agg: "sum", Title: "Валовая прибыль", Align: "right", Format: "#,##0.00", Expr: "Выручка - Себестоимость"},
		},
	}
	rep := &reportpkg.Report{Composition: trusted}

	userComp := &reportpkg.Composition{
		Groupings: []string{"организация"},
		Measures:  []reportpkg.Measure{{Field: "валоваяприбыль"}},
	}
	eff := effectiveComposition(rep, &reportpkg.UserReportSettings{Composition: userComp})
	if len(eff.Groupings) != 1 || eff.Groupings[0] != "Организация" {
		t.Fatalf("группировка должна канонизироваться к YAML-регистру: %+v", eff.Groupings)
	}
	if len(eff.Measures) != 1 {
		t.Fatalf("ожидали один показатель, got %+v", eff.Measures)
	}
	m := eff.Measures[0]
	if m.Field != "ВаловаяПрибыль" {
		t.Fatalf("поле показателя должно канонизироваться к YAML-регистру: %+v", m)
	}
	if m.Title != "Валовая прибыль" || m.Align != "right" || m.Format != "#,##0.00" || m.Agg != "sum" {
		t.Fatalf("презентационные дефолты из YAML потерялись: %+v", m)
	}
	if m.Expr != "Выручка - Себестоимость" {
		t.Fatalf("доверенный Expr должен наследоваться из YAML, got %q", m.Expr)
	}
}

func TestReportSettingsPanelJSONUsesBaseComposition(t *testing.T) {
	rep := &reportpkg.Report{
		Name: "ВаловаяПрибыльСКД",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Организация", "Номенклатура"},
			Columns:   []string{"Месяц"},
			Measures: []reportpkg.Measure{
				{Field: "Выручка", Agg: "sum", Title: "Выручка", Format: "#,##0.00"},
				{Field: "ВаловаяПрибыль", Agg: "sum", Title: "Валовая прибыль", Format: "#,##0.00"},
			},
			Totals: reportpkg.Totals{Grand: true, Subtotals: true},
			Sort:   []reportpkg.SortKey{{Field: "ВаловаяПрибыль", Dir: "desc"}},
		},
	}
	raw := reportSettingsPanelJSON(rep, nil)
	if raw == "" {
		t.Fatalf("ожидали JSON предзаполнения панели")
	}
	for _, want := range []string{"Организация", "Номенклатура", "ВаловаяПрибыль", "Месяц", "Grand", "Sort"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("JSON панели не содержит %q: %s", want, raw)
		}
	}
}

// TestReportSettingsPanel: панель «Настройки» рендерится при наличии ReportCols,
// содержит чекбоксы доступных полей и скрытое поле __settings.
func TestReportSettingsPanel(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "sales",
		Title: "Продажи",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
	preset := storage.ReportPreset{ID: "p1", Name: "Мой вариант", IsDefault: true}
	settings := &reportpkg.UserReportSettings{
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportCols":         []string{"Товар", "Сумма"},
		"ReportPresets":      []storage.ReportPreset{preset},
		"ActivePresetID":     "p1",
		"ActivePreset":       &preset,
		"UserSettings":       settings,
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, settings),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `data-block="settings"`) {
		t.Fatalf("нет блока настроек data-block=settings")
	}
	if !strings.Contains(out, `name="__settings"`) {
		t.Fatalf("нет скрытого поля __settings")
	}
	for _, want := range []string{`name="__preset"`, `Мой вариант`, `name="__preset_action" value="save"`, `name="__preset_action" value="save_as"`} {
		if !strings.Contains(out, want) {
			t.Errorf("в панели нет элемента пользовательских вариантов %q", want)
		}
	}
	for _, want := range []string{"Товар", "Сумма"} {
		if !strings.Contains(out, want) {
			t.Errorf("в панели нет поля %q", want)
		}
	}
}

func TestReportSettingsPanelBasePresetWithoutChangedIndicator(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "profit",
		Title: "Прибыль",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Организация"},
			Measures:  []reportpkg.Measure{{Field: "ВаловаяПрибыль", Agg: "sum"}},
		},
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportCols":         []string{"Организация", "ВаловаяПрибыль"},
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, nil),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `id="rs-json"`) || !strings.Contains(out, "Организация") || !strings.Contains(out, "ВаловаяПрибыль") {
		t.Fatalf("панель не получила базовый JSON настроек: %s", out)
	}
	if strings.Contains(out, "изменено") {
		t.Fatalf("базовая composition не должна показываться как пользовательское изменение")
	}
	if !strings.Contains(out, "disabled") {
		t.Fatalf("кнопка сброса должна быть disabled без пользовательских настроек")
	}
}

func TestReportSettingsPanelChecksLowercaseQueryColumns(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "profit",
		Title: "Прибыль",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Организация"},
			Measures:  []reportpkg.Measure{{Field: "ВаловаяПрибыль", Agg: "sum", Format: "#,##0.00"}},
		},
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportCols":         []string{"организация", "валоваяприбыль"},
		"UserSettings":       &reportpkg.UserReportSettings{Composition: &reportpkg.Composition{Groupings: []string{}}},
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, nil),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`class="rs-group" value="организация" checked`,
		`class="rs-measure" value="валоваяприбыль" checked`,
		`function rsNorm`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("панель должна отметить lower-case колонку %q: %s", want, out)
		}
	}
}

func TestReportParamsFormKeepsActiveSettings(t *testing.T) {
	rep := &reportpkg.Report{
		Name:   "profit",
		Title:  "Прибыль",
		Params: []reportpkg.Param{{Name: "Начало", Type: "date"}},
		Composition: &reportpkg.Composition{
			Groupings: []string{"Организация"},
			Measures:  []reportpkg.Measure{{Field: "ВаловаяПрибыль", Agg: "sum"}},
		},
	}
	settings := &reportpkg.UserReportSettings{
		Composition: &reportpkg.Composition{
			Groupings: []string{"Организация"},
			Measures:  []reportpkg.Measure{{Field: "ВаловаяПрибыль", Agg: "sum"}},
		},
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{"Начало": "2026-07-01"},
		"ReportParams":       []reportParamUI{{Name: "Начало", Label: "С даты", Type: "date", IsDate: true}},
		"UserSettings":       settings,
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, settings),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `name="__settings"`) || !strings.Contains(out, "ВаловаяПрибыль") {
		t.Fatalf("форма параметров не сохраняет активные настройки: %s", out)
	}
}

// TestReportSettingsPanelAppearance: панель «Настройки» содержит контролы
// оформления (линии сетки + зебра), через которые пользователь задаёт вид себе.
func TestReportSettingsPanelAppearance(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "sales",
		Title: "Продажи",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportCols":         []string{"Товар", "Сумма"},
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, nil),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`id="rs-lines"`, `id="rs-zebra"`, `value="vertical"`, `value="both"`} {
		if !strings.Contains(out, want) {
			t.Errorf("в панели нет контрола оформления %q", want)
		}
	}
}

// TestReportSettingsPanelHidden: без ReportCols или без composition панель не
// рендерится (обратная совместимость с плоскими отчётами).
func TestReportSettingsPanelHidden(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "skd",
		Title: "СКД",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, nil),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	if strings.Contains(buf.String(), `data-block="settings"`) {
		t.Errorf("панель настроек не должна рендериться без ReportCols")
	}

	plain := &reportpkg.Report{Name: "plain", Title: "Простой"}
	buf.Reset()
	data = map[string]any{
		"Report":             plain,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportCols":         []string{"Товар", "Сумма"},
		"ReportSettingsJSON": reportSettingsPanelJSON(plain, &reportpkg.UserReportSettings{Composition: &reportpkg.Composition{Appearance: reportpkg.Appearance{Lines: "both"}}}),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute plain page-report: %v", err)
	}
	if strings.Contains(buf.String(), `data-block="settings"`) {
		t.Errorf("панель настроек не должна рендериться у плоского отчёта даже при наличии ReportCols")
	}
}

// TestReportSettingsFilters: сохранённые отборы предзаполняют строки панели —
// select поля, select оператора (с выбранным gt) и значение.
func TestReportSettingsFilters(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "sales",
		Title: "Продажи",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
	settings := &reportpkg.UserReportSettings{
		Filters: []reportpkg.Filter{{Field: "Сумма", Op: "gt", Value: "100"}},
	}
	var buf bytes.Buffer
	data := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportCols":         []string{"Товар", "Сумма"},
		"UserSettings":       settings,
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, settings),
		"Cfg":                Config{},
		"Lang":               "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="rs-f-field"`) {
		t.Errorf("нет select поля отбора")
	}
	if !strings.Contains(out, `class="rs-f-op"`) {
		t.Fatalf("нет select оператора отбора")
	}
	if !strings.Contains(out, `value="gt" selected`) {
		t.Errorf("оператор gt не помечен selected")
	}
	if !strings.Contains(out, `value="100"`) {
		t.Errorf("значение отбора 100 не предзаполнено")
	}
}

// TestReportSettingsSaveReset: обработчики save/reset пишут и удаляют per-user
// настройки в _settings (для анонима user="").
func TestReportSettingsSaveReset(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}

	rep := reportSettingsTestReport("Продажи")
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	raw := `{"variant":"X"}`
	form := url.Values{"__settings": {raw}}
	r := reqWithChi("POST", "/ui/report/Продажи/settings/save", form, map[string]string{"name": "Продажи"})
	w := httptest.NewRecorder()
	s.reportSettingsSave(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save: ожидался 303, получен %d", w.Code)
	}
	if got, _ := db.GetReportUserSettings(ctx, "Продажи", ""); !strings.Contains(got, `"variant":"X"`) || !strings.Contains(got, "Товар") {
		t.Fatalf("save: настройки должны сохраниться в каноничном виде, получили %q", got)
	}

	r2 := reqWithChi("POST", "/ui/report/Продажи/settings/reset", url.Values{}, map[string]string{"name": "Продажи"})
	w2 := httptest.NewRecorder()
	s.reportSettingsReset(w2, r2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("reset: ожидался 303, получен %d", w2.Code)
	}
	if got, _ := db.GetReportUserSettings(ctx, "Продажи", ""); got != "" {
		t.Fatalf("reset: ожидали пусто, получили %q", got)
	}
}

func TestReportSettingsSaveNamedPreset(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preset-save.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	rep := reportSettingsTestReport("Продажи")
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	form := url.Values{
		"__settings":       {`{"variant":"X"}`},
		"__preset_action":  {"save_as"},
		"__preset_name":    {"По товарам"},
		"__preset_default": {"1"},
	}
	r := reqWithChi("POST", "/ui/report/Продажи/settings/save", form, map[string]string{"name": "Продажи"})
	w := httptest.NewRecorder()
	s.reportSettingsSave(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save_as: ожидался 303, получен %d: %s", w.Code, w.Body.String())
	}
	presets, err := db.ListReportPresets(ctx, "Продажи", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 1 || presets[0].Name != "По товарам" || !presets[0].IsDefault {
		t.Fatalf("пресет не сохранён как default: %+v", presets)
	}
	if !strings.Contains(presets[0].SettingsJSON, `"variant":"X"`) {
		t.Fatalf("settings_json не сохранён: %q", presets[0].SettingsJSON)
	}

	form2 := url.Values{
		"__settings":      {`{"variant":"Y"}`},
		"__preset":        {presets[0].ID},
		"__preset_action": {"save"},
		"__preset_name":   {"По товарам v2"},
	}
	r2 := reqWithChi("POST", "/ui/report/Продажи/settings/save", form2, map[string]string{"name": "Продажи"})
	w2 := httptest.NewRecorder()
	s.reportSettingsSave(w2, r2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("save existing: ожидался 303, получен %d: %s", w2.Code, w2.Body.String())
	}
	p, _ := db.GetReportPreset(ctx, "Продажи", "", presets[0].ID)
	if p == nil || p.Name != "По товарам v2" || !strings.Contains(p.SettingsJSON, `"variant":"Y"`) {
		t.Fatalf("пресет не обновлён: %+v", p)
	}

	r3 := reqWithChi("POST", "/ui/report/Продажи/settings/delete", url.Values{"__preset": {presets[0].ID}}, map[string]string{"name": "Продажи"})
	w3 := httptest.NewRecorder()
	s.reportPresetDelete(w3, r3)
	if w3.Code != http.StatusSeeOther {
		t.Fatalf("delete: ожидался 303, получен %d", w3.Code)
	}
	if p, _ := db.GetReportPreset(ctx, "Продажи", "", presets[0].ID); p != nil {
		t.Fatalf("пресет должен быть удалён: %+v", p)
	}
}

func TestReportSettingsSaveFromStandardCreatesReachablePreset(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preset-save-standard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _report_presets
		(id, report_name, user_login, name, settings_json, is_default)
		VALUES (?, ?, ?, ?, ?, ?)`,
		standardReportPresetID, "Продажи", "", "По товарам", `{"variant":"broken"}`, true); err != nil {
		t.Fatal(err)
	}

	rep := &reportpkg.Report{
		Name:   "Продажи",
		Params: []reportpkg.Param{{Name: "Начало", Type: "date"}},
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	form := url.Values{
		"__settings":      {`{"variant":"X"}`},
		"__preset":        {standardReportPresetID},
		"__preset_action": {"save"},
		"Начало":          {"2026-07-01"},
	}
	r := reqWithChi("POST", "/ui/report/Продажи/settings/save", form, map[string]string{"name": "Продажи"})
	w := httptest.NewRecorder()
	s.reportSettingsSave(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save from standard: ожидался 303, получен %d: %s", w.Code, w.Body.String())
	}

	presets, err := db.ListReportPresets(ctx, "Продажи", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 1 {
		t.Fatalf("ожидали один доступный пресет, got %+v", presets)
	}
	if presets[0].ID == standardReportPresetID {
		t.Fatalf("нельзя сохранять пользовательский вариант с reserved id %q", presets[0].ID)
	}
	if presets[0].Name != defaultReportPresetName {
		t.Fatalf("пустое имя должно получить автозаголовок %q, got %+v", defaultReportPresetName, presets[0])
	}
	if !strings.Contains(presets[0].SettingsJSON, `"variant":"X"`) {
		t.Fatalf("settings_json не сохранён: %q", presets[0].SettingsJSON)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "__run=1") || !strings.Contains(loc, "__preset="+url.QueryEscape(presets[0].ID)) || !strings.Contains(loc, "%D0%9D%D0%B0%D1%87%D0%B0%D0%BB%D0%BE=2026-07-01") {
		t.Fatalf("редирект должен вернуть на сформированный отчёт с параметрами и новым пресетом: %q", loc)
	}

	form2 := url.Values{
		"__settings":      {`{"variant":"Y"}`},
		"__preset":        {standardReportPresetID},
		"__preset_action": {"save"},
	}
	r2 := reqWithChi("POST", "/ui/report/Продажи/settings/save", form2, map[string]string{"name": "Продажи"})
	w2 := httptest.NewRecorder()
	s.reportSettingsSave(w2, r2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("second save from standard: ожидался 303, получен %d: %s", w2.Code, w2.Body.String())
	}
	presets, err = db.ListReportPresets(ctx, "Продажи", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 2 {
		t.Fatalf("ожидали два пресета после второго автосохранения, got %+v", presets)
	}
	var foundSecond bool
	for _, p := range presets {
		if p.Name == defaultReportPresetName+" 2" {
			foundSecond = true
		}
	}
	if !foundSecond {
		t.Fatalf("второй пустой save должен получить уникальное имя, got %+v", presets)
	}
}

func TestReportSettingsSaveDuplicatePresetNameReturnsConflict(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preset-save-dup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.SaveReportPreset(ctx, storage.ReportPreset{
		Report:       "Продажи",
		User:         "",
		Name:         "По товарам",
		SettingsJSON: `{"variant":"A"}`,
	}); err != nil {
		t.Fatal(err)
	}

	rep := reportSettingsTestReport("Продажи")
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	form := url.Values{
		"__settings":      {`{"variant":"B"}`},
		"__preset_action": {"save_as"},
		"__preset_name":   {"По товарам"},
	}
	r := reqWithChi("POST", "/ui/report/Продажи/settings/save", form, map[string]string{"name": "Продажи"})
	w := httptest.NewRecorder()
	s.reportSettingsSave(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate save_as: ожидался 409, получен %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), storage.ErrReportPresetNameExists.Error()) {
		t.Fatalf("duplicate save_as должен вернуть понятную ошибку, got %q", w.Body.String())
	}
	presets, err := db.ListReportPresets(ctx, "Продажи", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 1 || presets[0].SettingsJSON != `{"variant":"A"}` {
		t.Fatalf("duplicate save_as не должен менять существующий пресет: %+v", presets)
	}
}

func TestReportSettingsSaveNamedPresetNormalizesCurrentComposition(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preset-save-normalized.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	rep := &reportpkg.Report{
		Name: "ВаловаяПрибыльСКД",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Организация", "Номенклатура"},
			Measures: []reportpkg.Measure{
				{Field: "Выручка", Agg: "sum", Title: "Выручка", Format: "#,##0.00"},
				{Field: "Себестоимость", Agg: "sum", Title: "Себестоимость", Format: "#,##0.00"},
				{Field: "ВаловаяПрибыль", Agg: "sum", Title: "Валовая прибыль", Format: "#,##0.00"},
			},
			Totals: reportpkg.Totals{Grand: true, Subtotals: true},
			Sort:   []reportpkg.SortKey{{Field: "ВаловаяПрибыль", Dir: "desc"}},
		},
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	form := url.Values{
		"__settings":      {`{"composition":{"Groupings":[],"Measures":[]}}`},
		"__preset_action": {"save_as"},
		"__preset_name":   {"Текущий"},
	}
	r := reqWithChi("POST", "/ui/report/ВаловаяПрибыльСКД/settings/save", form, map[string]string{"name": "ВаловаяПрибыльСКД"})
	w := httptest.NewRecorder()
	s.reportSettingsSave(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save_as: ожидался 303, получен %d: %s", w.Code, w.Body.String())
	}
	presets, err := db.ListReportPresets(ctx, "ВаловаяПрибыльСКД", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 1 {
		t.Fatalf("ожидали один пресет, got %+v", presets)
	}
	if strings.Contains(presets[0].SettingsJSON, `"Measures":[]`) {
		t.Fatalf("пустые показатели не должны сохраняться: %s", presets[0].SettingsJSON)
	}
	st, err := reportpkg.ParseUserSettings(presets[0].SettingsJSON)
	if err != nil {
		t.Fatalf("ParseUserSettings: %v", err)
	}
	eff := effectiveComposition(rep, st)
	if len(eff.Groupings) != 2 || len(eff.Measures) != 3 {
		t.Fatalf("сохранённый пресет не восстановил текущую компоновку: %+v", eff)
	}
	if eff.Measures[2].Field != "ВаловаяПрибыль" || eff.Measures[2].Format != "#,##0.00" {
		t.Fatalf("формат показателя потерян: %+v", eff.Measures[2])
	}
}

// TestReportSettingsSaveRejectsLargeAndInvalid: save отвергает слишком большой
// блок __settings и битый JSON, а корректный — сохраняет в каноничном виде (issue #23).
func TestReportSettingsSaveRejectsLargeAndInvalid(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs23.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	rep := reportSettingsTestReport("Продажи")
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	// 1) Слишком большое значение → 413, в БД ничего не записано.
	big := strings.Repeat("x", maxUserSettingsBytes+1)
	form := url.Values{"__settings": {big}}
	r := reqWithChi("POST", "/ui/report/Продажи/settings/save", form, map[string]string{"name": "Продажи"})
	w := httptest.NewRecorder()
	s.reportSettingsSave(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("большое значение: ожидали 413, получили %d", w.Code)
	}
	if got, _ := db.GetReportUserSettings(ctx, "Продажи", ""); got != "" {
		t.Fatalf("большое значение не должно сохраняться, got %q", got)
	}

	// 2) Битый JSON → 400, ничего не записано.
	form2 := url.Values{"__settings": {"{не json"}}
	r2 := reqWithChi("POST", "/ui/report/Продажи/settings/save", form2, map[string]string{"name": "Продажи"})
	w2 := httptest.NewRecorder()
	s.reportSettingsSave(w2, r2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("битый JSON: ожидали 400, получили %d", w2.Code)
	}
	if got, _ := db.GetReportUserSettings(ctx, "Продажи", ""); got != "" {
		t.Fatalf("битый JSON не должен сохраняться, got %q", got)
	}

	// 3) Корректное значение → 303, в БД лежит каноничный JSON.
	form3 := url.Values{"__settings": {`{"variant":"X"}`}}
	r3 := reqWithChi("POST", "/ui/report/Продажи/settings/save", form3, map[string]string{"name": "Продажи"})
	w3 := httptest.NewRecorder()
	s.reportSettingsSave(w3, r3)
	if w3.Code != http.StatusSeeOther {
		t.Fatalf("корректное значение: ожидали 303, получили %d", w3.Code)
	}
	got, _ := db.GetReportUserSettings(ctx, "Продажи", "")
	if got == "" || !strings.Contains(got, `"variant":"X"`) {
		t.Fatalf("каноничный JSON не сохранён: %q", got)
	}
}

// TestLoadUserSettings: автозагрузка сохранённых настроек по пользователю.
func TestLoadUserSettings(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "load.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveReportUserSettings(ctx, "Продажи", "alice", `{"variant":"X"}`); err != nil {
		t.Fatal(err)
	}

	if s := loadUserSettings(ctx, db, "Продажи", "alice"); s == nil || s.Variant != "X" {
		t.Fatalf("alice: %+v", s)
	}
	if s := loadUserSettings(ctx, db, "Продажи", "bob"); s != nil {
		t.Fatalf("bob: ожидали nil, получили %+v", s)
	}
}

// TestReportSettingsIndicator: при активных настройках панель показывает
// пометку «изменено», а кнопка сброса активна; без настроек — кнопка disabled.
func TestReportSettingsIndicator(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "sales",
		Title: "Продажи",
		Composition: &reportpkg.Composition{
			Groupings: []string{"Товар"},
			Measures:  []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}},
		},
	}
	base := map[string]any{
		"Report":             rep,
		"ParamValues":        map[string]any{},
		"ReportParams":       []reportParamUI{},
		"ReportCols":         []string{"Товар", "Сумма"},
		"ReportSettingsJSON": reportSettingsPanelJSON(rep, nil),
		"Cfg":                Config{},
		"Lang":               "ru",
	}

	// С активными настройками.
	withSettings := map[string]any{}
	for k, v := range base {
		withSettings[k] = v
	}
	settings := &reportpkg.UserReportSettings{Variant: "X"}
	withSettings["UserSettings"] = settings
	withSettings["ReportSettingsJSON"] = reportSettingsPanelJSON(rep, settings)
	var b1 bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b1, "page-report", withSettings); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(b1.String(), "изменено") {
		t.Errorf("нет пометки «изменено» при активных настройках")
	}

	// Без настроек — кнопка сброса disabled.
	var b2 bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b2, "page-report", base); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := b2.String()
	if strings.Contains(out, "изменено") {
		t.Errorf("пометка «изменено» не должна показываться без настроек")
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("кнопка сброса должна быть disabled без настроек")
	}
}
