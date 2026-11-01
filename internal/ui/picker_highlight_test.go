package ui

// Конфиг.ПолеВыделения — строки подбора, где поле Истина, выделяются (как
// архивная номенклатура в подборе 1С). Тем же путём, что и пользователь:
// событие формы → pickerData.

import (
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func pickerHighlightResponse(t *testing.T, config string) formEventResponse {
	t.Helper()
	srv, ent := setupManagedEventsServer(t, `
Процедура ПодборНажатие()
	Колонки = Новый Массив;
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Наименование", "Деталь", "string"));
	Данные = Новый Массив;
	Данные.Добавить(Новый Структура("Идентификатор,Наименование,Архивная", "u-1", "Скоба", Ложь));
	Данные.Добавить(Новый Структура("Идентификатор,Наименование,Архивная", "u-2", "Активатор", Истина));
	Конфиг = Новый Структура("Заголовок", "Подбор");
	`+config+`
	ПоказатьПодбор(Данные, Колонки, Конфиг);
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind:     metadata.FormElementButton,
			Name:     "КнопкаПодбор",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ПодборНажатие"},
		},
	})
	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnClick))
	return decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
}

func TestPicker_HighlightField(t *testing.T) {
	resp := pickerHighlightResponse(t, `Конфиг.Вставить("ПолеВыделения", "Архивная");`)
	if resp.PickerData == nil || len(resp.PickerData.Rows) != 2 {
		t.Fatalf("ждали pickerData с двумя строками; error=%q", resp.Error)
	}
	if resp.PickerData.Rows[0].Highlight || !resp.PickerData.Rows[1].Highlight {
		t.Fatalf("выделена должна быть только архивная строка: %+v", resp.PickerData.Rows)
	}
	if _, ok := resp.PickerData.Rows[1].Data["Архивная"]; ok {
		t.Errorf("поле выделения не колонка и в данные строки не попадает: %+v", resp.PickerData.Rows[1].Data)
	}
}

func TestPicker_NoHighlightByDefault(t *testing.T) {
	resp := pickerHighlightResponse(t, "")
	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData; error=%q", resp.Error)
	}
	for _, r := range resp.PickerData.Rows {
		if r.Highlight {
			t.Fatalf("без ПолеВыделения строки не выделяются: %+v", resp.PickerData.Rows)
		}
	}
}

// Highlight must survive the same form event that configures the newer window
// options; both selectable and read-only pickers share this response path.
func TestPicker_HighlightWithWindowOptions(t *testing.T) {
	for _, noSelect := range []bool{false, true} {
		t.Run(map[bool]string{false: "selectable", true: "view-only"}[noSelect], func(t *testing.T) {
			mode := "Ложь"
			if noSelect {
				mode = "Истина"
			}
			resp := pickerHighlightResponse(t, `
Конфиг.Вставить("ПолеВыделения", "Архивная");
Конфиг.Вставить("Ширина", 960);
Поля = Новый Массив;
Поля.Добавить(Новый Структура("Имя,Заголовок,Значение", "Артикул", "Артикул", "C005"));
Конфиг.Вставить("ПоляШапки", Поля);
Конфиг.Вставить("БезВыбора", `+mode+`);`)
			if resp.Error != "" || resp.PickerData == nil || len(resp.PickerData.Rows) != 2 {
				t.Fatalf("form event response: %+v", resp)
			}
			cfg := resp.PickerData.Config
			if cfg.Width != 960 || cfg.NoSelect != noSelect || len(cfg.HeaderFields) != 1 {
				t.Fatalf("window options lost: %+v", cfg)
			}
			if field := cfg.HeaderFields[0]; field.Name != "Артикул" || field.Value != "C005" {
				t.Fatalf("header field lost: %+v", field)
			}
			if resp.PickerData.Rows[0].Highlight || !resp.PickerData.Rows[1].Highlight {
				t.Fatalf("highlight lost: %+v", resp.PickerData.Rows)
			}
			if _, ok := resp.PickerData.Rows[1].Data["Архивная"]; ok {
				t.Fatal("non-column highlight flag leaked into row data")
			}
		})
	}
}
