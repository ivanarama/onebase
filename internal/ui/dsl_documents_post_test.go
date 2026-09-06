package ui

// Документы.X.Провести(Ссылка) — #1168.
//
// У менеджера документов были ОтменитьПроведение/ПометитьНаУдаление/СнятьПометку,
// но не было Провести: вызов уходил в откат на модуль менеджера, там процедуры не
// находилось, и метод молча возвращал Неопределено. Документ оставался
// непроведённым, ошибки не было — отладка уходила в регистр и хук проведения.
//
// Тесты идут публичным путём: DSL-исходник исполняется через
// buildDSLVarsWithMessagesTx + interp.Run (runDSLBody) — так же, как его
// исполняет обработка, — а не вызовом postRef напрямую. Правило CLAUDE.md,
// повод — #611: зелёный тест на функции, которую боевой путь не зовёт, хуже
// отсутствующего.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Регрессия #1168: Документы.X.Провести(Ссылка) действительно проводит документ —
// признак проведения выставлен, движения ОбработкаПроведения записаны.
func TestDocsRoot_PostMethod_ПроводитПоСсылке(t *testing.T) {
	ctx, db, s, _, _ := newPostingDoc(t)

	if _, err := runDSLBody(t, s, `
  Док = Документы.ПоступлениеТоваров.Создать();
  Док.Номер = "ПОС-777";
  Стр = Док.Товары.Добавить();
  Стр.Номенклатура = "Тумбочка";
  Стр.Количество = 100;
  Реф = Док.Записать();
  Документы.ПоступлениеТоваров.Провести(Реф);
`); err != nil {
		t.Fatalf("Документы.X.Провести(Ссылка): %v", err)
	}

	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM поступлениетоваров WHERE номер = ?", "ПОС-777").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Error("posted=true ожидался после Документы.X.Провести(Ссылка); до #1168 вызов молча возвращал Неопределено")
	}

	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 1 {
		t.Errorf("ожидалось 1 движение ОбработкаПроведения, получили %d", mov)
	}
}

// Английский алиас Post(Ссылка) — как у остальных методов менеджера.
func TestDocsRoot_PostMethod_АнглийскийАлиас(t *testing.T) {
	ctx, db, s, _, _ := newPostingDoc(t)

	if _, err := runDSLBody(t, s, `
  Док = Документы.ПоступлениеТоваров.Создать();
  Док.Номер = "ПОС-778";
  Стр = Док.Товары.Добавить();
  Стр.Номенклатура = "Тумбочка";
  Стр.Количество = 100;
  Документы.ПоступлениеТоваров.Post(Док.Записать());
`); err != nil {
		t.Fatalf("Документы.X.Post(Ссылка): %v", err)
	}

	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM поступлениетоваров WHERE номер = ?", "ПОС-778").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Error("posted=true ожидался после Документы.X.Post(Ссылка)")
	}
}

// Аргумент не ссылка / аргумента нет — понятная ошибка, а не тишина: заявка ровно
// про то, что молчание дороже падения.
func TestDocsRoot_PostMethod_НеСсылкаИБезАргумента(t *testing.T) {
	for _, tc := range []struct {
		имя  string
		тело string
		хочу string
	}{
		{"строка вместо ссылки", `Документы.ПоступлениеТоваров.Провести("ПОС-001");`, "ожидается ссылка"},
		{"без аргумента", `Документы.ПоступлениеТоваров.Провести();`, "не передана ссылка"},
	} {
		t.Run(tc.имя, func(t *testing.T) {
			_, _, s, _, _ := newPostingDoc(t)
			_, err := runDSLBody(t, s, tc.тело)
			if err == nil {
				t.Fatalf("ожидалась ошибка %q, получили тишину", tc.хочу)
			}
			if !strings.Contains(err.Error(), tc.хочу) {
				t.Errorf("ошибка %q не содержит %q", err.Error(), tc.хочу)
			}
		})
	}
}

// Помеченный на удаление документ провести нельзя — инвариант тот же, что у
// объектного пути и у кнопки в списке; ошибка ловится Попыткой.
func TestDocsRoot_PostMethod_ПомеченныйНаУдаление(t *testing.T) {
	ctx, db, s, _, _ := newPostingDoc(t)

	msgs, err := runDSLBody(t, s, `
  Док = Документы.ПоступлениеТоваров.Создать();
  Док.Номер = "ПОС-779";
  Стр = Док.Товары.Добавить();
  Стр.Номенклатура = "Тумбочка";
  Стр.Количество = 100;
  Реф = Док.Записать();
  Документы.ПоступлениеТоваров.ПометитьНаУдаление(Реф);
  Попытка
    Документы.ПоступлениеТоваров.Провести(Реф);
  Исключение
    Сообщить("отказано");
  КонецПопытки;
`)
	if err != nil {
		t.Fatalf("сценарий с Попыткой: %v", err)
	}
	if len(msgs) == 0 || msgs[len(msgs)-1] != "отказано" {
		t.Errorf("ожидался отказ проведения помеченного документа, сообщения: %v", msgs)
	}

	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM поступлениетоваров WHERE номер = ?", "ПОС-779").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Error("помеченный на удаление документ не должен оказаться проведённым")
	}
	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 0 {
		t.Errorf("движений быть не должно, получили %d", mov)
	}
}

// Прикладная процедура «Провести» в модуле менеджера СТАРШЕ встроенного метода.
//
// Пока метода не было, одноимённая процедура в X.manager.os была единственным
// способом закрыть дыру — именно так её и обходили. Встроенный case стоит до
// отката на модуль менеджера, поэтому без явной уступки он перекрыл бы такую
// процедуру молча: конфигурация продолжила бы «работать», выполняя другой код.
// Заявка #1168 — про молчаливую подмену поведения, и чинить её молчаливой
// подменой поведения нельзя.
func TestDocsRoot_PostMethod_ПрикладнаяПроцедураСтарше(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "Счёт",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	// Функция, а не процедура: Сообщить внутри модуля менеджера уходит в
	// собственный набор переменных callManagerProc и до наших сообщений не
	// доезжает, поэтому «кто отработал» спрашиваем возвращённым значением.
	mgrSrc := `Функция Провести(Ссылка)
  Возврат "модуль менеджера";
КонецФункции`

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:        []*metadata.Entity{doc},
		ManagerPrograms: map[string]*ast.Program{"Счёт": mustParse(t, mgrSrc)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	msgs, err := runDSLBody(t, s, `
  Док = Документы.Счёт.Создать();
  Док.Номер = "С-001";
  Сообщить(Документы.Счёт.Провести(Док.Записать()));
`)
	if err != nil {
		t.Fatalf("Документы.Счёт.Провести(Ссылка): %v", err)
	}
	if len(msgs) == 0 || msgs[len(msgs)-1] != "модуль менеджера" {
		t.Fatalf("встроенный метод перекрыл прикладную процедуру модуля менеджера; сообщения: %v", msgs)
	}

	// Прикладная процедура ничего не проводила — значит и признак проведения
	// остался снятым: встроенный путь не отработал «заодно».
	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM счёт WHERE номер = ?", "С-001").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Error("проведение выполнил встроенный метод, хотя вызвана должна быть процедура модуля менеджера")
	}
}
