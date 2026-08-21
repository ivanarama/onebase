package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Матричная проверка #1076: реквизиты bool и date показываются одинаково на
// обоих диалектах в обычном списке справочника.
//
// До фикса значение шло через типобезразличный fmtReportCell, и булево
// приезжало из драйверов по-разному: SQLite числом, pgx булевым. В списке это
// давало «1» против «true» — замерено на этом же тесте до правки.
//
// Проверять надо СКВОЗЬ рендер страницы, а не вызовом форматтера: диспетчер по
// типу в шаблоне списка не один, и ветка bool отсутствовала ровно в той цепочке,
// которая рисует неключевые колонки. Юнит на fmtReportCell этого не показал бы.

var tcd1076Tag = regexp.MustCompile(`<[^>]+>`)

func tcd1076Cells(t *testing.T, htmlOut string) []string {
	t.Helper()
	var out []string
	for _, c := range regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`).FindAllStringSubmatch(htmlOut, -1) {
		txt := strings.TrimSpace(tcd1076Tag.ReplaceAllString(c[1], ""))
		if txt != "" {
			out = append(out, txt)
		}
	}
	return out
}

func tcd1076Has(cells []string, want string) bool {
	for _, c := range cells {
		if c == want {
			return true
		}
	}
	return false
}

func TestTypedCellDisplay_СписокОдинаковНаДиалектах_1076(t *testing.T) {
	// Зона уводится от UTC: часть дефекта — съезд календарного дня, а на
	// UTC-раннере он не воспроизводится.
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	ent := &metadata.Entity{
		Name: "Клиент1076", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Архивный", Type: metadata.FieldTypeBool},
			{Name: "ДатаРег", Type: metadata.FieldTypeDate},
		},
	}

	measured := map[string][]string{}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		ctx := context.Background()
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
		interp := interpreter.New()
		interp.LookupProc = reg.GetModuleProc
		s := &Server{
			store: db, reg: reg, interp: interp,
			lockMgr: runtime.NewLockManager(), messages: NewMessageStore(),
			widgetCache: widget.NewCache(time.Minute),
			aiChatLimit: newAIWindowLimiter(1000, time.Minute),
		}
		s.entitySvc = &entityservice.Service{
			Store: db, Reg: reg, Interp: interp,
			PrepareHook: s.enrichHeaderRefs, EnrichTPRows: s.enrichTPRowsWithRefs,
			BuildVars: s.buildDSLVarsWithMessagesTx,
			MakeThis: func(ctx context.Context, cs interpreter.CtxSource, o *runtime.Object, e *metadata.Entity) interpreter.This {
				return s.newFormObjectThisLive(ctx, cs, o, e, nil, false)
			},
		}
		r := chi.NewRouter()
		s.Mount(r)
		ts := httptest.NewServer(r)
		t.Cleanup(ts.Close)

		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
			"Наименование": "ООО Ромашка",
			"Активен":      true,
			"Архивный":     false,
			"ДатаРег":      time.Date(1985, 3, 14, 0, 0, 0, 0, time.Local),
		}, ent); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		cells := tcd1076Cells(t, getBody(t, ts.URL+"/ui/catalog/"+url.PathEscape(ent.Name)))
		measured[dialect] = cells

		if !tcd1076Has(cells, "✓") {
			t.Errorf("[%s] в списке нет галочки для истины, ячейки: %q", dialect, cells)
		}
		if !tcd1076Has(cells, "—") {
			t.Errorf("[%s] в списке нет прочерка для лжи, ячейки: %q", dialect, cells)
		}
		// Внутреннее представление булева не должно доезжать до страницы.
		for _, bad := range []string{"1", "0", "true", "false"} {
			if tcd1076Has(cells, bad) {
				t.Errorf("[%s] в списке видно внутреннее представление булева %q, ячейки: %q",
					dialect, bad, cells)
			}
		}
		if !tcd1076Has(cells, "14.03.1985") {
			t.Errorf("[%s] в списке нет даты 14.03.1985, ячейки: %q", dialect, cells)
		}
	})

	sq, okS := measured["sqlite"]
	pg, okP := measured["postgres"]
	if !okS || !okP {
		t.Log("сравнение диалектов пропущено: postgres не прогонялся (нет TEST_DATABASE_URL)")
		return
	}
	if strings.Join(sq, "|") != strings.Join(pg, "|") {
		t.Errorf("список расходится по диалектам:\n sqlite  =%q\n postgres=%q", sq, pg)
	}
}

// fieldDisplayText — общая точка; здесь фиксируется её договор по типам, чтобы
// следующая правка не увела один из вызывающих в собственную ветку.
func TestFieldDisplayText_ТипРешаетВид_1076(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	boolField := metadata.Field{Name: "Активен", Type: metadata.FieldTypeBool}
	dateField := metadata.Field{Name: "ДатаРег", Type: metadata.FieldTypeDate}

	cases := []struct {
		name  string
		field metadata.Field
		in    any
		want  string
	}{
		// Обе формы, в которых булево приходит из драйверов.
		{"булево из pgx", boolField, true, "✓"},
		{"булево из pgx (ложь)", boolField, false, "—"},
		{"булево из SQLite", boolField, int64(1), "✓"},
		{"булево из SQLite (ложь)", boolField, int64(0), "—"},
		{"дата из SQLite (UTC)", dateField, time.Date(1985, 3, 13, 21, 0, 0, 0, time.UTC), "14.03.1985"},
		{"дата из pgx (местная зона)", dateField, time.Date(1985, 3, 14, 0, 0, 0, 0, time.Local), "14.03.1985"},
		{"nil → пусто", dateField, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fieldDisplayText(c.field, c.in, nil); got != c.want {
				t.Errorf("fieldDisplayText(%s, %v) = %q, ожидалось %q",
					c.field.Type, c.in, got, c.want)
			}
		})
	}
}
