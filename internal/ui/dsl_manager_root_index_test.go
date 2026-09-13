package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Индексный доступ к корням менеджеров (#1434). Симптом заявки: выражение
// Документы[ИмяТипа] компилировалось, но давало Неопределено, и универсальный
// цикл по списку типов приходилось заменять Соответствием из литералов.
func TestManagerRootIndexAccess(t *testing.T) {
	_, _, srv, _, _ := newPostingDoc(t)

	t.Run("документ по строке", func(t *testing.T) {
		msgs, err := runDSLBody(t, srv, `
  Имя = "ПоступлениеТоваров";
  М = Документы[Имя];
  Если М = Неопределено Тогда
    Сообщить("Неопределено");
  Иначе
    Сообщить("менеджер");
  КонецЕсли;`)
		if err != nil {
			t.Fatalf("прогон: %v", err)
		}
		if len(msgs) != 1 || msgs[0] != "менеджер" {
			t.Errorf("Документы[Имя] дал %v, ожидался менеджер", msgs)
		}
	})

	t.Run("регистр по строке", func(t *testing.T) {
		msgs, err := runDSLBody(t, srv, `
  Имя = "ОстаткиТоваров";
  Р = РегистрыНакопления[Имя];
  Если Р = Неопределено Тогда
    Сообщить("Неопределено");
  Иначе
    Сообщить("менеджер");
  КонецЕсли;`)
		if err != nil {
			t.Fatalf("прогон: %v", err)
		}
		if len(msgs) != 1 || msgs[0] != "менеджер" {
			t.Errorf("РегистрыНакопления[Имя] дал %v, ожидался менеджер", msgs)
		}
	})

	// Реестр ищет имя без учёта регистра, и индексный доступ обязан вести себя
	// так же, как точечный: иначе одна и та же строка работала бы в одном
	// месте и молчала в другом.
	t.Run("без учёта регистра", func(t *testing.T) {
		msgs, err := runDSLBody(t, srv, `
  М = Документы["поступлениетоваров"];
  Если М = Неопределено Тогда
    Сообщить("Неопределено");
  Иначе
    Сообщить("менеджер");
  КонецЕсли;`)
		if err != nil {
			t.Fatalf("прогон: %v", err)
		}
		if len(msgs) != 1 || msgs[0] != "менеджер" {
			t.Errorf("регистронезависимый поиск дал %v", msgs)
		}
	})

	// Опечатка обязана падать там, где написана, а не превращаться в
	// Неопределено и всплывать позже «методом у Неопределено».
	t.Run("опечатка — ошибка, а не Неопределено", func(t *testing.T) {
		msgs, err := runDSLBody(t, srv, `
  Попытка
    М = Документы["ТакогоНет"];
    Сообщить("без ошибки");
  Исключение
    Сообщить(ОписаниеОшибки());
  КонецПопытки;`)
		if err != nil {
			t.Fatalf("прогон: %v", err)
		}
		if len(msgs) != 1 || msgs[0] == "без ошибки" {
			t.Fatalf("неизвестное имя не дало ошибки: %v", msgs)
		}
		if !strings.Contains(msgs[0], "ТакогоНет") {
			t.Errorf("в сообщении нет имени, по которому искать: %q", msgs[0])
		}
	})

	// Индексная запись корню не открывается, и отказ говорит правду: молчаливое
	// false дало бы «неизвестный реквизит» про существующий менеджер.
	t.Run("индексная запись отклоняется", func(t *testing.T) {
		msgs, err := runDSLBody(t, srv, `
  Попытка
    Документы["ПоступлениеТоваров"] = 1;
    Сообщить("записалось");
  Исключение
    Сообщить(ОписаниеОшибки());
  КонецПопытки;`)
		if err != nil {
			t.Fatalf("прогон: %v", err)
		}
		if len(msgs) != 1 || msgs[0] == "записалось" {
			t.Fatalf("индексная запись прошла: %v", msgs)
		}
		if !strings.Contains(msgs[0], "индексная запись не поддерживается") {
			t.Errorf("сообщение не объясняет отказ: %q", msgs[0])
		}
	})
}

// Справочники — четвёртый корень менеджеров; у него тот же код, и проверяется
// он так же, чтобы «одинаковая семантика всех manager-root» не осталась
// обещанием в описании.
func TestCatalogsRootIndexAccess(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	контрагент := &metadata.Entity{
		Name: "Контрагент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: "string"}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{контрагент}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{контрагент}})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	srv := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	srv.entitySvc = srv.newEntityService(nil)

	msgs, err := runDSLBody(t, srv, `
  Имя = "Контрагент";
  С = Справочники[Имя];
  Если С = Неопределено Тогда
    Сообщить("Неопределено");
  Иначе
    Сообщить("менеджер");
  КонецЕсли;
  Попытка
    Н = Справочники["ТакогоНет"];
    Сообщить("без ошибки");
  Исключение
    Сообщить("ошибка");
  КонецПопытки;`)
	if err != nil {
		t.Fatalf("прогон: %v", err)
	}
	if len(msgs) != 2 || msgs[0] != "менеджер" || msgs[1] != "ошибка" {
		t.Errorf("Справочники[Имя] повели себя иначе, чем остальные корни: %v", msgs)
	}
}
