package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/storage"
)

// План 64, этап 5b, блок C/D: тесты эндпоинта предпросмотра макета.

// postPreview шлёт JSON-тело {yaml, entity} на эндпоинт предпросмотра.
func postPreview(t *testing.T, h *handler, b *Base, body, format string) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	url := "/bases/" + b.ID + "/configurator/layout/preview"
	if format == "pdf" {
		url += "?format=pdf"
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.configuratorLayoutPreview(rec, req)
	return rec
}

const previewLayoutYAML = `name: ТоварнаяНакладная
document: Реализация
areas:
  - name: Заголовок
    rows:
      - cells:
          - parameter: Номер
  - name: Строка
    rows:
      - cells:
          - parameter: Номенклатура
          - parameter: Сумма
binding:
  repeat:
    - area: Строка
      source: Товары
`

// seedRealizationDoc мигрирует сущности проекта в SQLite, вставляет одну запись
// документа Реализация с двумя строками ТЧ Товары и привязывает базу к файлу БД.
func seedRealizationDoc(t *testing.T, h *handler, b *Base, dir string) {
	t.Helper()
	dbPath := filepath.Join(dir, "data.db")
	ctx := context.Background()

	proj, err := h.loadProjectFor(ctx, b)
	if err != nil {
		t.Fatalf("loadProjectFor: %v", err)
	}
	defer proj.Close()

	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var ent = h.findEntity(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), b, "Реализация")
	if ent == nil {
		t.Fatal("сущность Реализация не найдена")
		return
	}

	id := uuid.New()
	if err := db.Upsert(ctx, "Реализация", id, map[string]any{"Номер": "ПРОБА-777", "Дата": "2025-01-01"}, ent); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	tp := ent.TableParts[0]
	rows := []map[string]any{
		{"Номенклатура": "Молоко", "Сумма": 150},
		{"Номенклатура": "Хлеб", "Сумма": 42},
	}
	if err := db.UpsertTablePartRows(ctx, "Реализация", tp.Name, id, rows, tp); err != nil {
		t.Fatalf("UpsertTablePartRows: %v", err)
	}

	// Перенацеливаем базу на созданный SQLite-файл и сохраняем (Store.Get читает
	// из файла — без Update изменения не дойдут до хендлера).
	b.DBType = "sqlite"
	b.DBPath = dbPath
	if err := h.store.Update(b); err != nil {
		t.Fatalf("Update base: %v", err)
	}
}

// Валидный YAML + сущность с данными → HTML содержит реальные значения.
func TestLayoutPreview_RealData(t *testing.T) {
	h, b, dir := newLayoutTestBase(t)
	seedRealizationDoc(t, h, b, dir)

	rec := postPreview(t, h, b, `{"yaml":`+jsonStr(previewLayoutYAML)+`,"entity":"Реализация"}`, "html")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"ПРОБА-777", "Молоко", "Хлеб", "150", "42"} {
		if !strings.Contains(body, want) {
			t.Errorf("HTML предпросмотра не содержит %q\n%s", want, body)
		}
	}
}

// Без данных (БД пуста/недоступна) → синтетика: имена полей как значения.
func TestLayoutPreview_Synthetic(t *testing.T) {
	h, b, _ := newLayoutTestBase(t)
	// базу на SQLite-файл НЕ перенацеливаем → List вернёт ошибку → синтетика.

	rec := postPreview(t, h, b, `{"yaml":`+jsonStr(previewLayoutYAML)+`,"entity":"Реализация"}`, "html")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Номер документа — синтетический «000000001», поля ТЧ — «<Имя> N».
	for _, want := range []string{"000000001", "Номенклатура 1", "Номенклатура 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("синтетический HTML не содержит %q\n%s", want, body)
		}
	}
}

// Битый YAML → 400.
func TestLayoutPreview_BadYAML(t *testing.T) {
	h, b, _ := newLayoutTestBase(t)
	rec := postPreview(t, h, b, `{"yaml":"areas: [::: not yaml","entity":"Реализация"}`, "html")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получено %d, тело: %s", rec.Code, rec.Body.String())
	}
}

// Пустой YAML (нет областей) → 400.
func TestLayoutPreview_NoAreas(t *testing.T) {
	h, b, _ := newLayoutTestBase(t)
	rec := postPreview(t, h, b, `{"yaml":"name: x\n","entity":"Реализация"}`, "html")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получено %d", rec.Code)
	}
}

// PDF-формат → application/pdf с сигнатурой %PDF.
func TestLayoutPreview_PDF(t *testing.T) {
	h, b, dir := newLayoutTestBase(t)
	seedRealizationDoc(t, h, b, dir)

	rec := postPreview(t, h, b, `{"yaml":`+jsonStr(previewLayoutYAML)+`,"entity":"Реализация"}`, "pdf")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF") {
		t.Errorf("тело не начинается с %%PDF: %.16q", rec.Body.String())
	}
}

// jsonStr заключает строку в JSON-литерал (экранирование переводов строк и кавычек).
func jsonStr(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			b = append(b, string(r)...)
		}
	}
	b = append(b, '"')
	return string(b)
}
