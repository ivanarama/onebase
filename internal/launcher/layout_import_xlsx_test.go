package launcher

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/printform"
)

// План 155: эндпоинт импорта макета из бланка Excel.

// blankXLSX — бланк накладной: шапка с тегами документа, шапка таблицы, строка
// с тегами табличной части «Товары» (она и должна стать repeat-областью).
func blankXLSX(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sh := f.GetSheetName(0)

	set := func(cell, val string) {
		t.Helper()
		if err := f.SetCellValue(sh, cell, val); err != nil {
			t.Fatalf("SetCellValue %s: %v", cell, err)
		}
	}
	set("A1", "Накладная № {{Номер}} от {{Дата | date}}")
	set("A2", "№")
	set("B2", "Номенклатура")
	set("C2", "Сумма")
	set("A3", "{{@row}}")
	set("B3", "{{Товары.Номенклатура}}")
	set("C3", "{{Товары.Сумма | number:2}}")
	set("B4", "Итого:")
	set("C4", "{{Итог.Товары.Сумма | number:2}}")

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("WriteToBuffer: %v", err)
	}
	return buf.Bytes()
}

// postImportXLSX собирает multipart-запрос и вызывает хендлер.
func postImportXLSX(t *testing.T, h *handler, b *Base, name, doc, sheetName string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for field, val := range map[string]string{"name": name, "document": doc, "sheet": sheetName} {
		if val != "" {
			_ = mw.WriteField(field, val)
		}
	}
	if data != nil {
		fw, err := mw.CreateFormFile("file", "бланк.xlsx")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req := httptest.NewRequest(http.MethodPost, "/bases/"+b.ID+"/configurator/layout/import-xlsx", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.configuratorImportXLSXLayout(rec, req)
	return rec
}

func TestImportXLSX_HappyPath(t *testing.T) {
	h, b, dir := newLayoutTestBase(t)
	original := blankXLSX(t)
	rec := postImportXLSX(t, h, b, "ИзExcelНакладная", "Реализация", "", original)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело %s", rec.Code, truncate(rec.Body.String(), 400))
	}

	data, err := os.ReadFile(filepath.Join(dir, "printforms", "ИзExcelНакладная.layout.yaml"))
	if err != nil {
		t.Fatalf("файл макета не создан: %v", err)
	}
	parsed, err := printform.ParseLayoutBytes(data)
	if err != nil {
		t.Fatalf("созданный макет не парсится: %v", err)
	}
	if parsed.Document != "Реализация" {
		t.Errorf("document = %q, ожидалось «Реализация»", parsed.Document)
	}
	template, err := os.ReadFile(filepath.Join(dir, "printforms", "ИзExcelНакладная.template.xlsx"))
	if err != nil {
		t.Fatalf("исходный Excel-шаблон не сохранён: %v", err)
	}
	if !bytes.Equal(template, original) {
		t.Error("сохранённый Excel-шаблон отличается от загруженного")
	}

	// Главное в этом тесте — что состав табличных частей действительно доехал
	// из метаданных документа до импортёра: без него строка ТЧ не размножается,
	// и пользователь получает бланк с одной строкой товара.
	if parsed.Binding == nil || len(parsed.Binding.Repeat) != 1 {
		t.Fatalf("repeat не собран: %+v", parsed.Binding)
	}
	if parsed.Binding.Repeat[0].Source != "Товары" {
		t.Errorf("repeat.source = %q, ожидалось «Товары»", parsed.Binding.Repeat[0].Source)
	}

	// В дереве конфигуратора появился узел макета.
	if !strings.Contains(rec.Body.String(), `data-id="mkt-ИзExcelНакладная"`) {
		t.Error("после импорта в дереве нет узла mkt-ИзExcelНакладная")
	}
}

// Импорт не должен молча терять оформление: то, что не перенесено, показывается
// в баннере результата.
func TestImportXLSX_ReportsWarnings(t *testing.T) {
	h, b, _ := newLayoutTestBase(t)
	// Бланк без тегов табличной части — импортёр обязан предупредить, что
	// строки таблицы не размножатся.
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetCellValue(f.GetSheetName(0), "A1", "Накладная № {{Номер}}"); err != nil {
		t.Fatal(err)
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}

	rec := postImportXLSX(t, h, b, "БезСтрок", "Реализация", "", buf.Bytes())
	if !strings.Contains(rec.Body.String(), "Строк табличной части не найдено") {
		t.Errorf("предупреждение не показано, тело:\n%s", truncate(rec.Body.String(), 600))
	}
}

func TestImportXLSX_MissingDocument(t *testing.T) {
	h, b, dir := newLayoutTestBase(t)
	rec := postImportXLSX(t, h, b, "БезДокумента", "", "", blankXLSX(t))
	if !strings.Contains(rec.Body.String(), "выберите документ") && !strings.Contains(rec.Body.String(), "select a document") {
		t.Errorf("ожидалось сообщение про выбор документа, тело:\n%s", truncate(rec.Body.String(), 400))
	}
	if _, err := os.Stat(filepath.Join(dir, "printforms", "БезДокумента.layout.yaml")); err == nil {
		t.Error("макет без документа не должен создаваться")
	}
}

func TestImportXLSX_MissingName(t *testing.T) {
	h, b, _ := newLayoutTestBase(t)
	rec := postImportXLSX(t, h, b, "", "Реализация", "", blankXLSX(t))
	if !strings.Contains(rec.Body.String(), "обязательно") && !strings.Contains(rec.Body.String(), "required") {
		t.Errorf("ожидалось сообщение про обязательное имя, тело:\n%s", truncate(rec.Body.String(), 400))
	}
}

func TestImportXLSX_MissingFile(t *testing.T) {
	h, b, _ := newLayoutTestBase(t)
	rec := postImportXLSX(t, h, b, "БезФайла", "Реализация", "", nil)
	if !strings.Contains(rec.Body.String(), "Excel") {
		t.Errorf("ожидалось сообщение про выбор файла Excel, тело:\n%s", truncate(rec.Body.String(), 400))
	}
}

func TestImportXLSX_BrokenFile(t *testing.T) {
	h, b, dir := newLayoutTestBase(t)
	rec := postImportXLSX(t, h, b, "Битый", "Реализация", "", []byte("это не книга Excel"))
	if !strings.Contains(rec.Body.String(), "прочитать файл Excel") && !strings.Contains(rec.Body.String(), "read the Excel") {
		t.Errorf("ожидалось сообщение о нечитаемом файле, тело:\n%s", truncate(rec.Body.String(), 400))
	}
	if _, err := os.Stat(filepath.Join(dir, "printforms", "Битый.layout.yaml")); err == nil {
		t.Error("из битого файла макет создаваться не должен")
	}
}

func TestImportXLSX_UnknownSheet(t *testing.T) {
	h, b, _ := newLayoutTestBase(t)
	rec := postImportXLSX(t, h, b, "ЧужойЛист", "Реализация", "Нетакого", blankXLSX(t))
	if !strings.Contains(rec.Body.String(), "не найден") && !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("ожидалось сообщение о ненайденном листе, тело:\n%s", truncate(rec.Body.String(), 400))
	}
}

// Существующий макет не перезаписывается — общая для всех импортов защита
// (writeLayoutFile).
func TestImportXLSX_DuplicateRefused(t *testing.T) {
	h, b, dir := newLayoutTestBase(t)
	if rec := postImportXLSX(t, h, b, "Дубль", "Реализация", "", blankXLSX(t)); rec.Code != http.StatusOK {
		t.Fatalf("первый импорт: код %d", rec.Code)
	}
	before, err := os.ReadFile(filepath.Join(dir, "printforms", "Дубль.layout.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	rec := postImportXLSX(t, h, b, "Дубль", "Реализация", "", blankXLSX(t))
	if !strings.Contains(rec.Body.String(), "уже существует") && !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("ожидалось сообщение о дубле, тело:\n%s", truncate(rec.Body.String(), 400))
	}
	after, err := os.ReadFile(filepath.Join(dir, "printforms", "Дубль.layout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("существующий макет был перезаписан")
	}
}

// Конфигурация в БД: макет пишется в _onebase_config тем же путём.
func TestImportXLSX_ConfigDB(t *testing.T) {
	h, b := newLayoutTestBaseDB(t)
	original := blankXLSX(t)
	rec := postImportXLSX(t, h, b, "ИзExcelБД", "Реализация", "", original)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело %s", rec.Code, truncate(rec.Body.String(), 400))
	}
	if !strings.Contains(rec.Body.String(), "ИзExcelБД") {
		t.Errorf("макет не появился в конфигураторе, тело:\n%s", truncate(rec.Body.String(), 400))
	}
	template, ok := configReadLayout(t, b, "printforms/ИзExcelБД.template.xlsx")
	if !ok {
		t.Fatal("исходный Excel-шаблон не записан в _onebase_config")
	}
	if !bytes.Equal(template, original) {
		t.Error("Excel-шаблон в _onebase_config отличается от загруженного")
	}
}
