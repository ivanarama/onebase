package dslvars

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Присваивание константы обязано доезжать до базы.
//
// Объект `Константы` был обычной картой-снимком, поэтому
// `Константы.Имя = Значение` меняло значение только в памяти прогона: код
// отрабатывал без ошибки, чтение сразу после записи возвращало новое значение —
// и всё. Следующий запуск снова видел старое. Ровно на этом молча не работало
// аварийное отключение интеграции: обработка отчитывалась об успехе, а
// константа оставалась включённой (#719).
//
// Проверяем через ту же сборку переменных, которой пользуются UI и планировщик
// (Common.Build), и читаем результат ЗАНОВО из хранилища — не из снимка.

func constantsFixture(t *testing.T) (*storage.DB, *runtime.Registry, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "consts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	consts := []*metadata.Constant{{Name: "LLMEnabled", Type: metadata.FieldTypeBool}}
	if err := db.MigrateConstants(ctx, consts); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Constants: consts})
	return db, reg, ctx
}

func runConstantsProc(t *testing.T, db *storage.DB, reg *runtime.Registry, ctx context.Context, src string) error {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "t.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	in := interpreter.New()
	vars := Common{Ctx: ctx, Reg: reg, Store: db}.Build()
	obj := runtime.NewObject("T", metadata.KindCatalog)
	return in.Run(prog.Procedures[0], obj, vars)
}

func TestКонстанты_ПрисваиваниеДоезжаетДоБазы(t *testing.T) {
	db, reg, ctx := constantsFixture(t)

	if err := runConstantsProc(t, db, reg, ctx, `Процедура Тест()
		Константы.LLMEnabled = Истина;
	КонецПроцедуры`); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Читаем заново из хранилища: снимок прогона тут ничего не доказывает.
	got, err := db.GetConstant(ctx, "LLMEnabled")
	if err != nil {
		t.Fatalf("GetConstant: %v", err)
	}
	if got != true {
		t.Fatalf("в базе %#v, ждали true: присваивание не сохранилось", got)
	}
}

// Чтение сразу после записи в том же прогоне обязано видеть записанное — иначе
// сквозная запись сломала бы привычное поведение снимка.
func TestКонстанты_ЧтениеПослеЗаписиВТомЖеПрогоне(t *testing.T) {
	db, reg, ctx := constantsFixture(t)

	prog, err := parser.New(lexer.New(`Функция Тест()
		Константы.LLMEnabled = Истина;
		Возврат Константы.LLMEnabled;
	КонецФункции`, "t.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	in := interpreter.New()
	var res any
	if err := in.RunWithResult(prog.Procedures[0], runtime.NewObject("T", metadata.KindCatalog),
		&res, Common{Ctx: ctx, Reg: reg, Store: db}.Build()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if res != true {
		t.Fatalf("после записи прочитано %#v, ждали true", res)
	}
}

// Опечатка в имени — ошибка, а не тихое заведение ключа в памяти: отличить
// «выключил не ту константу» от «выключил несуществующую» иначе нечем.
func TestКонстанты_НеизвестноеИмяЭтоОшибка(t *testing.T) {
	db, reg, ctx := constantsFixture(t)

	err := runConstantsProc(t, db, reg, ctx, `Процедура Тест()
		Константы.LLMEnabledd = Истина;
	КонецПроцедуры`)
	if err == nil {
		t.Fatal("опечатка в имени константы прошла молча")
	}
	// Интерпретатор приводит имя поля к нижнему регистру ещё до Set
	// (interpreter.assign), поэтому исходного написания у прокси уже нет —
	// сравниваем без учёта регистра.
	if !strings.Contains(strings.ToLower(err.Error()), "llmenabledd") {
		t.Errorf("в ошибке нет имени константы: %v", err)
	}
	if !strings.Contains(err.Error(), "LLMEnabled") {
		t.Errorf("в ошибке нет подсказки с известными именами: %v", err)
	}
}
