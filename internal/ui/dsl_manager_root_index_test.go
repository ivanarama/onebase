package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
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

// #1732: запуск настоящей обработки закрепляет не только получение менеджера,
// но и операции с сохранёнными данными после индексного доступа к каждому корню.
func TestManagerRootIndexProcessorOperations(t *testing.T) {
	msgs := runManagerRootIndexProcessor(t, `
  ИмяДокумента = "Док";
  Док = Документы[ИмяДокумента].Создать();
  Док.Номер = "Д-1";
  Док.Дата = ТекущаяДата();
  Док.Комментарий = "исходный";
  Док.Записать();
  ИдДокумента = Док.Ссылка.УникальныйИдентификатор();
  Объект = Документы[ИмяДокумента].НайтиПоИдентификатору(ИдДокумента).ПолучитьОбъект();
  Объект.Комментарий = "перечитан";
  Объект.Записать();
  Сообщить(Документы[ИмяДокумента].НайтиПоИдентификатору(ИдДокумента).ПолучитьОбъект().Комментарий);

  ИмяСправочника = "Кат";
  Кат = Справочники[ИмяСправочника].Создать();
  Кат.Наименование = "товар";
  Кат.Записать();
  ИдСправочника = Кат.Ссылка.УникальныйИдентификатор();
  Объект = Справочники[ИмяСправочника].НайтиПоИдентификатору(ИдСправочника).ПолучитьОбъект();
  Объект.Наименование = "переименован";
  Объект.Записать();
  Сообщить(Справочники[ИмяСправочника].НайтиПоИдентификатору(ИдСправочника).ПолучитьОбъект().Наименование);

  Док = Документы[ИмяДокумента].НайтиПоИдентификатору(ИдДокумента).ПолучитьОбъект();
  Док.Провести();
  ИмяРегистра = "Остатки";
  Остатки = РегистрыНакопления[ИмяРегистра].Остатки();
  Сообщить(Строка(Остатки.Количество()) + ":" + Остатки[0].Товар + ":" + Строка(Остатки[0].Количество));
  ДвиженияДокумента = РегистрыНакопления[ИмяРегистра].ВыбратьПоРегистратору(Док.Ссылка);
  Сообщить(Строка(ДвиженияДокумента.Количество()) + ":" + ДвиженияДокумента[0].Товар);

  ИмяСведений = "Состояния";
  Запись = РегистрыСведений[ИмяСведений].СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Состояние = "готов";
  Запись.Записать();
  Прочитанная = РегистрыСведений[ИмяСведений].СоздатьМенеджерЗаписи();
  Прочитанная.Узел = "N1";
  Если Прочитанная.Прочитать() Тогда
    Сообщить(Прочитанная.Состояние);
  Иначе
    Сообщить("не найдено");
  КонецЕсли;`)
	want := []string{"перечитан", "переименован", "1:товар:7", "1:товар", "готов"}
	if !reflect.DeepEqual(msgs, want) {
		t.Fatalf("операции индексных менеджеров: got %q, want %q", msgs, want)
	}
}

func TestManagerRootIndexProcessorErrors(t *testing.T) {
	for _, root := range []struct{ name, entity string }{
		{"Документы", "Док"},
		{"Справочники", "Кат"},
		{"РегистрыНакопления", "Остатки"},
		{"РегистрыСведений", "Состояния"},
	} {
		t.Run(root.name, func(t *testing.T) {
			var body strings.Builder
			var want []string
			for _, index := range []string{"42", "Истина", "Неопределено"} {
				for _, operation := range []struct{ statement, message string }{
					{fmt.Sprintf("М = %s[%s];", root.name, index), "Имя реквизита в индексном чтении должно быть строкой"},
					{fmt.Sprintf("%s[%s] = 1;", root.name, index), "Имя реквизита в индексной записи должно быть строкой"},
				} {
					fmt.Fprintf(&body, "Попытка\n%s\nСообщить(\"без ошибки\");\nИсключение\nСообщить(ОписаниеОшибки());\nКонецПопытки;\n", operation.statement)
					want = append(want, operation.message)
				}
			}
			fmt.Fprintf(&body, `
  Попытка
    М = %s["ТакогоНет"];
    Сообщить("без ошибки");
  Исключение
    Сообщить(ОписаниеОшибки());
  КонецПопытки;
  Попытка
    %s["%s"] = 1;
    Сообщить("записалось");
  Исключение
    Сообщить(ОписаниеОшибки());
  КонецПопытки;`, root.name, root.name, root.entity)
			want = append(want, "ТакогоНет", "индексная запись не поддерживается")
			msgs := runManagerRootIndexProcessor(t, body.String())
			if len(msgs) != len(want) {
				t.Fatalf("сообщения отказов: got %q, want %q", msgs, want)
			}
			for i, text := range want {
				if !strings.Contains(msgs[i], text) {
					t.Errorf("отказ %d: got %q, want %q", i, msgs[i], text)
				}
			}
		})
	}
}

func runManagerRootIndexProcessor(t *testing.T, body string) []string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"documents/док.yaml":      "name: Док\nposting: true\nfields:\n  - {name: Номер, type: string}\n  - {name: Дата, type: date}\n  - {name: Комментарий, type: string}\n",
		"catalogs/кат.yaml":       "name: Кат\nfields:\n  - {name: Наименование, type: string}\n",
		"registers/остатки.yaml":  "name: Остатки\ndimensions:\n  - {name: Товар, type: string}\nresources:\n  - {name: Количество, type: number}\n",
		"inforegs/состояния.yaml": "name: Состояния\ndimensions:\n  - {name: Узел, type: string}\nresources:\n  - {name: Состояние, type: string}\n",
		"processors/проба.yaml":   "name: Проба\n",
		"src/проба.proc.os":       "Процедура Выполнить()\n" + body + "\nКонецПроцедуры\n",
		"src/док.posting.os": `Процедура ОбработкаПроведения()
  Движение = Движения.Остатки.Добавить();
  Движение.ВидДвижения = "Приход";
  Движение.Товар = "товар";
  Движение.Количество = 7;
КонецПроцедуры`,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		t.Fatal(err)
	}
	msgs, runErr, err := RunProcessorOffline(ctx, proj, db, "Проба", nil, nil)
	if err != nil || runErr != nil {
		t.Fatalf("RunProcessorOffline: setup=%v, execution=%v, messages=%q", err, runErr, msgs)
	}
	return msgs
}
