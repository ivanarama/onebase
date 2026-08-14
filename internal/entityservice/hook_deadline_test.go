package entityservice

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Прикладной хук обязан отрубаться по дедлайну (#865).
//
// Deadline-aware защита паузы (#736) была сделана только для sandboxed-путей —
// кнопок обработок и приёмки. Хуки Save (ОбработкаПроведения, ПриЗаписи,
// ОбработкаЗаполнения) исполнялись обычным Interp.Run: без дедлайна и ВНУТРИ
// открытой транзакции. `Приостановить(300)` в модуле проведения держал
// HTTP-запрос и БД-транзакцию пять минут; на SQLite соединение единственное,
// то есть держал всю базу.

func mustParseProc(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "t.os")).ParseProgram()
	if err != nil {
		t.Fatalf("разбор модуля: %v", err)
	}
	return prog
}

// newSleepingDoc — документ, чей ОбработкаПроведения спит дольше любого
// разумного предела.
func newSleepingDoc(t *testing.T, hookTimeout time.Duration) (context.Context, *storage.DB, *Service, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "deadline.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "Долгий",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: mustParseProc(t, `Процедура ОбработкаПроведения()
  Приостановить(300);
КонецПроцедуры`)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	svc := &Service{Store: db, Reg: registry, Interp: interp, HookTimeout: hookTimeout}
	return ctx, db, svc, doc
}

func TestХукПроведения_ОтрубаетсяПоДедлайну(t *testing.T) {
	ctx, db, svc, doc := newSleepingDoc(t, 300*time.Millisecond)

	start := time.Now()
	res, err := svc.Save(ctx, SaveRequest{
		Entity: doc,
		ID:     uuid.New(),
		IsNew:  true,
		Action: "post",
		Fields: map[string]any{"Номер": "1"},
	})
	elapsed := time.Since(start)

	if elapsed > 30*time.Second {
		t.Fatalf("хук выполнялся %v — дедлайн не сработал вовсе", elapsed)
	}
	if err == nil && res.DSLError == "" {
		t.Fatalf("Приостановить(300) прошло успешно за %v — предела нет", elapsed)
	}
	msg := res.DSLError
	if err != nil {
		msg += " " + err.Error()
	}
	if !strings.Contains(msg, "врем") {
		t.Errorf("ошибка не объясняет причину (ожидалось про время выполнения): %q", msg)
	}

	// Главное: транзакция не осталась открытой. На SQLite соединение
	// единственное, поэтому висящая транзакция сделала бы недоступной всю базу —
	// следующий же запрос это покажет.
	done := make(chan error, 1)
	go func() {
		var n int
		done <- db.QueryRow(context.Background(), "SELECT COUNT(*) FROM долгий").Scan(&n)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("база недоступна после отрубленного хука: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("запрос к базе завис — транзакция хука осталась открытой")
	}
}

// Обратная сторона: при ненастроенном пределе поведение прежнее. Нулевое
// значение означает «предел не задан», и менять на нём поведение молча нельзя.
func TestХукПроведения_БезНастройкиПределаНеПоявляется(t *testing.T) {
	ctx, _, svc, doc := newSleepingDoc(t, 0)
	// Короткая пауза вместо 300 секунд: проверяем, что лимита нет, а не ждём его.
	svc.Reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: mustParseProc(t, `Процедура ОбработкаПроведения()
  Приостановить(0.2);
КонецПроцедуры`)},
	})
	res, err := svc.Save(ctx, SaveRequest{
		Entity: doc, ID: uuid.New(), IsNew: true, Action: "post",
		Fields: map[string]any{"Номер": "2"},
	})
	if err != nil {
		t.Fatalf("без настроенного предела запись упала: %v", err)
	}
	if res.DSLError != "" {
		t.Fatalf("без настроенного предела хук отрублен: %s", res.DSLError)
	}
}
