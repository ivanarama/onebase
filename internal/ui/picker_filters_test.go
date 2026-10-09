package ui

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Конфиг.Отборы — колонки с выпадающими отборами над списком подбора.
// Проверяем тем же путём, что и пользователь: событие формы → pickerData.
func pickerFiltersResponse(t *testing.T, filters string) formEventResponse {
	t.Helper()
	srv, ent := setupManagedEventsServer(t, `
Процедура ПодборНажатие()
	Колонки = Новый Массив;
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Мастер", "Мастер", "string"));
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Направление", "Направление", "string"));
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Филиал", "Филиал", "string"));
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип,Редактируемое", "Количество", "Кол-во", "number", Истина));
	Данные = Новый Массив;
	Данные.Добавить(Новый Структура("Идентификатор,Мастер,Направление,Филиал", "u-1", "Смирнов", "СМ", "МСК"));
	Конфиг = Новый Структура("Заголовок,ОдинВыбор", "Выбор мастера", Истина);
	`+filters+`
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
	return decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
}

func TestPicker_FiltersFromString(t *testing.T) {
	// Регистр и пробелы — как напишут руками; в ответ уходят имена колонок.
	resp := pickerFiltersResponse(t, `Конфиг.Вставить("Отборы", " направление , ФИЛИАЛ ");`)
	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData; error=%q", resp.Error)
	}
	if got, want := resp.PickerData.Config.Filters, []string{"Направление", "Филиал"}; !reflect.DeepEqual(got, want) {
		t.Errorf("filters = %v, ждали %v", got, want)
	}
}

func TestPicker_FiltersFromArray(t *testing.T) {
	resp := pickerFiltersResponse(t, `О = Новый Массив; О.Добавить("Филиал"); О.Добавить("Филиал"); Конфиг.Вставить("Отборы", О);`)
	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData; error=%q", resp.Error)
	}
	if got, want := resp.PickerData.Config.Filters, []string{"Филиал"}; !reflect.DeepEqual(got, want) {
		t.Errorf("filters = %v, ждали %v (повтор схлопывается)", got, want)
	}
}

func TestPicker_FiltersFromEmptyArray(t *testing.T) {
	resp := pickerFiltersResponse(t, `Конфиг.Вставить("Отборы", Новый Массив);`)
	if resp.PickerData == nil || resp.Error != "" {
		t.Fatalf("пустой Массив должен открыть подбор; error=%q", resp.Error)
	}
	if len(resp.PickerData.Config.Filters) != 0 {
		t.Errorf("пустой Массив создал отборы: %v", resp.PickerData.Config.Filters)
	}
}

func TestPicker_NoFiltersByDefault(t *testing.T) {
	resp := pickerFiltersResponse(t, "")
	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData; error=%q", resp.Error)
	}
	if len(resp.PickerData.Config.Filters) != 0 {
		t.Errorf("без Отборы диалог обязан остаться прежним, filters = %v", resp.PickerData.Config.Filters)
	}
}

// Опечатка в имени колонки не должна молча превращаться в «отбора нет».
func TestPicker_FiltersRejectUnknownAndEditableColumns(t *testing.T) {
	for _, tc := range []struct{ filters, want string }{
		{`Конфиг.Вставить("Отборы", "Направленне");`, "Направленне"},
		{`Конфиг.Вставить("Отборы", "Количество");`, "редактируемая"},
	} {
		resp := pickerFiltersResponse(t, tc.filters)
		if resp.PickerData != nil {
			t.Errorf("%s: диалог открылся с неверным отбором", tc.filters)
		}
		if !strings.Contains(resp.Error+strings.Join(resp.Messages, " "), tc.want) {
			t.Errorf("%s: в ошибке нет %q: error=%q messages=%v", tc.filters, tc.want, resp.Error, resp.Messages)
		}
	}
}
