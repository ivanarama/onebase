package ui

import (
	"context"
	"encoding/json"
	"io"
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

// Матричная проверка #1074: значение реквизита табличной части приводится к
// своему типу ДО записи, одинаково на обоих диалектах и на обоих путях разбора
// формы.
//
// До фикса приведение разбирало только number и bool, а дата попадала в default
// и уезжала в базу строкой как есть. Колонка даты на SQLite — TEXT, поэтому
// «14.03.1985» (и любой другой текст) ложился туда молча, с ответом 303: для
// пользователя это выглядело успешным сохранением. На PostgreSQL та же строка
// давала 500. Повод ввести туда произвольный текст самый обычный — ячейка даты
// редактируется свободным текстом (#1077).
//
// Проверяются ОБА пути: tp_json управляемой формы и именованные поля
// автогенерируемой. До #1074 приведение было в них размножено двумя копиями
// одного switch, и починка одной копии не дошла бы до второй.

func tpw1074Server(t *testing.T, db *storage.DB, entities []*metadata.Entity) *httptest.Server {
	t.Helper()
	if err := db.Migrate(context.Background(), entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
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
		Store:        db,
		Reg:          reg,
		Interp:       interp,
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

// tpw1074Doc — документ с реквизитами ТЧ проверяемых типов. Управляемая форма
// добавляется только при managed=true: без неё разбор идёт вторым путём.
func tpw1074Doc(name string, managed bool) *metadata.Entity {
	doc := &metadata.Entity{
		Name: name, Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Дат", Type: metadata.FieldTypeDate},
				{Name: "Чис", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	if managed {
		doc.Forms = []*metadata.FormModule{{
			Name: "ФормаОбъекта", Kind: "object", EntityName: name,
			LayoutKind: metadata.FormLayoutManaged,
			Elements: []*metadata.FormElement{
				{Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки"},
			},
		}}
	}
	return doc
}

func tpw1074Post(t *testing.T, ts *httptest.Server, doc *metadata.Entity, id uuid.UUID, form url.Values) int {
	t.Helper()
	form.Set("Дата", "2026-01-01")
	form.Set("action", "save")
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := hc.PostForm(ts.URL+"/ui/document/"+url.PathEscape(doc.Name)+"/"+id.String(), form) //nolint:noctx // тестовый сервер
	if err != nil {
		t.Fatalf("POST формы документа: %v", err)
	}
	defer resp.Body.Close()        //nolint:errcheck // тело не нужно
	io.Copy(io.Discard, resp.Body) //nolint:errcheck // тело не нужно
	return resp.StatusCode
}

// tpw1074Day — календарный день записанного значения. Именно он терялся: строка
// в колонке даты не день сдвигала, а вовсе переставала быть датой.
func tpw1074Day(t *testing.T, v any) string {
	t.Helper()
	switch d := v.(type) {
	case time.Time:
		return d.In(time.Local).Format("2006-01-02")
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, d); err == nil {
				return parsed.In(time.Local).Format("2006-01-02")
			}
		}
		return "НЕ ДАТА: " + d
	}
	return "НЕ ДАТА"
}

func tpw1074Rows(t *testing.T, db *storage.DB, doc *metadata.Entity, id uuid.UUID) []map[string]any {
	t.Helper()
	rows, err := db.GetTablePartRows(context.Background(), doc.Name, "Строки", id, doc.TableParts[0])
	if err != nil {
		t.Fatalf("GetTablePartRows: %v", err)
	}
	return rows
}

func TestTablePartWrite_ЗначениеПриводитсяКТипуДоЗаписи_1074(t *testing.T) {
	// Зона уводится от UTC: календарный день даты — то, что здесь проверяется,
	// а на UTC-раннере часть расхождений по датам не воспроизводится вовсе.
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	// Оба пути разбора формы: управляемая шлёт tp_json, автогенерируемая —
	// именованные поля tp.<ТЧ>.<индекс>.<реквизит>.
	paths := []struct {
		name    string
		managed bool
		payload func(dat, chis string) url.Values
	}{
		{
			name: "управляемая форма (tp_json)", managed: true,
			payload: func(dat, chis string) url.Values {
				blob, _ := json.Marshal([]map[string]any{{"Дат": dat, "Чис": chis}})
				return url.Values{"tp_json.Строки": {string(blob)}}
			},
		},
		{
			name: "автогенерируемая форма (именованные поля)", managed: false,
			payload: func(dat, chis string) url.Values {
				return url.Values{
					"tp.Строки.0.Дат": {dat},
					"tp.Строки.0.Чис": {chis},
				}
			},
		},
	}

	cases := []struct {
		name       string
		dat, chis  string
		wantStatus int
		wantRows   int
		wantDay    string // при wantRows==1: пусто — ожидается NULL
	}{
		{"дата как её показывает форма", "14.03.1985", "1", http.StatusSeeOther, 1, "1985-03-14"},
		{"дата в ISO из редактора", "1985-03-14", "1", http.StatusSeeOther, 1, "1985-03-14"},
		{"текст вместо даты отбивается", "не дата", "1", http.StatusBadRequest, 0, ""},
		{"текст вместо числа отбивается", "1985-03-14", "не число", http.StatusBadRequest, 0, ""},
		// Незаполненный необязательный реквизит — рядовое действие. До #1074 в
		// колонку даты уходила пустая строка: на SQLite она туда ложилась, на
		// PostgreSQL давала 500.
		{"пустая дата даёт NULL", "", "1", http.StatusSeeOther, 1, ""},
	}

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		for _, path := range paths {
			for _, tc := range cases {
				doc := tpw1074Doc("Проба"+strings.ReplaceAll(uuid.New().String()[:8], "-", ""), path.managed)
				ts := tpw1074Server(t, db, []*metadata.Entity{doc})
				id := uuid.New()
				if err := db.Upsert(context.Background(), doc.Name, id,
					map[string]any{"Дата": time.Now()}, doc); err != nil {
					t.Fatalf("Upsert: %v", err)
				}

				got := tpw1074Post(t, ts, doc, id, path.payload(tc.dat, tc.chis))
				if got != tc.wantStatus {
					t.Errorf("[%s] %s / %s: ответ %d, ожидался %d",
						dialect, path.name, tc.name, got, tc.wantStatus)
					continue
				}
				rows := tpw1074Rows(t, db, doc, id)
				if len(rows) != tc.wantRows {
					// Отказ обязан быть полным: строка не должна оказаться в базе
					// ни целиком, ни частично.
					t.Errorf("[%s] %s / %s: строк в базе %d, ожидалось %d: %#v",
						dialect, path.name, tc.name, len(rows), tc.wantRows, rows)
					continue
				}
				if tc.wantRows == 0 {
					continue
				}
				if tc.wantDay == "" {
					if rows[0]["Дат"] != nil {
						t.Errorf("[%s] %s / %s: в колонке даты %#v, ожидался NULL",
							dialect, path.name, tc.name, rows[0]["Дат"])
					}
					continue
				}
				if day := tpw1074Day(t, rows[0]["Дат"]); day != tc.wantDay {
					t.Errorf("[%s] %s / %s: в базе день %s (%#v), ожидался %s",
						dialect, path.name, tc.name, day, rows[0]["Дат"], tc.wantDay)
				}
			}
		}
	})
}

// Непришедший реквизит — это пусто. До #1074 автогенерируемый путь считал его
// через безусловный fmt.Sprintf("%v", nil) и записывал в базу строку «<nil>».
func TestTablePartWrite_ОтсутствующийРеквизитНеСтановитсяNil_1074(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		doc := tpw1074Doc("ПробаБезПоля", false)
		ts := tpw1074Server(t, db, []*metadata.Entity{doc})
		id := uuid.New()
		if err := db.Upsert(context.Background(), doc.Name, id,
			map[string]any{"Дата": time.Now()}, doc); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		// Прислано только число; реквизит даты не пришёл вовсе.
		if got := tpw1074Post(t, ts, doc, id, url.Values{"tp.Строки.0.Чис": {"1"}}); got != http.StatusSeeOther {
			t.Fatalf("[%s] ответ %d, ожидался %d", dialect, got, http.StatusSeeOther)
		}
		rows := tpw1074Rows(t, db, doc, id)
		if len(rows) != 1 {
			t.Fatalf("[%s] строк в базе %d, ожидалась 1", dialect, len(rows))
		}
		if got, ok := rows[0]["Дат"].(string); ok && strings.Contains(got, "nil") {
			t.Errorf("[%s] в колонке даты %q — непришедший реквизит записан как текст", dialect, got)
		}
	})
}
