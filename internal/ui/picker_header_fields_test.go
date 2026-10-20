package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// Конфиг.ПоляШапки — поля отбора в шапке окна подбора, как «Список
// номенклатуры» 1С. Проверяем путём пользователя: событие формы → pickerData,
// значения полей шапки → переменная ПодборПоля обработчика.
const pickerHeaderHandler = `
Процедура ПодборНажатие()
	Колонки = Новый Массив;
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Номенклатура", "Товар", "string"));
	Данные = Новый Массив;
	Данные.Добавить(Новый Структура("Идентификатор,Номенклатура", "u-1", "Гайка"));
	Бренды = Новый Массив;
	Бренды.Добавить(Новый Структура("Значение,Представление", "b-1", "ARISTON"));
	Бренды.Добавить("BOSCH");
	Поля = Новый Массив;
	Поля.Добавить(Новый Структура("Имя,Заголовок,Тип,Значение,Варианты", "Бренд", "Бренд", "Список", "b-1", Бренды));
	Поля.Добавить(Новый Структура("Имя,Заголовок,Значение,Ширина", "Артикул", "Артикул", "C005", 120));
	Поля.Добавить(Новый Структура("Имя,Заголовок,Тип,Значение", "СкрыватьНулевые", "Скрывать нулевые", "Булево", Истина));
	Конфиг = Новый Структура("Заголовок,ПоляШапки,БезВыбора", "Список номенклатуры", Поля, Истина);
	ПоказатьПодбор(Данные, Колонки, Конфиг);
КонецПроцедуры

Процедура ПодборПоиск()
	Сообщить("[" + ПодборПоля.Бренд + "|" + ПодборПоля.артикул + "|" + ПодборПоля.СкрыватьНулевые + "]");
КонецПроцедуры
`

func pickerHeaderButton() *metadata.FormElement {
	return &metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "КнопкаПодбор",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnClick:  "ПодборНажатие",
			metadata.FormEventOnSearch: "ПодборПоиск",
		},
	}
}

func TestPicker_HeaderFieldsConfig(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, pickerHeaderHandler, nil, []*metadata.FormElement{pickerHeaderButton()})
	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnClick))
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData; error=%q", resp.Error)
	}
	cfg := resp.PickerData.Config
	if !cfg.NoSelect {
		t.Errorf("БезВыбора не дошло до окна")
	}
	if len(cfg.HeaderFields) != 3 {
		t.Fatalf("полей шапки %d, ждали 3: %+v", len(cfg.HeaderFields), cfg.HeaderFields)
	}
	brand, art, zero := cfg.HeaderFields[0], cfg.HeaderFields[1], cfg.HeaderFields[2]
	if brand.Type != "list" || brand.Value != "b-1" || len(brand.Options) != 2 ||
		brand.Options[0] != (pickerHeaderOption{Value: "b-1", Title: "ARISTON"}) ||
		brand.Options[1] != (pickerHeaderOption{Value: "BOSCH", Title: "BOSCH"}) {
		t.Errorf("поле-список: %+v", brand)
	}
	if art.Type != "string" || art.Value != "C005" || art.Width != 120 {
		t.Errorf("поле-строка (тип по умолчанию): %+v", art)
	}
	if zero.Type != "bool" || zero.Value != true {
		t.Errorf("поле-флажок: %+v", zero)
	}
}

func TestPicker_HeaderFieldsInvalid(t *testing.T) {
	for _, tc := range []struct{ fields, want string }{
		{`Поля.Добавить(Новый Структура("Заголовок", "Без имени"));`, "не задано Имя"},
		{`Поля.Добавить(Новый Структура("Имя", "Бренд")); Поля.Добавить(Новый Структура("Имя", "бренд"));`, "дважды"},
		{`Поля.Добавить(Новый Структура("Имя,Тип", "Бренд", "Дата"));`, "неизвестный Тип"},
	} {
		resp := pickerFiltersResponse(t, `Поля = Новый Массив; `+tc.fields+` Конфиг.Вставить("ПоляШапки", Поля);`)
		if resp.PickerData != nil || !strings.Contains(resp.Error, tc.want) {
			t.Errorf("%s: ждали ошибку «%s», получили error=%q", tc.fields, tc.want, resp.Error)
		}
	}
}

// Значения полей шапки приходят обработчику Поиск переменной ПодборПоля,
// имена — без учёта регистра.
func TestPicker_HeaderFieldsReachSearchHandler(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, pickerHeaderHandler, nil, []*metadata.FormElement{pickerHeaderButton()})
	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnSearch))
	body.Set("_pick_query", "")
	body.Set("_pick_fields", `{"Бренд":"b-2","Артикул":"X1","СкрыватьНулевые":false}`)
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.Error != "" || len(resp.Messages) == 0 || resp.Messages[0] != "[b-2|X1|false]" {
		t.Fatalf("ПодборПоля в обработчике: messages=%v error=%q", resp.Messages, resp.Error)
	}
}

// То же в форме обработки: у неё свой разбор служебных полей.
func TestPicker_HeaderFieldsReachProcessorSearchHandler(t *testing.T) {
	form := processorExecutionForm(pickerHeaderButton())
	form.ProgramAST = mustParse(t, pickerHeaderHandler)
	proc := &processor.Processor{Name: "СписокНоменклатуры", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnSearch))
	body.Set("_pick_query", "")
	body.Set("_pick_fields", `{"Бренд":"b-1","Артикул":"","СкрыватьНулевые":true}`)
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.Error != "" || len(resp.Messages) == 0 || resp.Messages[0] != "[b-1||true]" {
		t.Fatalf("ПодборПоля в обработке: messages=%v error=%q", resp.Messages, resp.Error)
	}
}
