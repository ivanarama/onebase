package ui

import (
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// План 46, фаза 1: обработчик кнопки вызывает ПоказатьПодбор(Данные,Колонки,
// Конфиг). form-event должен вернуть pickerData с колонками, строками и
// конфигом, НЕ применяя ТЧ (диалог открывается на клиенте).
func TestPicker_ShowPickerReturnsPickerData(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПодборНажатие()
	Колонки = Новый Массив;
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип,Редактируемое", "Номенклатура", "Товар", "string", Ложь));
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип,Редактируемое", "Количество", "Кол-во", "number", Истина));
	Данные = Новый Массив;
	Данные.Добавить(Новый Структура("Идентификатор,Номенклатура,Количество", "u-1", "Болт", 5));
	Данные.Добавить(Новый Структура("Идентификатор,Номенклатура,Количество", "u-2", "Гайка", 3));
	Конфиг = Новый Структура("Заголовок,ПолеПоиска,ПолеКоличества", "Подбор товаров", "Номенклатура", "Количество");
	ПоказатьПодбор(Данные, Колонки, Конфиг);
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton,
			Name: "КнопкаПодбор",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "ПодборНажатие",
			},
		},
	})

	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnClick))

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData != nil; body=%s", rec.Body.String())
	}
	pd := resp.PickerData
	if len(pd.Columns) != 2 {
		t.Fatalf("ждали 2 колонки, получили %d (%+v)", len(pd.Columns), pd.Columns)
	}
	if pd.Columns[0].Name != "Номенклатура" || pd.Columns[0].Title != "Товар" {
		t.Errorf("колонка 0 = %+v", pd.Columns[0])
	}
	if !pd.Columns[1].Editable || pd.Columns[1].Type != "number" {
		t.Errorf("колонка «Количество» должна быть editable/number: %+v", pd.Columns[1])
	}
	if len(pd.Rows) != 2 {
		t.Fatalf("ждали 2 строки, получили %d", len(pd.Rows))
	}
	if pd.Rows[0].ID != "u-1" || pd.Rows[0].Data["Номенклатура"] != "Болт" {
		t.Errorf("строка 0 = %+v", pd.Rows[0])
	}
	if pd.Config.Title != "Подбор товаров" || pd.Config.SearchField != "Номенклатура" {
		t.Errorf("config = %+v", pd.Config)
	}
}

// План 46, фаза 2: _pick_result (JSON) разбирается в переменную ПодборРезультат,
// доступную обработчику события Выбор. Проверяем через Сообщить.
func TestPicker_PickResultParsedToVariable(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПодборВыбор()
	Для Каждого Стр Из ПодборРезультат Цикл
		Сообщить(Стр.Номенклатура);
	КонецЦикла;
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton,
			Name: "КнопкаПодбор",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnChoice: "ПодборВыбор",
			},
		},
	})

	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnChoice))
	body.Set("_pick_result", `[{"id":"u-1","Номенклатура":"Болт","Количество":"5"},{"id":"u-2","Номенклатура":"Гайка","Количество":"3"}]`)

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())

	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 2 || resp.Messages[0] != "Болт" || resp.Messages[1] != "Гайка" {
		t.Errorf("messages=%v, ждали [Болт Гайка]", resp.Messages)
	}
}

// parsePickResult: корректный JSON → Массив MapThis; пустой/битый → nil.
func TestParsePickResult(t *testing.T) {
	arr := parsePickResult(`[{"id":"a","Кол":"2"}]`)
	if arr == nil {
		t.Fatal("ждали непустой Массив")
	}
	items := arr.Iterate()
	if len(items) != 1 {
		t.Fatalf("ждали 1 элемент, получили %d", len(items))
	}
	mt, ok := items[0].(*interpreter.MapThis)
	if !ok {
		t.Fatalf("ждали *MapThis, получили %T", items[0])
	}
	if mt.Get("id") != "a" || mt.Get("кол") != "2" { // MapThis регистронезависим
		t.Errorf("значения не совпали: %+v", mt.M)
	}
	if parsePickResult("") != nil {
		t.Error("пустая строка должна давать nil")
	}
	if parsePickResult("{не json") != nil {
		t.Error("битый JSON должен давать nil")
	}
}

// План 46 + одиночный выбор: конфиг «ОдинВыбор» доезжает до клиента флагом
// single. Диалог писался под подбор номенклатуры (много строк с количествами);
// там, где значение ровно одно, мультивыбор — лишний способ ошибиться.
func TestPicker_SingleChoiceFlag(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПодборНажатие()
	Колонки = Новый Массив;
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Номер", "Заявка №", "string"));
	Данные = Новый Массив;
	Данные.Добавить(Новый Структура("Идентификатор,Номер", "u-1", "ЗАЯ-000001"));
	Конфиг = Новый Структура("Заголовок,ОдинВыбор", "Выберите заявку", Истина);
	ПоказатьПодбор(Данные, Колонки, Конфиг);
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton,
			Name: "КнопкаПодбор",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "ПодборНажатие",
			},
		},
	})

	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnClick))

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData != nil; body=%s", rec.Body.String())
	}
	if !resp.PickerData.Config.Single {
		t.Errorf("ждали single=true в конфиге диалога, получено %+v", resp.PickerData.Config)
	}
	if resp.PickerData.Config.Title != "Выберите заявку" {
		t.Errorf("заголовок = %q", resp.PickerData.Config.Title)
	}
}

// Без «ОдинВыбор» диалог остаётся прежним, мультивыборным.
func TestPicker_MultiChoiceStaysDefault(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПодборНажатие()
	Колонки = Новый Массив;
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Номер", "Заявка №", "string"));
	Данные = Новый Массив;
	Данные.Добавить(Новый Структура("Идентификатор,Номер", "u-1", "ЗАЯ-000001"));
	ПоказатьПодбор(Данные, Колонки, Новый Структура("Заголовок", "Подбор"));
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{
			Kind: metadata.FormElementButton,
			Name: "КнопкаПодбор",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "ПодборНажатие",
			},
		},
	})

	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnClick))

	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.PickerData == nil {
		t.Fatal("ждали pickerData != nil")
	}
	if resp.PickerData.Config.Single {
		t.Error("без «ОдинВыбор» диалог обязан остаться мультивыборным")
	}
}
