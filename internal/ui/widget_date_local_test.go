package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Колонка list-виджета с format: date показывает момент в МЕСТНОМ времени.
// SQLite отдаёт дату строкой в UTC («2026-09-29T17:35:00Z»), и виджет печатал
// её как есть — задача, заведённая в 20:35 по Москве, стояла в списке с 17:35,
// хотя та же дата в форме и в отчёте показывалась верно (#1077).
func TestWidgetListDateColumnInLocalTime(t *testing.T) {
	// Зона фиксируется: на UTC-раннере без этого проверять было бы нечего.
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "widget-date.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	задача := &metadata.Entity{
		Name: "ЗадачаВремя", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Тема", Type: metadata.FieldTypeString},
			{Name: "Момент", Type: metadata.FieldTypeDate},
		},
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача}})
	reg.LoadWidgets([]*metadata.Widget{{
		Name: "ЗадачиВремя", Type: metadata.WidgetTypeList, Title: "Задачи",
		Query:   "ВЫБРАТЬ Тема, Момент ИЗ Документ.ЗадачаВремя УПОРЯДОЧИТЬ ПО Тема",
		Columns: []metadata.WidgetColumn{{Field: "Тема"}, {Field: "Момент", Format: "date"}},
	}})
	if err := db.Migrate(ctx, []*metadata.Entity{задача}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for id, row := range map[string]map[string]any{
		// 17:35 UTC = 20:35 по Москве.
		"79f4d98a-3ce0-4da4-82ea-e9c8686e804f": {"Тема": "А-момент", "Момент": time.Date(2026, 9, 29, 17, 35, 0, 0, time.UTC)},
		// Местная полночь 29.09 — это 28.09 21:00 UTC: показывается одной датой.
		"a8b64d2d-d422-490a-9e4e-092eefc47a40": {"Тема": "Б-дата", "Момент": time.Date(2026, 9, 29, 0, 0, 0, 0, time.Local)},
	} {
		if err := db.Upsert(ctx, задача.Name, mustUUID(t, id), row, задача); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)
	req := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+urlEscape("ЗадачиВремя"), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Ответ — JSON с разметкой в поле html: сверяем уже разобранную разметку.
	var payload struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ответ виджета не JSON: %v\n%s", err, rec.Body.String())
	}
	body := payload.HTML
	if !strings.Contains(body, "<td>29.09.2026 20:35</td>") || strings.Contains(body, "17:35") {
		t.Fatalf("момент показан не в местном времени (ждали 29.09.2026 20:35): %s", body)
	}
	if !strings.Contains(body, "<td>29.09.2026</td>") || strings.Contains(body, "28.09.2026") {
		t.Fatalf("местная полночь должна показываться датой 29.09.2026 без времени: %s", body)
	}
}

// Тот же перевод для значения time.Time — так отдаёт дату PostgreSQL (pgx,
// в зоне процесса или UTC).
func TestWidgetCellDateTimeValueInLocalTime(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	got := widgetCell(map[string]any{"Момент": time.Date(2026, 9, 29, 17, 35, 0, 0, time.UTC)}, "Момент", "date")
	if got != "29.09.2026 20:35" {
		t.Fatalf("widgetCell(time.Time UTC) = %q, ждали 29.09.2026 20:35", got)
	}
}
