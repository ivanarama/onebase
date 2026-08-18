package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Колонка табличной части рисуется по типу поля (#1010): перечисление —
// списком, булево — флажком. До этого и то и другое было текстовым полем:
// значение перечисления набиралось руками (опечатка молча ломала прикладные
// сравнения), а флажок ещё и терялся — сервер отдавал в value сырое «1» из
// SQLite, а разбор сохранения признаёт истиной только «true».

func tpEditorsEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{
			Name: "Контакты",
			Fields: []metadata.Field{
				{Name: "Значение", Type: metadata.FieldTypeString},
				{Name: "Вид", Type: metadata.FieldType("enum:ВидыКонтактов"), EnumName: "ВидыКонтактов"},
				{Name: "Основной", Type: metadata.FieldTypeBool},
			},
		}},
	}
}

var tpEditorsLabels = map[string]map[string]map[string]string{
	"Контакты": {"Вид": {"Телефон": "Телефон", "Почта": "Почта", "Мессенджер": "Мессенджер"}},
}

var tpEditorsOrder = map[string]map[string][]string{
	"Контакты": {"Вид": {"Телефон", "Почта", "Мессенджер"}},
}

func renderTPEditorsAutoForm(t *testing.T, row map[string]any) string {
	t.Helper()
	ent := tpEditorsEntity()
	data := map[string]any{
		"Entity":        ent,
		"ID":            "11111111-1111-1111-1111-111111111111",
		"IsNew":         false,
		"CanWrite":      true,
		"Values":        map[string]string{"Наименование": "Ромашка"},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPRefMeta":     map[string]any{},
		"TPEnumLabels":  tpEditorsLabels,
		"TPEnumOrder":   tpEditorsOrder,
		"TablePartRows": map[string]any{"Контакты": []map[string]any{row}},
		"Lang":          "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-form", data); err != nil {
		t.Fatalf("ExecuteTemplate page-form: %v", err)
	}
	return buf.String()
}

func TestAutoFormTPEnumRendersSelect(t *testing.T) {
	html := renderTPEditorsAutoForm(t, map[string]any{"Значение": "+7", "Вид": "Почта", "Основной": int64(1)})
	if strings.Contains(html, `<input type="text" name="tp.Контакты.0.Вид"`) {
		t.Error("колонка-перечисление осталась текстовым полем")
	}
	if !strings.Contains(html, `<select name="tp.Контакты.0.Вид">`) {
		t.Fatalf("нет <select> для колонки-перечисления:\n%s", tpFragment(html))
	}
	// Порядок — как объявлены values:, а не алфавитный: карта подписей отдаёт
	// ключи в случайном порядке, поэтому список строится по TPEnumOrder.
	order := []string{`<option value="Телефон"`, `<option value="Почта" selected`, `<option value="Мессенджер"`}
	pos := -1
	for _, want := range order {
		at := strings.Index(html, want)
		if at < 0 {
			t.Fatalf("нет пункта %s:\n%s", want, tpFragment(html))
		}
		if at < pos {
			t.Errorf("порядок значений перечисления не совпадает с объявленным (%s)", want)
		}
		pos = at
	}
	// Пустой пункт обязателен: «не выбрано» — законное состояние, и без него
	// список не очистить.
	if !strings.Contains(html, `<option value="">`) {
		t.Error("нет пустого пункта для очистки значения")
	}
}

func TestAutoFormTPBoolRendersCheckbox(t *testing.T) {
	// int64(1) — ровно то, что отдаёт SQLite; именно на нём текстовое поле
	// показывало «1» и следующая запись формы снимала флажок.
	checked := renderTPEditorsAutoForm(t, map[string]any{"Значение": "+7", "Вид": "Телефон", "Основной": int64(1)})
	if !strings.Contains(checked, `<input type="checkbox" name="tp.Контакты.0.Основной" value="true" checked>`) {
		t.Errorf("взведённый флажок не отрисован:\n%s", tpFragment(checked))
	}
	unchecked := renderTPEditorsAutoForm(t, map[string]any{"Значение": "+7", "Вид": "Телефон", "Основной": false})
	if !strings.Contains(unchecked, `<input type="checkbox" name="tp.Контакты.0.Основной" value="true">`) {
		t.Errorf("снятый флажок не отрисован:\n%s", tpFragment(unchecked))
	}
	if strings.Contains(unchecked, `value="true" checked`) {
		t.Error("снятый флажок отрисован взведённым")
	}
}

// Значение, которого в перечислении больше нет, остаётся в списке отдельным
// пунктом. Без него браузер выбрал бы первый вариант, и простое открытие формы
// подменило бы данные.
func TestAutoFormTPEnumKeepsUnknownValue(t *testing.T) {
	html := renderTPEditorsAutoForm(t, map[string]any{"Значение": "+7", "Вид": "Факс"})
	if !strings.Contains(html, `<option value="Факс" selected`) {
		t.Fatalf("значение вне перечисления потеряно:\n%s", tpFragment(html))
	}
	if strings.Contains(html, `<option value="Телефон" selected`) {
		t.Error("значение молча подменено первым вариантом перечисления")
	}
}

// Строки, которые добавляет JS, сервер не рендерит: список значений и признак
// булевой колонки едут в разметке отдельно, иначе addTpRow снова поставил бы
// текстовое поле.
func TestAutoFormTPCarriesEnumDataForNewRows(t *testing.T) {
	html := renderTPEditorsAutoForm(t, map[string]any{"Значение": "+7", "Вид": "Телефон"})
	for _, want := range []string{
		`data-tp-bool-fields="Основной"`,
		`id="ob-tp-enum-labels"`,
		`id="ob-tp-enum-order"`,
		`"Вид":["Телефон","Почта","Мессенджер"]`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в разметке нет %s", want)
		}
	}
}

// Managed-форма с no_grid (простая DOM-таблица) рисуется тем же правилом.
// Перерисовка таблицы после события формы давно ставила там <select>, а
// первичный рендер оставался текстовым полем — то есть поведение зависело от
// того, дёрнул ли пользователь обработчик.
func TestManagedNoGridTPUsesTypedEditors(t *testing.T) {
	ent := tpEditorsEntity()
	form := managedObjectForm(
		fieldEl("ПолеНаименование", "Объект.Наименование"),
		&metadata.FormElement{
			Kind: metadata.FormElementTablePart, Name: "ТабКонтакты",
			DataPath: "Объект.Контакты", NoGrid: true,
		},
	)
	ent.Forms = []*metadata.FormModule{form}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": false, "CanWrite": true,
		"Values":        map[string]string{"Наименование": "Ромашка"},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPRefMeta":     map[string]any{},
		"TPEnumLabels":  tpEditorsLabels,
		"TPEnumOrder":   tpEditorsOrder,
		"TablePartRows": map[string][]map[string]any{
			"Контакты": {{"Значение": "+7", "Вид": "Почта", "Основной": true}},
		},
		"Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate page-managed-form: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `<select name="tp.Контакты.0.Вид">`) || !strings.Contains(html, `<option value="Почта" selected`) {
		t.Errorf("колонка-перечисление не стала списком:\n%s", tpFragment(html))
	}
	if !strings.Contains(html, `<input type="checkbox" name="tp.Контакты.0.Основной" value="true" checked>`) {
		t.Errorf("булева колонка не стала флажком:\n%s", tpFragment(html))
	}
}

func TestTPEnumOptions(t *testing.T) {
	t.Run("порядок из TPEnumOrder", func(t *testing.T) {
		got := tpEnumOptions(tpEditorsLabels, tpEditorsOrder, "Контакты", "Вид", "Почта")
		want := []string{"Телефон", "Почта", "Мессенджер"}
		if len(got) != len(want) {
			t.Fatalf("вариантов %d, ждали %d: %+v", len(got), len(want), got)
		}
		for i, v := range want {
			if got[i].Value != v {
				t.Fatalf("вариант %d = %q, ждали %q", i, got[i].Value, v)
			}
		}
		if !got[1].Selected {
			t.Error("текущее значение не помечено выбранным")
		}
	})
	t.Run("без порядка — детерминированный алфавит", func(t *testing.T) {
		got := tpEnumOptions(tpEditorsLabels, nil, "Контакты", "Вид", nil)
		want := []string{"Мессенджер", "Почта", "Телефон"}
		for i, v := range want {
			if got[i].Value != v {
				t.Fatalf("вариант %d = %q, ждали %q (порядок карты случаен — нужна сортировка)", i, got[i].Value, v)
			}
		}
	})
	t.Run("неизвестное значение попадает в список", func(t *testing.T) {
		got := tpEnumOptions(tpEditorsLabels, tpEditorsOrder, "Контакты", "Вид", "Факс")
		last := got[len(got)-1]
		if !last.Unknown || !last.Selected || last.Value != "Факс" {
			t.Fatalf("значение вне перечисления потеряно: %+v", got)
		}
	})
	t.Run("нет данных — пустой список, а не паника", func(t *testing.T) {
		if got := tpEnumOptions(nil, nil, "Контакты", "Вид", nil); len(got) != 0 {
			t.Fatalf("ждали пусто, получили %+v", got)
		}
		if got := tpEnumOptions(map[string]any{}, map[string]any{}, "Контакты", "Вид", ""); len(got) != 0 {
			t.Fatalf("ждали пусто, получили %+v", got)
		}
	})
}

func TestTruthyCell(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want bool
	}{
		{true, true}, {false, false},
		{int64(1), true}, {int64(0), false}, // SQLite
		{float64(1), true}, {float64(0), false}, // JSON строк ТЧ
		{"true", true}, {"1", true}, {"false", false}, {"", false},
		{nil, false},
	} {
		if got := truthyCell(tc.in); got != tc.want {
			t.Errorf("truthyCell(%#v) = %v, ждали %v", tc.in, got, tc.want)
		}
	}
}

// tpFragment вырезает кусок разметки табличной части — чтобы в отчёте теста
// была видна причина, а не вся страница.
func tpFragment(html string) string {
	at := strings.Index(html, "tp-body-")
	if at < 0 {
		return html
	}
	end := at + 1500
	if end > len(html) {
		end = len(html)
	}
	return html[at:end]
}
