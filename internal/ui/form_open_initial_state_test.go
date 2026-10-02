package ui

// Заполнение формы в ПриОткрытии — её исходное состояние, а не правка
// пользователя.
//
// С #1559 ответ на событие формы сообщает, изменил ли обработчик данные (dirty):
// несохранённая правка команды не должна молча теряться при закрытии. Особого
// случая для открытия не было, и всё, что форма заполняет сама — умолчания
// нового документа, дата и склад рабочего места, — считалось несохранённой
// правкой. Форма открывалась со звёздочкой в заголовке, а уход со страницы и
// «Закрыть» спрашивали о сохранении, хотя пользователь ничего не трогал. В 1С
// программное изменение данных формы модифицированность не ставит. Найдено в
// браузере на рабочем месте продаж торговой конфигурации PuT.

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

const openFillSource = `
Процедура ПриОткрытииФормы()
	Если НЕ ЗначениеЗаполнено(Объект.Наименование) Тогда
		Объект.Наименование = "по умолчанию";
	КонецЕсли;
КонецПроцедуры

Процедура КомандаНажатие()
	Объект.Наименование = "командой";
КонецПроцедуры
`

func TestFormOpenFillIsInitialState_Object(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, openFillSource,
		map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытииФормы"},
		[]*metadata.FormElement{{
			Kind: metadata.FormElementButton, Name: "Команда",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КомандаНажатие"},
		}})

	// Новая форма: _id нет, обработчик открытия подставляет умолчание.
	opened := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
		"_element": {""}, "_event": {string(metadata.FormEventOnOpen)}, "_kind": {"object"},
		"Наименование": {""},
	}).Body.Bytes())
	if !opened.OK || opened.Values["Наименование"] != "по умолчанию" {
		t.Fatalf("обработчик открытия не отработал: %+v", opened)
	}
	if opened.Dirty == nil || *opened.Dirty {
		t.Errorf("заполнение при открытии сделало форму «изменённой»: dirty=%v", opened.Dirty)
	}

	// Команда меняет данные — это по-прежнему несохранённая правка.
	clicked := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
		"_element": {"Команда"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"},
		"Наименование": {"по умолчанию"},
	}).Body.Bytes())
	if !clicked.OK || clicked.Dirty == nil || !*clicked.Dirty {
		t.Errorf("правка командой перестала помечать форму изменённой: %+v", clicked)
	}
}

func TestFormOpenFillIsInitialState_Processor(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Команда",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КомандаНажатие"},
	})
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытииФормы"}
	form.ProgramAST = mustParse(t, strings.ReplaceAll(openFillSource, "Наименование", "Склад"))
	proc := &processor.Processor{
		Name:   "РабочееМесто",
		Params: []processor.Param{{Name: "Склад", Type: "string"}},
		Forms:  []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	post := func(body url.Values) formEventResponse {
		t.Helper()
		rec := postProcessorFormEventExecution(t, srv, proc.Name, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
		return decodeFormEventResponse(t, rec.Body.Bytes())
	}

	opened := post(url.Values{"_element": {""}, "_event": {string(metadata.FormEventOnOpen)}, "Склад": {""}})
	if !opened.OK || opened.Values["Склад"] != "по умолчанию" {
		t.Fatalf("обработчик открытия не отработал: %+v", opened)
	}
	if opened.Dirty == nil || *opened.Dirty {
		t.Errorf("заполнение при открытии сделало форму обработки «изменённой»: dirty=%v", opened.Dirty)
	}

	clicked := post(url.Values{"_element": {"Команда"}, "_event": {string(metadata.FormEventOnClick)}, "Склад": {"по умолчанию"}})
	if !clicked.OK || clicked.Dirty == nil || !*clicked.Dirty {
		t.Errorf("правка командой перестала помечать форму обработки изменённой: %+v", clicked)
	}
}
