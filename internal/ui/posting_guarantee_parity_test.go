package ui

// Паритет ГАРАНТИЙ трёх путей проведения (план 124, #962 Н3).
//
// Соседний posting_parity_test.go сравнивает последствия УСПЕШНОГО проведения:
// признак, версию, движения, живой список. Этого оказалось мало. Н1 и Н2 —
// про отказы: предел времени хука, который не сработал, и значение
// перечисления, которое не отклонили. При успешном проведении все три пути
// ведут себя одинаково, тест зелёный, а расходятся они ровно там, где что-то
// должно пойти не так.
//
// Отсюда правило, которое этот файл и закрепляет: паритет успеха не
// доказывает паритет гарантий. Каждая гарантия проверяется на ВСЕХ трёх
// входах, и мутация, выключающая её в одном из них, обязана красить именно
// этот тест — а не только тест «своего» пути.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// guaranteeServer — документ с реквизитом-перечислением и хуком, который это
// значение портит. Именно хук, а не вызывающий: значение, присвоенное в
// ОбработкаПроведения, приходит в запись уже после входной проверки, и путь
// обязан отклонить его сам.
func guaranteeServer(t *testing.T, onPost string) (context.Context, *storage.DB, *Server, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "guarantee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

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

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Enums:    []*metadata.Enum{status},
		Programs: map[string]*ast.Program{doc.Name: mustParse(t, onPost)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = &entityservice.Service{
		Store: db, Reg: registry, Interp: interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}
	return ctx, db, s, doc
}

func newOrder(t *testing.T, ctx context.Context, s *Server, doc *metadata.Entity, num string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc, ID: id, IsNew: true,
		Fields: map[string]any{"Номер": num, "Статус": "Новый"},
	}); err != nil {
		t.Fatalf("подготовка документа %s: %v", num, err)
	}
	return id
}

func statusInDB(t *testing.T, ctx context.Context, db *storage.DB, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := db.QueryRow(ctx, `SELECT статус FROM заказ WHERE id=?`, id.String()).Scan(&status); err != nil {
		t.Fatalf("чтение статуса: %v", err)
	}
	return status
}

// Недопустимое значение перечисления, пришедшее ОТ ВЫЗЫВАЮЩЕГО, обязано
// отклоняться одинаково на всех путях, где вызывающий вообще передаёт поля.
//
// Списочный путь (postDocument) сюда не входит намеренно: он проводит уже
// записанный документ и полей не принимает. Проверять на нём нечего — и
// делать вид, что проверяем, значило бы получить зелёный тест ни о чём.
//
// Значение, присвоенное САМИМ ХУКОМ, проверяется отдельным тестом ниже: до
// #977 его не проверял ни один из трёх путей.
func TestПаритетГарантий_МусорВПеречисленииОтвергаетсяВезде(t *testing.T) {
	const noop = `Процедура ОбработкаПроведения()
КонецПроцедуры`
	const bad = "ТАКОГО_НЕТ"

	t.Run("entityservice (форма/REST)", func(t *testing.T) {
		ctx, db, s, doc := guaranteeServer(t, noop)
		id := newOrder(t, ctx, s, doc, "С-1")
		res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
			Entity: doc, ID: id, Action: "post",
			Fields: map[string]any{"Номер": "С-1", "Статус": bad},
		})
		assertEnumRejected(t, ctx, db, id, res.DSLError, err)
	})

	t.Run("DSL Записать()", func(t *testing.T) {
		ctx, db, s, doc := guaranteeServer(t, noop)
		id := newOrder(t, ctx, s, doc, "С-2")
		dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy)
		loaded, err := dp.LoadObject(id.String())
		if err != nil {
			t.Fatalf("ПолучитьОбъект: %v", err)
		}
		w := loaded.(*docWriter)
		w.Set("Статус", bad)
		assertEnumRejected(t, ctx, db, id, callWriteCatchingError(t, w), nil)
	})
}

// Значение, присвоенное хуком, обязано отклоняться так же, как пришедшее от
// пользователя. До #977 не проверял ни один путь: входная проверка стоит до
// хука, и присвоенное им доезжало до базы навсегда.
//
// Этот тест — про паритет в полном составе: списочный путь сюда входит, потому
// что для него хук вообще единственный источник полей.
func TestПаритетГарантий_МусорОтХукаОтвергаетсяВезде(t *testing.T) {
	const onPost = `Процедура ОбработкаПроведения()
  ЭтотОбъект.Статус = "ТАКОГО_НЕТ";
КонецПроцедуры`

	t.Run("entityservice (форма/REST)", func(t *testing.T) {
		ctx, db, s, doc := guaranteeServer(t, onPost)
		id := newOrder(t, ctx, s, doc, "Х-1")
		res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
			Entity: doc, ID: id, Action: "post",
			Fields: map[string]any{"Номер": "Х-1", "Статус": "Новый"},
		})
		assertEnumRejected(t, ctx, db, id, res.DSLError, err)
	})

	t.Run("DSL Провести()", func(t *testing.T) {
		ctx, db, s, doc := guaranteeServer(t, onPost)
		id := newOrder(t, ctx, s, doc, "Х-2")
		dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy)
		loaded, err := dp.LoadObject(id.String())
		if err != nil {
			t.Fatalf("ПолучитьОбъект: %v", err)
		}
		assertEnumRejected(t, ctx, db, id, callPostCatchingError(t, loaded.(*docWriter)), nil)
	})

	t.Run("список postDocument", func(t *testing.T) {
		ctx, db, s, doc := guaranteeServer(t, onPost)
		id := newOrder(t, ctx, s, doc, "Х-3")
		req := reqWithChi(http.MethodPost, "/ui/document/"+doc.Name+"/"+id.String()+"/post", nil,
			map[string]string{"entity": doc.Name, "id": id.String()})
		rec := httptest.NewRecorder()
		s.postDocument(rec, req)
		assertEnumRejected(t, ctx, db, id, rec.Header().Get("Location"), nil)
	})
}

// assertEnumRejected — общий критерий для всех путей: недопустимое значение не
// сохранилось. Форма сообщения у путей разная (DSLError у сервиса, паника у
// DSL), и требовать одинаковый текст было бы придиркой; одинаковым обязан быть
// результат.
func assertEnumRejected(t *testing.T, ctx context.Context, db *storage.DB, id uuid.UUID, signal string, err error) {
	t.Helper()
	got := statusInDB(t, ctx, db, id)
	if got == "ТАКОГО_НЕТ" {
		t.Fatalf("недопустимое значение перечисления сохранено в базе (сигнал отказа: %q, err=%v) — "+
			"оно останется там навсегда, а форма покажет пустой выбор", signal, err)
	}
	if got != "Новый" {
		t.Errorf("статус в базе = %q, ожидался прежний «Новый»", got)
	}
	if strings.TrimSpace(signal) == "" && err == nil {
		t.Error("значение отклонено молча — вызывающий не узнал об отказе")
	}
}

func callWriteCatchingError(t *testing.T, w *docWriter) (msg string) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil {
			msg = fmt.Sprintf("%v", rec)
		}
	}()
	w.CallMethod("записать", nil)
	return ""
}

func callPostCatchingError(t *testing.T, w *docWriter) (msg string) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil {
			msg = fmt.Sprintf("%v", rec)
		}
	}()
	w.CallMethod("провести", nil)
	return ""
}

// Предел времени хука обязан действовать на всех путях: хук исполняется внутри
// открытой транзакции, и на SQLite, где соединение единственное, зависший хук
// делает недоступной всю базу.
func TestПаритетГарантий_ПределВремениХукаДействуетВезде(t *testing.T) {
	const onPost = `Процедура ОбработкаПроведения()
  Приостановить(20);
КонецПроцедуры`

	// Предел ставим жёстким: тест обязан отличать «сработал предел» от
	// «дождались сна», и разница в двадцать раз этого достигает.
	const limitSec = 1

	t.Run("entityservice (форма/REST)", func(t *testing.T) {
		ctx, _, s, doc := guaranteeServer(t, onPost)
		s.cfg.Limits.RequestTimeoutSec = limitSec
		s.entitySvc.HookTimeout = s.operationTimeout(opEntitySave)
		id := newOrder(t, ctx, s, doc, "Т-1")
		assertHookInterrupted(t, func() string {
			res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
				Entity: doc, ID: id, Action: "post",
				Fields: map[string]any{"Номер": "Т-1", "Статус": "Новый"},
			})
			if err != nil {
				return err.Error()
			}
			return res.DSLError
		})
	})

	t.Run("DSL Провести()", func(t *testing.T) {
		ctx, _, s, doc := guaranteeServer(t, onPost)
		s.cfg.Limits.RequestTimeoutSec = limitSec
		id := newOrder(t, ctx, s, doc, "Т-2")
		dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy)
		loaded, err := dp.LoadObject(id.String())
		if err != nil {
			t.Fatalf("ПолучитьОбъект: %v", err)
		}
		assertHookInterrupted(t, func() string {
			return callPostCatchingError(t, loaded.(*docWriter))
		})
	})

	t.Run("список postDocument", func(t *testing.T) {
		ctx, _, s, doc := guaranteeServer(t, onPost)
		s.cfg.Limits.RequestTimeoutSec = limitSec
		id := newOrder(t, ctx, s, doc, "Т-3")
		assertHookInterrupted(t, func() string {
			req := reqWithChi(http.MethodPost, "/ui/document/"+doc.Name+"/"+id.String()+"/post", nil,
				map[string]string{"entity": doc.Name, "id": id.String()})
			rec := httptest.NewRecorder()
			s.postDocument(rec, req)
			return rec.Header().Get("Location")
		})
	})
}

// assertHookInterrupted проверяет не текст ошибки, а время: важно, что путь
// вернул управление по пределу, а не досидел до конца паузы.
func assertHookInterrupted(t *testing.T, run func() string) {
	t.Helper()
	done := make(chan string, 1)
	go func() { done <- run() }()

	select {
	case signal := <-done:
		if strings.TrimSpace(signal) == "" {
			t.Error("хук отработал без признака отказа — предел времени не применился")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("хук со сном 20 с не был прерван за 10 с при пределе 1 с — " +
			"на SQLite это значит, что вся база недоступна всё это время")
	}
}
