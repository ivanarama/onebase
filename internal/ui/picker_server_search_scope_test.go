package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

const pickerSearchHandler = `
Процедура ПодборПоиск()
	Сообщить("[" + ПодборЗапрос + "]");
	Данные = Новый Массив;
	Если ПодборЗапрос = "гай" Тогда
		Данные.Добавить(Новый Структура("Идентификатор,Номенклатура", "u-2", "Гайка"));
	КонецЕсли;
	Колонки = Новый Массив;
	Колонки.Добавить(Новый Структура("Имя,Заголовок,Тип", "Номенклатура", "Товар", "string"));
	Конфиг = Новый Структура("Заголовок,ПоискНаСервере", "Подбор товаров", Истина);
	ПоказатьПодбор(Данные, Колонки, Конфиг);
КонецПроцедуры
`

// Серверный поиск обязан работать и в формах ОБРАБОТОК: у них отдельный
// обработчик события, который читал только _pick_result. Без ПодборЗапрос
// платформенный сценарий там молча не работал — обработчик видел <nil> вместо
// набранного, а ответ выглядел успешным.
func TestPicker_ServerSearchQueryReachesProcessorForm(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "КнопкаПодбор",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnSearch: "ПодборПоиск",
		},
	})
	form.ProgramAST = mustParse(t, pickerSearchHandler)
	proc := &processor.Processor{Name: "ПодборОбработка", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnSearch))
	body.Set("_pick_query", "гай")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("код %d, тело %s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.PickerData == nil {
		t.Fatalf("ждали pickerData != nil; тело=%s", rec.Body.String())
	}
	if len(resp.PickerData.Rows) != 1 || resp.PickerData.Rows[0].Data["Номенклатура"] != "Гайка" {
		t.Fatalf("ждали одну строку «Гайка», получили %+v", resp.PickerData.Rows)
	}
	if len(resp.Messages) == 0 || resp.Messages[0] != "[гай]" {
		t.Fatalf("обработчик обработки увидел ПодборЗапрос=%v, ждали «[гай]»", resp.Messages)
	}

	// Пустая строка поиска — такой же запрос, переменная обязана прийти пустой.
	body.Set("_pick_query", "")
	rec = postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp = decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.PickerData == nil || len(resp.PickerData.Rows) != 0 {
		t.Fatalf("на пустом запросе ждали диалог без строк, получили %+v", resp.PickerData)
	}
	if len(resp.Messages) == 0 || resp.Messages[0] != "[]" {
		t.Fatalf("на пустом запросе ПодборЗапрос=%v, ждали «[]»", resp.Messages)
	}
}

// Служебное поле не должно отнимать имя у легального параметра обработки:
// параметр _pick_query переезжает под префикс, а служебное значение доезжает.
func TestPicker_ServerSearchQuerySurvivesProcessorParamCollision(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "КнопкаПодбор",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnSearch: "ПодборПоиск",
		},
	})
	form.ProgramAST = mustParse(t, pickerSearchHandler)
	proc := &processor.Processor{
		Name:   "ПодборОбработкаКоллизия",
		Params: []processor.Param{{Name: "_pick_query", Type: "string"}},
		Forms:  []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	service := processorServiceFieldName(proc.Params, "_pick_query")
	if service == "_pick_query" {
		t.Fatalf("служебное поле не разведено с одноимённым параметром обработки")
	}
	body := url.Values{}
	body.Set("_element", "КнопкаПодбор")
	body.Set("_event", string(metadata.FormEventOnSearch))
	body.Set("_pick_query", "параметр обработки")
	body.Set(service, "гай")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("код %d, тело %s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if len(resp.Messages) == 0 || resp.Messages[0] != "[гай]" {
		t.Fatalf("ПодборЗапрос=%v, ждали «[гай]» из служебного поля", resp.Messages)
	}
}

// Диалог открывает не только кнопка, но и команда автопанели. Событие Поиск
// уходит той же цели, поэтому команда обязана его принимать — иначе
// fail-closed отказывает, а ai-guide обещает все три события.
func TestPicker_ServerSearchAllowedForFormCommand(t *testing.T) {
	allowed := metadata.BrowserFormCommandEvents()
	found := false
	for _, e := range allowed {
		if e == metadata.FormEventOnSearch {
			found = true
		}
	}
	if !found {
		t.Fatalf("команда формы не принимает Поиск: %v", allowed)
	}

	srv, ent := setupManagedEventsServer(t, pickerSearchHandler, nil, nil)
	ent.Forms[0].Commands = []*metadata.FormCommand{{Name: "КомандаПодбор", Action: "ПодборПоиск"}}

	body := url.Values{}
	body.Set("_element", "КомандаПодбор")
	body.Set("_event", string(metadata.FormEventOnSearch))
	body.Set("_pick_query", "гай")
	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.Error != "" {
		t.Fatalf("команда отвергла событие Поиск: %s", resp.Error)
	}
	if resp.PickerData == nil || len(resp.PickerData.Rows) != 1 {
		t.Fatalf("ждали диалог с одной строкой, получили %+v (ошибка %q)", resp.PickerData, resp.Error)
	}
	if len(resp.Messages) == 0 || resp.Messages[0] != "[гай]" {
		t.Fatalf("команда увидела ПодборЗапрос=%v, ждали «[гай]»", resp.Messages)
	}
}
