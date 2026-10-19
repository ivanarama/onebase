package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// ПриСменеСтраницы: набор страниц шлёт событие с именем открытой страницы,
// обработчик получает его в ТекущаяСтраница. Имя приходит из браузера —
// принимается только страница того же набора.

const pageChangeProgram = `
Процедура ПанельПриСменеСтраницы()
	Сообщить("Страница: " + ТекущаяСтраница);
КонецПроцедуры
`

func pageChangeElements() []*metadata.FormElement {
	return []*metadata.FormElement{
		{
			Kind: metadata.FormElementPages,
			Name: "Панель",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnPageChange: "ПанельПриСменеСтраницы",
			},
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementPage, Name: "СтрПервая"},
				{Kind: metadata.FormElementPage, Name: "СтрДеньги"},
			},
		},
	}
}

func pageChangeBody(page string) url.Values {
	body := url.Values{}
	body.Set("_element", "Панель")
	body.Set("_event", string(metadata.FormEventOnPageChange))
	body.Set("_page", page)
	return body
}

func TestPageChange_HandlerReceivesCurrentPage(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, pageChangeProgram, nil, pageChangeElements())
	rec := executeFormEvent(t, srv, ent, pageChangeBody("СтрДеньги"))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if !strings.Contains(strings.Join(resp.Messages, "\n"), "Страница: СтрДеньги") {
		t.Fatalf("обработчик не получил ТекущаяСтраница: %v", resp.Messages)
	}
}

func TestPageChange_ForeignPageFailsClosed(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, pageChangeProgram, nil, pageChangeElements())
	for _, page := range []string{"", "Панель", "ВыдуманнаяСтраница"} {
		rec := executeFormEvent(t, srv, ent, pageChangeBody(page))
		resp := decodeFormEventResponse(t, rec.Body.Bytes())
		if resp.OK {
			t.Fatalf("страница %q не из набора, а событие принято: %v", page, resp.Messages)
		}
		if !strings.Contains(resp.Error, "не входит в набор страниц") {
			t.Fatalf("страница %q: error=%q", page, resp.Error)
		}
	}
}

func TestPageChange_PageItselfDoesNotSend(t *testing.T) {
	elements := pageChangeElements()
	elements[0].Children[1].Handlers = map[metadata.FormEventType]string{
		metadata.FormEventOnPageChange: "ПанельПриСменеСтраницы",
	}
	srv, ent := setupManagedEventsServer(t, pageChangeProgram, nil, elements)
	body := pageChangeBody("СтрДеньги")
	body.Set("_element", "СтрДеньги")
	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK {
		t.Fatalf("страница сама отправила ПриСменеСтраницы: %v", resp.Messages)
	}
}

func TestPageChange_ProcessorForm(t *testing.T) {
	form := processorExecutionForm(pageChangeElements()...)
	form.ProgramAST = mustParse(t, pageChangeProgram)
	proc := &processor.Processor{Name: "СменаСтраницыОбработки", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	body := processorClickBody("Панель")
	body.Set("_event", string(metadata.FormEventOnPageChange))
	body.Set("_page", "СтрПервая")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if !strings.Contains(strings.Join(resp.Messages, "\n"), "Страница: СтрПервая") {
		t.Fatalf("обработчик формы обработки не получил ТекущаяСтраница: %v", resp.Messages)
	}

	body.Set("_page", "Чужая")
	rec = postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp = decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "не входит в набор страниц") {
		t.Fatalf("чужая страница в форме обработки: ok=%v error=%q", resp.OK, resp.Error)
	}
}
