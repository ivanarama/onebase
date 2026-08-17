package ui

// Гарантии записи документа из DSL (#962).
//
// Обе проверялись только на пути entityservice.Save, а запись документа из DSL
// идёт своей реализацией (docWriter) — и обе мимо неё проезжали. Тесты идут
// публичным путём: DSL-код через Документы.X, как его пишет прикладной
// разработчик, а не вызовом внутренних функций (правило #611).

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// docGuardServer поднимает сервер с документом, у которого есть
// реквизит-перечисление и (по желанию) хук проведения.
func docGuardServer(t *testing.T, onPost string, limitSec int) (*Server, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "guards.db"))
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
	s.cfg.Limits.RequestTimeoutSec = limitSec
	return s, ctx
}

// Недопустимое значение перечисления не должно доехать до базы: лежит оно там
// потом навсегда, форма показывает пустой выбор, а сравнение «Если Статус =
// "Закрыт"» молча не срабатывает.
func TestДокументыDSL_МусорВПеречисленииОтвергается(t *testing.T) {
	s, ctx := docGuardServer(t, "", 0)

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	proxy := root.Get("Заказ")
	if proxy == nil {
		t.Fatal("Документы.Заказ → nil")
	}
	w, ok := proxy.(*docProxy).CallMethod("создать", nil).(*docWriter)
	if !ok {
		t.Fatal("Создать() вернул не docWriter")
	}
	w.Set("Номер", "З-1")
	w.Set("Статус", "ТАКОГО_НЕТ")

	err := callWriteExpectingError(t, w)
	if err == nil {
		t.Fatal("запись приняла несуществующее значение перечисления — оно осталось бы в базе навсегда")
	}
	if !strings.Contains(err.Error(), "ТАКОГО_НЕТ") && !strings.Contains(err.Error(), "СтатусЗаказа") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}

	var count int
	if err := s.store.QueryRow(ctx, `SELECT COUNT(*) FROM заказ`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("отклонённый документ всё же записан: строк %d", count)
	}
}

// Допустимое значение обязано проходить: проверка, отвергающая верное, хуже
// отсутствующей.
func TestДокументыDSL_ДопустимоеЗначениеПеречисленияПроходит(t *testing.T) {
	s, ctx := docGuardServer(t, "", 0)

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	w := root.Get("Заказ").(*docProxy).CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "З-2")
	w.Set("Статус", "Закрыт")

	if err := callWriteExpectingError(t, w); err != nil {
		t.Fatalf("допустимое значение отвергнуто: %v", err)
	}
	var count int
	if err := s.store.QueryRow(ctx, `SELECT COUNT(*) FROM заказ`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("документ не записан: строк %d", count)
	}
}

// Хук проведения обязан подчиняться пределу времени. Пока его не было, пауза в
// ОбработкаПроведения держала открытую транзакцию всё своё время — на SQLite
// это единственное соединение, то есть вся база.
func TestДокументыDSL_ДолгийХукОбрываетсяПоЛимиту(t *testing.T) {
	onPost := `Процедура ОбработкаПроведения()
  Приостановить(20);
КонецПроцедуры`
	s, ctx := docGuardServer(t, onPost, 1)

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	w := root.Get("Заказ").(*docProxy).CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "З-3")

	started := time.Now()
	err := callPostExpectingError(t, w)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("двадцатисекундный хук отработал целиком, хотя предел операции — одна секунда")
	}
	// Запас щедрый: важно, что обрыв случился по пределу, а не по сну.
	if elapsed > 10*time.Second {
		t.Fatalf("хук держал транзакцию %v при пределе 1 с — предел не применился", elapsed)
	}
}

// callWriteExpectingError зовёт Записать() и возвращает ошибку, если она была.
// Ошибки DSL приходят паникой userError — так их видит и прикладной код,
// поэтому ловим ровно так же.
func callWriteExpectingError(t *testing.T, w *docWriter) (err error) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%v", rec)
		}
	}()
	w.CallMethod("записать", nil)
	return nil
}

func callPostExpectingError(t *testing.T, w *docWriter) (err error) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%v", rec)
		}
	}()
	w.CallMethod("провести", nil)
	return nil
}
