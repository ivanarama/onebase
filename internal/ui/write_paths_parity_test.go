package ui

// Паритет дверей записи документа (#962, находка Н3).
//
// Записать и провести документ можно тремя независимыми реализациями:
// entityservice.Save (форма, REST, DSL-справочники), docWriter
// (DSL-документы) и Server.postDocument (кнопка «Провести» из списка). Каждая
// повторяет то, что делают другие, поэтому новая гарантия либо пишется трижды,
// либо на одной из дверей молча не работает — так прожили #769 (перечисления
// мимо DSL-документов) и #865 (дедлайн хука мимо кнопки).
//
// Этот тест — негативная проверка, которую предлагает сама заявка: гарантия
// обязана ломаться на ВСЕХ входах, если её выключить. Пока такого теста нет,
// расхождение видно только чтением кода; с ним — падает столько дверей,
// сколько потеряло гарантию, и по списку сразу понятно какая.
//
// Гарантия для проверки выбрана простая и без побочных эффектов — контроль
// значений перечисления. Она есть на всех трёх путях, поэтому годится как
// эталон паритета: если завтра появится четвёртая дверь, тест покажет её
// отсутствие в списке, а не промолчит.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

const parityBadEnum = "ТАКОГО_НЕТ"

// parityServer поднимает сервер с документом, у которого есть
// реквизит-перечисление. onPost — исходник хука проведения (может быть пустым).
func parityServer(t *testing.T, onPost string) (*Server, *metadata.Entity, *storage.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	status := &metadata.Enum{Name: "СтатусЗаказа", Values: []string{"Новый", "Закрыт"}}
	doc := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldTypeString, EnumName: "СтатусЗаказа"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	programs := map[string]*ast.Program{}
	if onPost != "" {
		programs["Заказ"] = mustParse(t, onPost)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Enums:    []*metadata.Enum{status},
		Programs: programs,
	})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	s.entitySvc = s.newEntityService(nil)
	return s, doc, db, ctx
}

// writeViaSave — дверь №1: entityservice.Save (форма, REST v1/v2, DSL-справочники).
func writeViaSave(t *testing.T, status string) error {
	t.Helper()
	s, doc, _, ctx := parityServer(t, "")
	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     uuid.New(),
		IsNew:  true,
		Fields: map[string]any{"номер": "З-1", "статус": status},
	})
	if err != nil {
		return err
	}
	// Save сообщает об отказе НЕ ошибкой, а полем результата: err остаётся nil.
	// Вызывающий, который смотрит только на err, примет отказ за успех — и это
	// само по себе часть находки Н3: одна гарантия, три способа сообщить о ней.
	if res.DSLError != "" {
		return fmt.Errorf("%s", res.DSLError)
	}
	return nil
}

// writeViaDSL — дверь №2: Документы.X.Создать().Записать() (docWriter).
func writeViaDSL(t *testing.T, status string) (err error) {
	t.Helper()
	s, _, _, ctx := parityServer(t, "")
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	proxy, ok := root.Get("Заказ").(*docProxy)
	if !ok {
		t.Fatal("Документы.Заказ вернул не docProxy")
	}
	w, ok := proxy.CallMethod("создать", nil).(*docWriter)
	if !ok {
		t.Fatal("Создать() вернул не docWriter")
	}
	w.Set("Номер", "З-2")
	w.Set("Статус", status)

	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%v", rec)
		}
	}()
	w.CallMethod("записать", nil)
	return nil
}

// postViaButton — дверь №3: POST /ui/document/{entity}/{id}/post, то есть
// кнопка «Провести» из списка. Значение перечисления ставит хук проведения:
// именно так шапка меняется на этом пути, и именно там она проверяется.
func postViaButton(t *testing.T, status string) error {
	t.Helper()
	onPost := fmt.Sprintf(`Процедура ОбработкаПроведения()
  ЭтотОбъект.Статус = "%s";
КонецПроцедуры`, status)
	s, doc, db, ctx := parityServer(t, onPost)

	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{"номер": "З-3", "статус": "Новый"}, doc); err != nil {
		t.Fatal(err)
	}

	r := reqWithChi("POST", "/ui/document/заказ/"+id.String()+"/post", url.Values{},
		map[string]string{"kind": "document", "entity": "заказ", "id": id.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, r)

	row, err := db.GetByID(ctx, doc.Name, id, doc)
	if err != nil {
		t.Fatal(err)
	}
	if asBool(row["posted"]) {
		return nil // дверь приняла запись
	}
	// Эта дверь отказывает третьим способом: редирект 303 с текстом причины в
	// параметре posting_error. Ни ошибки, ни поля результата — HTTP-ответ.
	reason := rec.Header().Get("Location")
	if idx := strings.Index(reason, "posting_error="); idx >= 0 {
		if decoded, decErr := url.QueryUnescape(reason[idx+len("posting_error="):]); decErr == nil {
			reason = decoded
		}
	}
	return fmt.Errorf("документ не проведён: %s", strings.TrimSpace(reason))
}

type parityDoor struct {
	name  string
	write func(t *testing.T, status string) error
}

func parityDoors() []parityDoor {
	return []parityDoor{
		{"entityservice.Save (форма, REST)", func(t *testing.T, s string) error { return writeViaSave(t, s) }},
		{"DSL Документы.X.Записать()", func(t *testing.T, s string) error { return writeViaDSL(t, s) }},
		{"POST …/post (кнопка «Провести»)", func(t *testing.T, s string) error { return postViaButton(t, s) }},
	}
}

// Главная проверка: мусор в перечислении обязан быть отвергнут КАЖДОЙ дверью.
// Если гарантию выключить, тест назовёт поимённо те двери, которые её потеряли,
// — а не промолчит, потому что уцелела самая обкатанная.
func TestWritePaths_EnumGuardHoldsOnEveryDoor(t *testing.T) {
	var passed []string
	for _, door := range parityDoors() {
		t.Run(door.name, func(t *testing.T) {
			err := door.write(t, parityBadEnum)
			if err == nil {
				passed = append(passed, door.name)
				t.Fatalf("дверь приняла несуществующее значение перечисления — оно осталось бы в базе навсегда")
			}
			if !strings.Contains(err.Error(), parityBadEnum) && !strings.Contains(err.Error(), "СтатусЗаказа") {
				t.Errorf("отказ не объясняет причину: %v", err)
			}
		})
	}
	if len(passed) > 0 {
		t.Errorf("гарантия держится не на всех входах записи; пропустили: %s", strings.Join(passed, ", "))
	}
}

// Контроль: допустимое значение обязано проходить всеми дверями. Без него тест
// выше проходил бы и на реализации, которая отвергает вообще всё.
func TestWritePaths_ValidEnumPassesOnEveryDoor(t *testing.T) {
	for _, door := range parityDoors() {
		t.Run(door.name, func(t *testing.T) {
			if err := door.write(t, "Закрыт"); err != nil {
				t.Fatalf("допустимое значение отвергнуто: %v", err)
			}
		})
	}
}
