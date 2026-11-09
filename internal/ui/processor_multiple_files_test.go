package ui

// Пачка файлов в обработку (касса: «Загрузка сканов в заявки» — заявка
// определяется по имени файла). Параметр type: binary с multiple: true отдаёт
// DSL Массив Структур {Путь, ИмяФайла, Размер, ТипMIME}; одиночный binary
// по-прежнему отдаёт путь, но копия лежит под исходным именем — его читает
// Новый Файл(Путь).Имя. Проверяется публичный путь: multipart POST запуска и
// события управляемой формы, разметка GET.

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
)

const multipleFilesProgram = `
Процедура Выполнить()
	Если ТипЗнч(Параметры.Сканы) = Тип("Массив") Тогда
		Сообщить("файлов: " + Формат(Параметры.Сканы.Количество(), "ЧГ=0"));
		Для Каждого Ф Из Параметры.Сканы Цикл
			Сообщить("скан: " + Ф.ИмяФайла + "|" + Формат(Ф.Размер, "ЧГ=0") + "|" + Ф.ТипMIME + "|" + Новый Файл(Ф.Путь).Имя);
		КонецЦикла;
	КонецЕсли;
	Если ЗначениеЗаполнено(Параметры.Акт) Тогда
		Сообщить("акт: " + Новый Файл(Параметры.Акт).Имя);
	КонецЕсли;
КонецПроцедуры
`

type uploadPart struct {
	field, name, mime string
	data              []byte
}

func multipartBody(t *testing.T, values url.Values, parts []uploadPart) (*bytes.Buffer, string) {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	for k, vs := range values {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, p := range parts {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="`+p.field+`"; filename="`+p.name+`"`)
		if p.mime != "" {
			h.Set("Content-Type", p.mime)
		}
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(p.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return body, mw.FormDataContentType()
}

func multipleFilesProcessor() *processor.Processor {
	return &processor.Processor{
		Name:  "ЗагрузкаСканов",
		Title: "Загрузка сканов",
		Params: []processor.Param{
			{Name: "Сканы", Type: "binary", Multiple: true},
			{Name: "Акт", Type: "binary"},
		},
	}
}

func TestProcessorMultipleFiles_RunGivesArrayAndOriginalNames(t *testing.T) {
	s, _ := newSubmitTestServer(t, nil)
	proc := multipleFilesProcessor()
	s.reg.LoadProcessors([]*processor.Processor{proc})
	s.reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{proc.Name: mustParse(t, multipleFilesProgram)}})
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	defer ts.Close()

	body, contentType := multipartBody(t, nil, []uploadPart{
		{field: "Сканы", name: "Заявка 123.jpg", mime: "image/jpeg", data: []byte("jpeg-bytes")},
		{field: "Сканы", name: "Заявка 456.pdf", mime: "application/pdf", data: []byte("%PDF-1.4 x")},
		{field: "Акт", name: `C:\fakepath\Акт приёма: 7.png`, mime: "image/png", data: []byte("png")},
	})
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/ui/processor/"+url.PathEscape(proc.Name), body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	html := string(page)
	for _, want := range []string{
		"файлов: 2",
		"скан: Заявка 123.jpg|10|image/jpeg|Заявка 123.jpg",
		"скан: Заявка 456.pdf|10|application/pdf|Заявка 456.pdf",
		// Одиночный binary: путь к копии под исходным именем; недопустимый в
		// имени файла символ заменён, каталог браузера отброшен.
		"акт: Акт приёма_ 7.png",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("нет %q в ответе (status %d):\n%.2000s", want, resp.StatusCode, html)
		}
	}
}

func TestProcessorMultipleFiles_ManagedFormEventReceivesFiles(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеСканы", DataPath: "Объект.Сканы", Type: "file"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Загрузить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Загрузить"}},
	)
	program := mustParse(t, `
Процедура Загрузить()
	Для Каждого Ф Из Объект.Сканы Цикл
		Сообщить(Ф.ИмяФайла + "|" + Формат(Ф.Размер, "ЧГ=0"));
	КонецЦикла;
КонецПроцедуры
`)
	form.ProgramAST = program
	proc := &processor.Processor{
		Name:   "СканыФорма",
		Params: []processor.Param{{Name: "Сканы", Type: "binary", Multiple: true}},
		Forms:  []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)

	body, contentType := multipartBody(t, processorClickBody("Загрузить"), []uploadPart{
		{field: "Сканы", name: "Заявка 1.jpg", data: []byte("12345")},
		{field: "Сканы", name: "Заявка 2.jpg", data: []byte("123")},
	})
	rec := postProcessorFormEventExecution(t, srv, proc.Name, contentType, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || strings.Join(resp.Messages, ";") != "Заявка 1.jpg|5;Заявка 2.jpg|3" {
		t.Fatalf("событие формы не получило файлы: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}

	// Событие без выбранных файлов (urlencoded, как шлёт obFire без файлов)
	// не ломается: параметр просто не передан.
	plain := processorClickBody("Загрузить")
	plainRec := postProcessorFormEventExecution(t, srv, proc.Name, "application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(plain.Encode()))
	if plainResp := decodeFormEventResponse(t, plainRec.Body.Bytes()); plainResp.Error != "" && strings.Contains(plainResp.Error, "obFire") {
		t.Fatalf("событие без файлов отклонено: %q", plainResp.Error)
	}
}

func TestProcessorMultipleFiles_ManagedFormRendersRealFileInput(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеСканы", DataPath: "Объект.Сканы", Type: "file"},
	)
	proc := &processor.Processor{
		Name:   "СканыРазметка",
		Params: []processor.Param{{Name: "Сканы", Type: "binary", Multiple: true}},
		Forms:  []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	router := chi.NewRouter()
	router.Get("/ui/processor/{name}", srv.processorForm)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/processor/"+url.PathEscape(proc.Name), nil))
	html := rec.Body.String()
	if !strings.Contains(html, `type="file" name="Сканы" data-ob-binary-file="ПолеСканы" multiple`) {
		k := strings.Index(html, "ПолеСканы")
		if k < 0 {
			k = 0
		}
		t.Fatalf("status %d; нет поля файла с multiple:\n%.1500s", rec.Code, html[k:])
	}
	if strings.Contains(html, `id="file-path-ПолеСканы"`) {
		t.Fatal("двоичный параметр не должен рисоваться текстовым путём")
	}
}
