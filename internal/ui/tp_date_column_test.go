package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// Матричная проверка #1077: хранимая колонка типа date в табличной части
// управляемой формы приезжает в грид ОДИНАКОВО на обоих диалектах и с верным
// календарным днём.
//
// До фикса строки грида отдавались голым json.Marshal, а он печатает time.Time
// в зоне драйвера. Зоны разные — SQLite всегда UTC, pgx берёт зону процесса, —
// поэтому одна дата приезжала как «1985-03-13T21:00:00Z» против
// «1985-03-14T00:00:00+03:00», и на SQLite день был на сутки раньше записанного.
//
// Путь публичный: запись через storage, чтение — GET формы документа с разбором
// data-sg-rows и data-sg-cols отрисованной страницы.

func tpd1077Doc() *metadata.Entity {
	doc := &metadata.Entity{
		Name: "Заказ1077", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "ДатаБезВремени", Type: metadata.FieldTypeDate},
				{Name: "ДатаСоВременем", Type: metadata.FieldTypeDate},
				{Name: "Прочее", Type: metadata.FieldTypeString},
			},
		}},
	}
	doc.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: doc.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки"},
		},
	}}
	return doc
}

func tpd1077Server(t *testing.T, db *storage.DB, doc *metadata.Entity) *httptest.Server {
	t.Helper()
	if err := db.Migrate(context.Background(), []*metadata.Entity{doc}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	s := &Server{
		store:       db,
		reg:         reg,
		interp:      interp,
		lockMgr:     runtime.NewLockManager(),
		messages:    NewMessageStore(),
		widgetCache: widget.NewCache(time.Minute),
		aiChatLimit: newAIWindowLimiter(1000, time.Minute),
	}
	s.entitySvc = &entityservice.Service{
		Store: db, Reg: reg, Interp: interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func TestTPDateColumn_ОдинаковаНаДиалектахИСВернымДнём_1077(t *testing.T) {
	// Зона уводится от UTC: съезд календарного дня возникает только на хосте со
	// смещением, и на UTC-раннере CI тест был бы зелёным на сломанном коде.
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	const wantDay = "1985-03-14T00:00"
	const wantWithTime = "1985-03-14T13:45"

	measured := map[string]map[string]string{}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		ctx := context.Background()
		doc := tpd1077Doc()
		ts := tpd1077Server(t, db, doc)

		id := uuid.New()
		if err := db.Upsert(ctx, doc.Name, id,
			map[string]any{"Дата": time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)}, doc); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if err := db.UpsertTablePartRows(ctx, doc.Name, "Строки", id, []map[string]any{{
			"ДатаБезВремени": time.Date(1985, 3, 14, 0, 0, 0, 0, time.Local),
			"ДатаСоВременем": time.Date(1985, 3, 14, 13, 45, 0, 0, time.Local),
			"Прочее":         "x",
		}}, doc.TableParts[0]); err != nil {
			t.Fatalf("UpsertTablePartRows: %v", err)
		}

		htmlOut := getBody(t, ts.URL+"/ui/document/"+url.PathEscape(doc.Name)+"/"+id.String())
		rows := parseManagedTPRows(t, htmlOut)
		if len(rows) != 1 {
			t.Fatalf("[%s] строк ТЧ %d, ожидалась 1", dialect, len(rows))
		}
		cells := map[string]string{
			"ДатаБезВремени": fmt.Sprintf("%v", rows[0]["ДатаБезВремени"]),
			"ДатаСоВременем": fmt.Sprintf("%v", rows[0]["ДатаСоВременем"]),
		}
		measured[dialect] = cells

		if got := cells["ДатаБезВремени"]; got != wantDay {
			t.Errorf("[%s] дата без времени приехала как %q, ожидалось %q", dialect, got, wantDay)
		}
		if got := cells["ДатаСоВременем"]; got != wantWithTime {
			t.Errorf("[%s] дата со временем приехала как %q, ожидалось %q", dialect, got, wantWithTime)
		}
		// Клиенту колонка обязана быть объявлена датой — без этого ветка
		// type==="date" в buildColumns не сработает и колонка снова уедет в
		// свободнотекстовый редактор.
		for _, c := range parseManagedTPColumns(t, htmlOut) {
			if c.ID == "ДатаБезВремени" && c.Type != string(metadata.FieldTypeDate) {
				t.Errorf("[%s] тип колонки на клиенте %q, ожидался %q",
					dialect, c.Type, metadata.FieldTypeDate)
			}
		}
	})

	sq, okS := measured["sqlite"]
	pg, okP := measured["postgres"]
	if !okS || !okP {
		t.Log("сравнение диалектов пропущено: postgres не прогонялся (нет TEST_DATABASE_URL)")
		return
	}
	for _, name := range []string{"ДатаБезВремени", "ДатаСоВременем"} {
		if sq[name] != pg[name] {
			t.Errorf("колонка %s расходится по диалектам: sqlite=%q postgres=%q",
				name, sq[name], pg[name])
		}
	}
}

// Круг «показали → отредактировали → записали → показали» не должен смещать дату.
//
// Это стык #1077 и #1074: грид отдаёт значение в виде «2006-01-02T15:04», ровно
// его редактор кладёт обратно в tp_json, а приведение типа на записи обязано
// принять этот вид и сохранить тот же день. Каждый из двух PR по отдельности
// проверяет свою половину; смещение возникло бы именно на стыке, и поймать его
// можно только пройдя круг целиком.
func TestTPDateColumn_КругПоказРедактированиеЗапись_НеСмещаетДень_1077(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		ctx := context.Background()
		doc := tpd1077Doc()
		ts := tpd1077Server(t, db, doc)

		id := uuid.New()
		if err := db.Upsert(ctx, doc.Name, id,
			map[string]any{"Дата": time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)}, doc); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if err := db.UpsertTablePartRows(ctx, doc.Name, "Строки", id, []map[string]any{{
			"ДатаБезВремени": time.Date(1985, 3, 14, 0, 0, 0, 0, time.Local),
			"ДатаСоВременем": time.Date(1985, 3, 14, 13, 45, 0, 0, time.Local),
			"Прочее":         "x",
		}}, doc.TableParts[0]); err != nil {
			t.Fatalf("UpsertTablePartRows: %v", err)
		}

		read := func(step string) map[string]any {
			t.Helper()
			rows := parseManagedTPRows(t, getBody(t,
				ts.URL+"/ui/document/"+url.PathEscape(doc.Name)+"/"+id.String()))
			if len(rows) != 1 {
				t.Fatalf("[%s] %s: строк ТЧ %d, ожидалась 1", dialect, step, len(rows))
			}
			return rows[0]
		}

		before := read("до правки")
		// Пользователь ничего не менял: редактор кладёт обратно то же значение,
		// что показал грид.
		blob, err := json.Marshal([]map[string]any{{
			"ДатаБезВремени": before["ДатаБезВремени"],
			"ДатаСоВременем": before["ДатаСоВременем"],
			"Прочее":         "x",
		}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if code := tpw1074Post(t, ts, doc, id, url.Values{
			"tp_json.Строки": {string(blob)},
		}); code != http.StatusSeeOther {
			t.Fatalf("[%s] запись вернула %d, ожидалось %d", dialect, code, http.StatusSeeOther)
		}

		after := read("после правки")
		for _, name := range []string{"ДатаБезВремени", "ДатаСоВременем"} {
			if before[name] != after[name] {
				t.Errorf("[%s] круг сместил %s: было %v, стало %v",
					dialect, name, before[name], after[name])
			}
		}
	})
}
