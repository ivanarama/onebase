package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Разбор значения параметра отчёта лежал в трёх копиях, и копии расходились
// НАМЕРЕННО: экран и выгрузки оставляют негодную дату строкой и дают построить
// отчёт, а `/api/v2` отвечает 400. Расхождение нигде не было записано и
// держалось случайно. Проверяем его через публичные точки входа: половина на
// экране здесь, половина в internal/api.
func TestОтчёт_НегоднаяДатаНаЭкранеОстаётсяСтрокой(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "report-param-parse.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cat := &metadata.Entity{
		Name: "КлиентОтчёта",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}
	rep := &reportpkg.Report{
		Name:   "КлиентыНаДату",
		Params: []reportpkg.Param{{Name: "НаДату", Type: "date"}},
		Query:  `ВЫБРАТЬ Наименование ИЗ Справочник.КлиентОтчёта ГДЕ Наименование >= &НаДату`,
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}, Reports: []*reportpkg.Report{rep}})
	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interpreter.New(),
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
		ops:      newOperationLimiter(),
	}

	run := func(value string) *httptest.ResponseRecorder {
		form := url.Values{"НаДату": {value}}
		r := reqWithChi("POST", "/ui/report/КлиентыНаДату", form, map[string]string{"name": "КлиентыНаДату"})
		w := httptest.NewRecorder()
		s.reportRun(w, r)
		return w
	}

	w := run("мусор")
	if w.Code != 200 {
		t.Fatalf("экран ответил %d на негодную дату, ожидался 200: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "мусор") {
		t.Fatalf("экран не вернул введённое значение в поле — форма не перезаполнена")
	}

	// Контроль: годная дата по тому же пути тоже строится.
	if w := run("2026-08-29"); w.Code != 200 {
		t.Fatalf("экран ответил %d на годную дату: %s", w.Code, w.Body.String())
	}
}
