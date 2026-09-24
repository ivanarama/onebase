package ui

// Импорт данных из Excel в справочник (#1470). Параметр обработки вида `binary`
// отдаёт DSL ПУТЬ к временной копии загруженного файла — один смысл аргумента,
// как у ПутьКВложению, — а ПрочитатьExcel читает по нему книгу. Прежний `file`
// приводил байты к тексту, и .xlpx как zip-архив доезжал до DSL испорченным.

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

const binaryImportProgram = `
Процедура Выполнить()
	Строки = ПрочитатьExcel(Параметры.Файл);
	Сообщить("путь: " + Параметры.Файл);
	Для Инд = 1 По Строки.Количество() - 1 Цикл
		Стр = Строки[Инд];
		Элемент = Справочники.Ученики.Создать();
		Элемент.Код = Стр[0];
		Элемент.Наименование = Стр[1];
		Элемент.Записать();
	КонецЦикла;
КонецПроцедуры
`

func binaryImportFixture(t *testing.T) (*Server, context.Context, *httptest.Server, *metadata.Entity) {
	t.Helper()
	cat := &metadata.Entity{
		Name: "Ученики",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	proc := &processor.Processor{
		Name:  "ЗагрузкаУчеников",
		Title: "Загрузка учеников",
		Params: []processor.Param{
			{Name: "Файл", Type: "binary", Label: "Файл Excel"},
		},
	}
	s.reg.LoadProcessors([]*processor.Processor{proc})
	s.reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{cat},
		Programs: map[string]*ast.Program{proc.Name: mustParse(t, binaryImportProgram)},
	})
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return s, ctx, ts, cat
}

// xlsxWithStudents — настоящая книга .xlsx, а не подделка: ведущий ноль в коде
// обязан дожить до справочника, а он переживает только текстовую ячейку.
func xlsxWithStudents(t *testing.T) []byte {
	t.Helper()
	data, err := excel.ExportList(
		[]string{"Код", "ФИО"},
		[][]any{
			{"007", "Иванов Иван"},
			{"012", "Петрова Анна"},
		},
	)
	if err != nil {
		t.Fatalf("сборка книги: %v", err)
	}
	return data
}

func postBinaryProcessor(t *testing.T, ts *httptest.Server, procName, fieldName, fileName string, data []byte) (int, string) {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart Close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/ui/processor/"+url.PathEscape(procName), body)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	return resp.StatusCode, buf.String()
}

func TestProcessorBinaryParam_ExcelImportCreatesCatalogRows(t *testing.T) {
	s, ctx, ts, cat := binaryImportFixture(t)

	code, page := postBinaryProcessor(t, ts, "ЗагрузкаУчеников", "Файл", "ученики.xlsx", xlsxWithStudents(t))
	if code != http.StatusOK {
		t.Fatalf("код %d: %s", code, page)
	}

	rows, err := s.store.List(ctx, cat.Name, cat, storage.ListParams{RowFilterEvaluated: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("в справочнике %d строк, ждали 2: %s", len(rows), page)
	}
	byCode := map[string]string{}
	for _, row := range rows {
		byCode[fmt.Sprintf("%v", row["Код"])] = fmt.Sprintf("%v", row["Наименование"])
	}
	// Ведущий ноль — главная проверка: он выживает только если значение ячейки
	// пришло текстом, а не было приведено к числу.
	if byCode["007"] != "Иванов Иван" || byCode["012"] != "Петрова Анна" {
		t.Fatalf("загружено %#v", byCode)
	}
}

// Временный файл живёт ровно прогон. Иначе каталог загрузок копил бы книги
// пользователей, и никто бы этого не заметил.
func TestProcessorBinaryParam_TempFileRemovedAfterRun(t *testing.T) {
	_, _, ts, _ := binaryImportFixture(t)

	code, page := postBinaryProcessor(t, ts, "ЗагрузкаУчеников", "Файл", "ученики.xlsx", xlsxWithStudents(t))
	if code != http.StatusOK {
		t.Fatalf("код %d: %s", code, page)
	}
	path := ""
	for _, line := range strings.Split(page, "\n") {
		if idx := strings.Index(line, "путь: "); idx >= 0 {
			rest := line[idx+len("путь: "):]
			if end := strings.IndexAny(rest, "<\r"); end >= 0 {
				rest = rest[:end]
			}
			path = strings.TrimSpace(rest)
			break
		}
	}
	if path == "" {
		t.Fatalf("обработка не сообщила путь временного файла: %s", page)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("временный файл %s остался после прогона (err=%v)", path, err)
	}
	if filepath.Ext(path) != ".xlsx" {
		t.Errorf("расширение временного файла потеряно: %s", path)
	}
}

// Managed obFire преобразует FormData в urlencoded и передаёт уже прочитанное
// браузером содержимое обычной строкой. Для zip-архива это порча, поэтому
// параметр обязан отказывать внятно, а не молча испортить файл.
func TestProcessorBinaryParam_ManagedPathRejectsBinary(t *testing.T) {
	_, _, ts, _ := binaryImportFixture(t)

	form := url.Values{"Файл": {"PK\x03\x04 испорченный zip"}}
	req, err := http.NewRequest(http.MethodPost,
		ts.URL+"/ui/processor/"+url.PathEscape("ЗагрузкаУчеников"), strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	page := buf.String()
	if resp.StatusCode == http.StatusOK && !strings.Contains(page, "двоичный файл передать нельзя") {
		t.Fatalf("двоичный параметр принят текстом: код %d, тело %s", resp.StatusCode, page)
	}
	if !strings.Contains(page, "двоичный файл передать нельзя") {
		t.Fatalf("нет внятного отказа: код %d, тело %s", resp.StatusCode, page)
	}
}

// Отказ binary-параметра — ключ i18n: пользователь английского интерфейса
// обязан получить английский текст. Тест бьёт в настоящий managed-маршрут
// (/form-event требует managed-форму обработки) со штатным словарём и
// Accept-Language: en (круг 3 #1470).
func TestProcessorBinaryParam_RefusalLocalizedOnFormEvent(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Ученики",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{cat})
	form := &metadata.FormModule{
		Name:       "Форма",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Name: "Файл", Kind: metadata.FormElementField, DataPath: "Файл",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "Отказ"}},
		},
	}
	proc := &processor.Processor{
		Name:  "ЗагрузкаУчеников",
		Title: "Загрузка учеников",
		Params: []processor.Param{
			{Name: "Файл", Type: "binary", Label: "Файл Excel"},
		},
		Forms: []*metadata.FormModule{form},
	}
	s.reg.LoadProcessors([]*processor.Processor{proc})
	s.reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{cat},
		Programs: map[string]*ast.Program{proc.Name: mustParse(t, binaryImportProgram)},
	})
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatalf("загрузка словаря: %v", err)
	}
	s.cfg.Bundle = bundle

	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	formValues := url.Values{
		"Файл":     {"PK" + "\x03\x04" + " испорченный zip"},
		"_fc_Файл": {"PK" + "\x03\x04" + " испорченный zip"},
		"_element": {"Файл"},
		"_event":   {string(metadata.FormEventOnChange)},
	}
	req, err := http.NewRequest(http.MethodPost,
		ts.URL+"/ui/processor/"+url.PathEscape("ЗагрузкаУчеников")+"/form-event",
		strings.NewReader(formValues.Encode()))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("Accept-Language", "en")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400: %s", resp.StatusCode, page)
	}
	if !strings.Contains(string(page), "a binary file cannot be passed through the managed form") {
		t.Fatalf("отказ не переведён на английский: код %d, тело %s", resp.StatusCode, page)
	}
}
