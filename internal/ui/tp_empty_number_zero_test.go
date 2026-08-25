package ui

// Пустое числовое поле табличной части и идиома 1С «Поле = 0» (#1136).
//
// Матричная проверка публичного пути целиком: POST формы документа → строка в
// базе → чтение объекта из DSL через Документы.X. Невведённая цена доезжает до
// модуля как nil (в колонке — NULL), и `Если Стр.Цена = 0` молча давал Ложь:
// nil не проходил числовую ветку сравнения и уходил в строковый ключ.
//
// Диалекты здесь не декорация: `number` на SQLite хранится TEXT, на PostgreSQL
// — numeric, поэтому заполненное число возвращается в DSL разными Go-типами, а
// пустое обязано в обоих случаях остаться NULL и сравниваться с нулём одинаково.

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
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

const tpz1136DocName = "ПробаПустойЦены"

// tpz1136Doc — документ с управляемой формой: разбор ТЧ идёт путём tp_json, тем
// самым, что описан в заявке (ввод строки в SlickGrid).
func tpz1136Doc() *metadata.Entity {
	doc := &metadata.Entity{
		Name: tpz1136DocName, Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{
			Name: "Товары",
			Fields: []metadata.Field{
				{Name: "Цена", Type: metadata.FieldTypeNumber},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	doc.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: doc.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementTablePart, Name: "Товары", DataPath: "Объект.Товары"},
		},
	}}
	return doc
}

// tpz1136Server поднимает и HTTP-точку записи, и сам Server: путь чтения идёт
// через Документы.X, а он живёт на Server, а не на роутере.
func tpz1136Server(t *testing.T, db *storage.DB, entities []*metadata.Entity) (*httptest.Server, *Server) {
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
	s.entitySvc = s.newEntityService(nil)
	r := chi.NewRouter()
	s.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, s
}

// tpz1136Write записывает документ формой и возвращает его идентификатор.
func tpz1136Write(t *testing.T, ts *httptest.Server, doc *metadata.Entity, db *storage.DB, row map[string]any) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := db.Upsert(context.Background(), doc.Name, id,
		map[string]any{"Дата": time.Now()}, doc); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	blob, err := json.Marshal([]map[string]any{row})
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if got := tpw1074Post(t, ts, doc, id, url.Values{"tp_json.Товары": {string(blob)}}); got != http.StatusSeeOther {
		t.Fatalf("POST формы: ответ %d, ожидался %d", got, http.StatusSeeOther)
	}
	return id
}

// tpz1136Rows — строки ТЧ прямо из базы: ими проверяется предусловие (в колонке
// NULL) и отсутствие строк-призраков.
func tpz1136Rows(t *testing.T, db *storage.DB, doc *metadata.Entity, id uuid.UUID) []map[string]any {
	t.Helper()
	rows, err := db.GetTablePartRows(context.Background(), doc.Name, "Товары", id, doc.TableParts[0])
	if err != nil {
		t.Fatalf("GetTablePartRows: %v", err)
	}
	return rows
}

// tpz1136ReadFromDSL возвращает «Цена=0/Цена>0/ЗначениеЗаполнено» для каждой
// строки прочитанного объекта. Путь публичный: тот же, которым прикладной
// модуль перебирает ТЧ существующего документа.
func tpz1136ReadFromDSL(t *testing.T, s *Server, id uuid.UUID) string {
	t.Helper()
	src := fmt.Sprintf(`Функция Т()
  Об = Документы.%s.НайтиПоИдентификатору(Ид).ПолучитьОбъект();
  Рез = "";
  Для Каждого Стр Из Об.Товары Цикл
    Рез = Рез + Строка(Стр.Цена = 0) + "/" + Строка(Стр.Цена > 0) + "/" + Строка(ЗначениеЗаполнено(Стр.Цена));
  КонецЦикла;
  Возврат Рез;
КонецФункции`, tpz1136DocName)
	prog, err := parser.New(lexer.New(src, "тест.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ctx := context.Background()
	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
	vars["Ид"] = id.String()
	var result any
	if err := s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := result.(string)
	return got
}

func TestTablePartEmptyNumber_СравниваетсяСНулём_1136(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		doc := tpz1136Doc()
		ts, s := tpz1136Server(t, db, []*metadata.Entity{doc})

		// Цена не введена — ровно случай из заявки: в колонке NULL, а модуль
		// обязан увидеть ноль.
		empty := tpz1136Write(t, ts, doc, db, map[string]any{"Цена": "", "Количество": "2"})
		rows := tpz1136Rows(t, db, doc, empty)
		if len(rows) != 1 {
			t.Fatalf("[%s] строк в базе %d, ожидалась 1: %#v", dialect, len(rows), rows)
		}
		// Предусловие: правка лечит чтение, а не запись — в базе по-прежнему NULL.
		if rows[0]["Цена"] != nil {
			t.Errorf("[%s] в колонке цены %#v, ожидался NULL", dialect, rows[0]["Цена"])
		}
		if got := tpz1136ReadFromDSL(t, s, empty); got != "true/false/false" {
			t.Errorf("[%s] пустая цена: «=0/>0/Заполнено» = %q, ожидалось \"true/false/false\"", dialect, got)
		}

		// Введённая цена не должна стать нулём заодно.
		filled := tpz1136Write(t, ts, doc, db, map[string]any{"Цена": "10", "Количество": "2"})
		if got := tpz1136ReadFromDSL(t, s, filled); got != "false/true/true" {
			t.Errorf("[%s] введённая цена: «=0/>0/Заполнено» = %q, ожидалось \"false/true/true\"", dialect, got)
		}

		// Введённый ноль неотличим от пустоты — и был неотличим до правки:
		// ЗначениеЗаполнено(0) в 1С тоже Ложь. Закрепляем, чтобы правку не
		// прочитали как утрату различия, которого не было.
		zero := tpz1136Write(t, ts, doc, db, map[string]any{"Цена": "0", "Количество": "2"})
		if got := tpz1136ReadFromDSL(t, s, zero); got != "true/false/false" {
			t.Errorf("[%s] введённый ноль: «=0/>0/Заполнено» = %q, ожидалось \"true/false/false\"", dialect, got)
		}
	})
}

// Симптом заявки в исходном виде: ПриИзменении строки ТЧ, цена ещё не введена,
// объект собран из tp_json — до базы дело вообще не дошло. Ветка автоподстановки
// цены выбиралась по `Стр.Цена = 0` и молча не срабатывала.
func TestFormEventEmptyNumber_НоваяСтрокаВидитНоль_1136(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТоварыПриИзмененииСтроки()
	Для Каждого Стр Из Объект.Товары Цикл
		Если Стр.Цена = 0 Тогда
			Сообщить("подставляем цену");
		КонецЕсли;
	КонецЦикла;
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind:     metadata.FormElementTablePart,
		Name:     "ЭлементТовары",
		DataPath: "Объект.Товары",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnRowChanged: "ТоварыПриИзмененииСтроки",
		},
	}})
	ent.TableParts = []metadata.TablePart{{
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Цена", Type: metadata.FieldTypeNumber},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		},
	}}

	body := url.Values{}
	body.Set("_element", "ЭлементТовары")
	body.Set("_event", string(metadata.FormEventOnRowChanged))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("_tp_row", "0")
	body.Set("_tp_row_number", "1")
	body.Set("tp_json.Товары", `[{"Цена":"","Количество":"2"}]`)

	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "подставляем цену" {
		t.Errorf("messages=%v, ожидалось [подставляем цену]", resp.Messages)
	}
}

// Регресс на строки-призраки: полностью пустая строка грида в базу не идёт.
// Это цена варианта «дефолты типа при сборке строки» (#1136, вариант 1), от
// которого отказались, — но проверка нужна и здесь: она сторожит границу, за
// которую починка не должна заходить.
func TestTablePartEmptyNumber_ПустаяСтрокаНеСохраняется_1136(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dialect := "sqlite"
		if strings.Contains(t.Name(), "postgres") {
			dialect = "postgres"
		}
		doc := tpz1136Doc()
		ts, _ := tpz1136Server(t, db, []*metadata.Entity{doc})

		id := tpz1136Write(t, ts, doc, db, map[string]any{"Цена": "", "Количество": ""})
		if rows := tpz1136Rows(t, db, doc, id); len(rows) != 0 {
			t.Errorf("[%s] пустая строка грида сохранена: %#v", dialect, rows)
		}
	})
}
