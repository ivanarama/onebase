package launcher

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
)

// richCfgData строит «богатый» configuratorData: данными заполнены все
// data-rich ветки шаблона (подсистемы с составом и рабочим столом, виджеты,
// глобальная главная с рядами, управляемые формы, отчёты, обработки, поля
// справочников/документов). Именно эти ветки — основная поверхность
// execute-time-ошибок html/template (после миграции с text/template), поэтому
// smoke-тест должен их реально исполнить, а не оставить нулевыми.
func richCfgData(tab string) *configuratorData {
	return &configuratorData{
		Base: &Base{ID: "b", Name: "Тест", ConfigSource: "file", Port: 8080},
		Lang: "ru",
		Tab:  tab,
		Catalogs: []cfgEntity{{
			Name: "Номенклатура", Kind: "Справочник",
			Fields: []cfgField{
				{Name: "Цена", Type: "number"},
				{Name: "Поставщик", Type: "reference", RefEntity: "Контрагенты"},
				{Name: "Вид", Type: "enum", EnumName: "ВидыНоменклатуры"},
			},
		}},
		Docs: []cfgEntity{{
			Name: "Реализация", Kind: "Документ", Posting: true,
			Fields: []cfgField{{Name: "Сумма", Type: "number"}},
			TableParts: []cfgTablePart{{
				Name:   "Товары",
				Fields: []cfgField{{Name: "Количество", Type: "number"}},
			}},
		}},
		Registers: []cfgRegister{{
			Name:       "Продажи",
			Dimensions: []cfgField{{Name: "Номенклатура", Type: "reference", RefEntity: "Номенклатура"}},
			Resources:  []cfgField{{Name: "Сумма", Type: "number"}},
		}},
		InfoRegisters: []cfgInfoRegister{{
			Name:       "Цены",
			Periodic:   true,
			Dimensions: []cfgField{{Name: "Номенклатура", Type: "reference", RefEntity: "Номенклатура"}},
			Resources:  []cfgField{{Name: "Цена", Type: "number"}},
		}},
		AccountRegisters: []cfgAccountRegister{{
			Name: "Хозрасчётный", Title: "Хозрасчётный", Accounts: "ПланСчетов",
			Resources: []cfgField{{Name: "Сумма", Type: "number"}},
		}},
		Enums:     []cfgEnum{{Name: "ВидыНоменклатуры", Values: []string{"Товар", "Услуга"}}},
		Constants: []cfgConstant{{Name: "ОсновнаяВалюта", Type: "string", Label: "Основная валюта", Default: "RUB"}},
		Reports:   []cfgReport{{Name: "Продажи", Title: "Продажи за период"}},
		Modules:   []cfgModule{{Name: "ОбщийМодуль", Source: "Процедура Тест() КонецПроцедуры"}},
		Processors: []cfgProcessor{{
			Name: "Загрузка", Title: "Загрузка данных",
			Params: []cfgParam{{Name: "Файл", Type: "string", Label: "Файл"}},
		}},
		Pages: []cfgPage{{Name: "Старт", Title: "Стартовая"}},
		Subsystems: []cfgSubsystem{{
			Name:  "Продажи",
			Title: "Продажи",
			Icon:  "shopping-cart",
			Order: 1,
			Contents: metadata.SubsystemContents{
				Catalogs:   []string{"Номенклатура"},
				Documents:  []string{"Реализация"},
				Registers:  []string{"Продажи"},
				InfoRegs:   []string{"Цены"},
				Reports:    []string{"Продажи"},
				Processors: []string{"Загрузка"},
			},
			HomeWidgets: []string{"ВыручкаЗаДень"},
			HomeRows:    [][]string{{"ВыручкаЗаДень"}},
			HomeLayout:  "rows",
		}},
		Widgets: []cfgWidget{{
			Name: "ВыручкаЗаДень", Type: "kpi", Title: "Выручка за день",
			YAML: "name: ВыручкаЗаДень\ntype: kpi\n",
		}},
		WidgetOptions: []widgetOption{{Name: "ВыручкаЗаДень", Title: "Выручка за день"}},
		GlobalHome: cfgHomePage{
			Title:   "Главная",
			Widgets: []string{"ВыручкаЗаДень"},
			Rows:    [][]string{{"ВыручкаЗаДень"}},
			Layout:  "rows",
		},
		HomePageYAML: "title: Главная\n",
		ManagedForms: []cfgManagedForm{{
			Entity: "номенклатура", Name: "ФормаСписка", Kind: "list",
			YAML: "kind: list\n", YAMLPath: "forms/номенклатура/формасписка.form.yaml",
		}},
		AllEntityNames: []string{"Номенклатура", "Реализация"},
		AllEnumNames:   []string{"ВидыНоменклатуры"},
		PlatformVer:    "test",
		UIServerURL:    "http://localhost:8080",
		DSNMasked:      "sqlite://test.db",
	}
}

// TestConfigurator_PagesRender: каждая верхнеуровневая вкладка рендерится через
// html/template без execute-ошибки и содержит свой якорь. Вкладка "tree"
// рендерится на «богатых» данных, чтобы исполнить data-rich ветки (подсистемы,
// виджеты, главная, управляемые формы) — основной источник execute-ошибок
// html/template после миграции с text/template (план 55, защита от регресса).
func TestConfigurator_PagesRender(t *testing.T) {
	// Якоря — фрагменты, уникальные для тела конкретной вкладки (а не общий
	// syntax-ref-panel, который cfg-main рендерит для всех вкладок), чтобы сабтест
	// доказывал рендер именно этой вкладки, а не только отсутствие execute-ошибки.
	cases := []struct{ tab, anchor string }{
		{"tree", `class="obj-tabs"`},
		{"convert", `id="convert-src-dir"`},
		{"files", `class="files-grid"`},
		{"backup", `name="backup_schedule"`},
	}
	for _, c := range cases {
		t.Run(c.tab, func(t *testing.T) {
			var buf bytes.Buffer
			if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-main", richCfgData(c.tab)); err != nil {
				t.Fatalf("рендер вкладки %q: %v", c.tab, err)
			}
			if !strings.Contains(buf.String(), c.anchor) {
				t.Fatalf("во вкладке %q нет якоря %q", c.tab, c.anchor)
			}
		})
	}
}

func renderCfgMain(t *testing.T, data *configuratorData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-main", data); err != nil {
		t.Fatalf("ExecuteTemplate cfg-main: %v", err)
	}
	return buf.String()
}

// Глобальный тумблер режима переводов: одна кнопка в топбаре вместо спойлера у
// каждого поля. Есть на вкладке «Дерево» (где правят объекты), нет на других
// вкладках. Раннее восстановление состояния (класс cfg-titles-on из localStorage)
// присутствует в head, чтобы поля не «прыгали» при загрузке.
func TestTitlesToggle_TopbarButton(t *testing.T) {
	// Кнопка есть на «Дерево», когда в конфигурации настроены языки интерфейса
	// (только тогда есть что переводить).
	withLangs := richCfgData("tree")
	withLangs.AvailableLangs = []i18n.Lang{{Code: "en", Native: "English"}}
	tree := renderCfgMain(t, withLangs)
	if !strings.Contains(tree, `id="cfg-titles-toggle"`) {
		t.Error("на вкладке «Дерево» нет кнопки режима переводов")
	}
	if !strings.Contains(tree, "cfgTitlesToggle()") {
		t.Error("кнопка режима переводов не вызывает cfgTitlesToggle()")
	}
	if !strings.Contains(tree, "cfg-titles-on") || !strings.Contains(tree, "cfgTitlesOn") {
		t.Error("в head нет раннего восстановления режима переводов из localStorage")
	}

	// Нет языков интерфейса → переводить нечего, кнопки быть не должно.
	if noLangs := renderCfgMain(t, richCfgData("tree")); strings.Contains(noLangs, `id="cfg-titles-toggle"`) {
		t.Error("кнопка режима переводов не должна показываться без языков интерфейса")
	}

	// Вне «Дерево» кнопки нет даже при наличии языков.
	files := richCfgData("files")
	files.AvailableLangs = []i18n.Lang{{Code: "en", Native: "English"}}
	if out := renderCfgMain(t, files); strings.Contains(out, `id="cfg-titles-toggle"`) {
		t.Error("кнопка режима переводов не должна показываться вне «Дерева»")
	}
}

// TestConfigurator_CSSExternalized: CSS вынесен в /static/configurator.css —
// в HTML присутствует <link>, а инлайн-правила (напр. .cfg-modal-body{) ушли
// из рендера в файл (план 55 фаза 2a).
func TestConfigurator_CSSExternalized(t *testing.T) {
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-main", richCfgData("tree")); err != nil {
		t.Fatalf("ExecuteTemplate cfg-main: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `href="/static/configurator.css"`) {
		t.Error("нет <link> на /static/configurator.css")
	}
	if strings.Contains(out, ".cfg-modal-body{") {
		t.Error("инлайн-CSS всё ещё в рендере — должен быть в файле")
	}
}

// TestConfigurator_JSExternalized: основной JS вынесен в /static/configurator.js —
// в HTML присутствует <script src>, тело главного скрипта (напр. функция
// cfgNewObj) ушло из рендера в файл, а bootstrap-<script> (window.__cfg) идёт
// СТРОГО РАНЬШЕ подключения файла, чтобы данные были доступны при его загрузке
// (план 55 фаза 2b-2).
func TestConfigurator_JSExternalized(t *testing.T) {
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-main", richCfgData("tree")); err != nil {
		t.Fatalf("ExecuteTemplate cfg-main: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `src="/static/configurator.js"`) {
		t.Error("нет <script src> на /static/configurator.js")
	}
	if strings.Contains(out, "function cfgNewObj(") {
		t.Error("инлайн-JS всё ещё в рендере — должен быть в файле")
	}
	iBoot := strings.Index(out, "window.__cfg")
	iSrc := strings.Index(out, `src="/static/configurator.js"`)
	if iBoot < 0 {
		t.Fatal("нет bootstrap window.__cfg в рендере")
	}
	if iBoot >= iSrc {
		t.Errorf("bootstrap window.__cfg (%d) должен идти раньше <script src> (%d)", iBoot, iSrc)
	}
}
