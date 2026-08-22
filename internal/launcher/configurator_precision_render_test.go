package launcher

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/i18n"
)

func renderCfgTree(t *testing.T, data *configuratorData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "tab-tree", data); err != nil {
		t.Fatalf("ExecuteTemplate tab-tree: %v", err)
	}
	return buf.String()
}

// Регистр накопления: у числовых полей должны быть инпуты Длина/Точность, а у
// каждой секции — кнопка «+ Добавить» (раньше их не было вовсе, нельзя было
// добавить поле через UI).
func TestRegisterDetail_PrecisionAndAddButtons(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "X", ConfigSource: "file"}, Lang: "ru", Tab: "tree",
		Registers: []cfgRegister{{
			Name:       "Остатки",
			Dimensions: []cfgField{{Name: "Товар", Type: "string"}},
			Resources:  []cfgField{{Name: "Сумма", Type: "number", Length: 15, Scale: 2}},
		}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{
		`name="res.0.length"`,
		`name="res.0.scale"`,
		`cfgAddField('rg-dim-Остатки','new_dim','','register')`,
		`cfgAddField('rg-res-Остатки','new_res','','register')`,
		`cfgAddField('rg-attr-Остатки','new_attr','','register')`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("register-detail: нет фрагмента %q", want)
		}
	}
}

// Регистр сведений: у числовых измерений/ресурсов — инпуты Длина/Точность.
func TestInfoRegDetail_Precision(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "X", ConfigSource: "file"}, Lang: "ru", Tab: "tree",
		InfoRegisters: []cfgInfoRegister{{
			Name:       "Курсы",
			Dimensions: []cfgField{{Name: "Валюта", Type: "string"}},
			Resources:  []cfgField{{Name: "Курс", Type: "number", Length: 10, Scale: 4}},
		}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{`name="res.0.length"`, `name="res.0.scale"`} {
		if !strings.Contains(html, want) {
			t.Errorf("inforeg-detail: нет фрагмента %q", want)
		}
	}
}

// План счетов: у числового ресурса — инпуты Длина/Точность.
func TestAccountRegDetail_Precision(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "X", ConfigSource: "file"}, Lang: "ru", Tab: "tree",
		AccountRegisters: []cfgAccountRegister{{
			Name:      "Бухучёт",
			Resources: []cfgField{{Name: "Сумма", Type: "number", Length: 18, Scale: 2}},
		}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{`name="res.0.length"`, `name="res.0.scale"`} {
		if !strings.Contains(html, want) {
			t.Errorf("accountreg-detail: нет фрагмента %q", want)
		}
	}
}

// Регистр накопления с AvailableLangs: titles-блоки должны рендериться.
// Проверяет, что AvailableLangs теперь передаётся в под-шаблон register-detail
// (ранее guard {{if $.AvailableLangs}} был всегда ложен из-за отсутствия ключа).
func TestRegisterDetail_TitlesBlockRendered(t *testing.T) {
	data := &configuratorData{
		Base:           &Base{ID: "b", Name: "X", ConfigSource: "file"},
		Lang:           "ru",
		Tab:            "tree",
		AvailableLangs: []i18n.Lang{{Code: "en", Native: "English"}},
		Registers: []cfgRegister{{
			Name:       "Остатки",
			Dimensions: []cfgField{{Name: "Товар", Type: "string"}},
			Resources:  []cfgField{{Name: "Сумма", Type: "number"}},
		}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{
		`name="titles.en"`,
		`name="dim.0.titles.en"`,
		`name="res.0.titles.en"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("register-detail: titles-блок не найден: %q", want)
		}
	}
}

// Регистр сведений с AvailableLangs: titles-блоки объекта и измерений/ресурсов
// должны рендериться в inline-форме (доступ к $.AvailableLangs из корня).
func TestInfoRegDetail_TitlesBlockRendered(t *testing.T) {
	data := &configuratorData{
		Base:           &Base{ID: "b", Name: "X", ConfigSource: "file"},
		Lang:           "ru",
		Tab:            "tree",
		AvailableLangs: []i18n.Lang{{Code: "en", Native: "English"}},
		InfoRegisters: []cfgInfoRegister{{
			Name:       "Курсы",
			Dimensions: []cfgField{{Name: "Валюта", Type: "string"}},
			Resources:  []cfgField{{Name: "Курс", Type: "number"}},
		}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{
		`name="titles.en"`,
		`name="dim.0.titles.en"`,
		`name="res.0.titles.en"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("inforeg-detail: titles-блок не найден: %q", want)
		}
	}
}

// Регистр бухгалтерии с AvailableLangs: titles-блоки объекта и ресурсов
// должны рендериться в inline-форме (доступ к $.AvailableLangs из корня).
func TestAccountRegDetail_TitlesBlockRendered(t *testing.T) {
	data := &configuratorData{
		Base:           &Base{ID: "b", Name: "X", ConfigSource: "file"},
		Lang:           "ru",
		Tab:            "tree",
		AvailableLangs: []i18n.Lang{{Code: "en", Native: "English"}},
		AccountRegisters: []cfgAccountRegister{{
			Name:      "Хозрасчетный",
			Accounts:  "ПланСчетов",
			Resources: []cfgField{{Name: "Сумма", Type: "number"}},
		}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{
		`name="titles.en"`,
		`name="res.0.titles.en"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("accountreg-detail: titles-блок не найден: %q", want)
		}
	}
}

// Справочник с AvailableLangs: titles-блоки объекта и реквизита должны рендериться.
// Проверяет, что AvailableLangs передаётся в под-шаблон entity-detail
// (защита от регрессии класса T7: если ключ выпадет из dict — блоки пропадут незаметно).
func TestEntityDetail_TitlesBlockRendered(t *testing.T) {
	data := &configuratorData{
		Base:           &Base{ID: "b", Name: "X", ConfigSource: "file"},
		Lang:           "ru",
		Tab:            "tree",
		AvailableLangs: []i18n.Lang{{Code: "en", Native: "English"}},
		Catalogs: []cfgEntity{{
			Name:   "Товар",
			Kind:   "Справочник",
			Fields: []cfgField{{Name: "Артикул", Type: "string"}},
		}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{
		`name="titles.en"`,
		`name="field.0.titles.en"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("entity-detail: titles-блок не найден: %q", want)
		}
	}
}

// Поля переводов больше не оборачиваются в спойлер <details> у каждого
// реквизита — это плоский <div class="titles-block">, видимостью которого
// управляет глобальный режим переводов (кнопка 🌐 в топбаре). Защита от
// возврата к «спойлеру у каждого поля», от которого захламлялся экран.
func TestTitlesBlock_FlatNotDetails(t *testing.T) {
	data := &configuratorData{
		Base:           &Base{ID: "b", Name: "X", ConfigSource: "file"},
		Lang:           "ru",
		Tab:            "tree",
		AvailableLangs: []i18n.Lang{{Code: "en", Native: "English"}},
		Catalogs: []cfgEntity{{
			Name:   "Товар",
			Kind:   "Справочник",
			Fields: []cfgField{{Name: "Артикул", Type: "string"}},
		}},
	}
	html := renderCfgTree(t, data)
	if !strings.Contains(html, `<div class="titles-block">`) {
		t.Error(`titles-block должен быть плоским <div class="titles-block">`)
	}
	if strings.Contains(html, `<details class="titles-block"`) {
		t.Error("titles-block не должен быть <details> — спойлер у каждого поля убран")
	}
}

// Константа: у типа «число» — инпуты Длина/Точность (имена length/scale).
func TestConstantDetail_Precision(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "X", ConfigSource: "file"}, Lang: "ru", Tab: "tree",
		Constants: []cfgConstant{{Name: "СтавкаНДС", Type: "number", Length: 5, Scale: 2, Label: "НДС"}},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{`name="length"`, `name="scale"`} {
		if !strings.Contains(html, want) {
			t.Errorf("constant-detail: нет фрагмента %q", want)
		}
	}
}
