package ui

// Стандартные реквизиты объекта в DSL. У объекта документа из
// Документы.X.Создать() и Ссылка.ПолучитьОбъект() Проведен и ПометкаУдаления
// читались как Неопределено: объект несёт только объявленные реквизиты.
//
// Собственная ссылка объекта отдавалась сырой. После Записать() в объекте лежала
// ссылка без менеджера, после хука записи или проведения — привязанная к уже
// закрытой транзакции хука, а у объекта из Ссылка.ПолучитьОбъект() её не было
// вовсе. Поэтому Д.Ссылка.ПолучитьОбъект() после Д.Провести() падал на
// «transaction has already been committed», а у справочника — на «ссылка не
// привязана к менеджеру» или «context canceled».

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

const standardAttrsPostingOS = `Процедура ОбработкаПроведения()
  // Собственная ссылка внутри хука по-прежнему читает в его транзакции.
  Об = this.Ссылка.ПолучитьОбъект();
  Сообщить("хук: " + Об.Комментарий);
КонецПроцедуры
`

const standardAttrsProcOS = `Функция Флаг(Значение)
  Если ТипЗнч(Значение) <> Тип("Булево") Тогда
    Возврат "не булево: " + ТипЗнч(Значение);
  КонецЕсли;
  Если Значение Тогда
    Возврат "да";
  КонецЕсли;
  Возврат "нет";
КонецФункции

Процедура Выполнить()
  Д = Документы.Док.Создать();
  Д.Дата = ТекущаяДата();
  Д.Комментарий = "первый";
  Сообщить("новый: " + Флаг(Д.Проведен) + " " + Флаг(Д.ПометкаУдаления));

  Д.Записать();
  Сообщить("записан: " + Флаг(Д.Ссылка.ПолучитьОбъект().Проведен));

  Д.Провести();
  Сообщить("проведён: " + Флаг(Д.Проведен));

  Об = Д.Ссылка.ПолучитьОбъект();
  Сообщить("загружен: " + Флаг(Об.Проведен) + " " + Флаг(Об.ПометкаУдаления) + " " + Флаг(Об.Ссылка = Д.Ссылка));
  Сообщить("ссылка загруженного: " + Об.Ссылка.ПолучитьОбъект().Комментарий);

  Документы.Док.ОтменитьПроведение(Д.Ссылка);
  Об.Прочитать();
  Сообщить("отменён: " + Флаг(Об.Проведен) + " " + Флаг(Об["Проведен"]));

  Документы.Док.ПометитьНаУдаление(Д.Ссылка);
  Сообщить("помечен: " + Флаг(Д.Ссылка.ПолучитьОбъект().ПометкаУдаления));

  // Откат транзакции возвращает и признак объекта.
  Д2 = Документы.Док.Создать();
  Д2.Дата = ТекущаяДата();
  Д2.Комментарий = "откат";
  НачатьТранзакцию();
  Д2.Провести();
  ОтменитьТранзакцию();
  Сообщить("после отката: " + Флаг(Д2.Проведен));
КонецПроцедуры
`

const selfRefCatalogHookOS = `Процедура ПриЗаписи()
  Если this.Наименование = "" Тогда
    ВызватьИсключение "пусто";
  КонецЕсли;
КонецПроцедуры
`

const selfRefCatalogProcOS = `Процедура Выполнить()
  К = Справочники.Кат.Создать();
  К.Наименование = "а";
  К.Записать();
  Сообщить("после записи: " + К.Ссылка.ПолучитьОбъект().Наименование);

  О = К.Ссылка.ПолучитьОбъект();
  Сообщить("загружен: " + О.Ссылка.ПолучитьОбъект().Наименование);
  О.Прочитать();
  Сообщить("перечитан: " + О.Ссылка.ПолучитьОбъект().Наименование);

  // Ссылка, которую вернул Записать() внутри транзакции, переживает её фиксацию.
  НачатьТранзакцию();
  К2 = Справочники.Кат.Создать();
  К2.Наименование = "б";
  С2 = К2.Записать();
  ЗафиксироватьТранзакцию();
  Сообщить("после транзакции: " + С2.ПолучитьОбъект().Наименование);
КонецПроцедуры
`

func TestDSLDocumentStandardAttributes(t *testing.T) {
	dir := t.TempDir()
	mk := func(sub string) string {
		p := filepath.Join(dir, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		return p
	}
	docDir := mk("documents")
	procDir := mk("processors")
	srcDir := mk("src")

	doc := "name: Док\nposting: true\nfields:\n" +
		"  - {name: Дата, type: date}\n" +
		"  - {name: Комментарий, type: string}\n"
	if err := os.WriteFile(filepath.Join(docDir, "док.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "док.posting.os"), []byte(standardAttrsPostingOS), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "проба.yaml"), []byte("name: Проба\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "проба.proc.os"), []byte(standardAttrsProcOS), 0o644); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"новый: нет нет",
		"записан: нет",
		"хук: первый",
		"проведён: да",
		"загружен: да нет да",
		"ссылка загруженного: первый",
		"отменён: нет нет",
		"помечен: да",
		"хук: откат",
		"после отката: нет",
	}
	if got := runSelfRefProc(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("сообщения:\n got %q\nwant %q", got, want)
	}
}

// Хук записи справочника исполняется со своим «ЭтотОбъект» и привязывает ссылки
// объекта к транзакции хука, поэтому проверяются оба пути: с хуком и без.
func TestDSLCatalogSelfReferenceAfterWrite(t *testing.T) {
	for _, withHook := range []bool{false, true} {
		name := "без хука"
		if withHook {
			name = "с ПриЗаписи"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mk := func(sub string) string {
				p := filepath.Join(dir, sub)
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", sub, err)
				}
				return p
			}
			catDir := mk("catalogs")
			procDir := mk("processors")
			srcDir := mk("src")

			cat := "name: Кат\nfields:\n  - {name: Наименование, type: string}\n"
			if err := os.WriteFile(filepath.Join(catDir, "кат.yaml"), []byte(cat), 0o644); err != nil {
				t.Fatal(err)
			}
			if withHook {
				if err := os.WriteFile(filepath.Join(srcDir, "кат.os"), []byte(selfRefCatalogHookOS), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(procDir, "проба.yaml"), []byte("name: Проба\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(srcDir, "проба.proc.os"), []byte(selfRefCatalogProcOS), 0o644); err != nil {
				t.Fatal(err)
			}

			want := []string{
				"после записи: а",
				"загружен: а",
				"перечитан: а",
				"после транзакции: б",
			}
			if got := runSelfRefProc(t, dir); !reflect.DeepEqual(got, want) {
				t.Fatalf("сообщения:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// runSelfRefProc загружает проект и прогоняет обработку «Проба» тем же путём,
// что procrun.
func runSelfRefProc(t *testing.T, dir string) []string {
	t.Helper()
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	msgs, runErr, err := RunProcessorOffline(ctx, proj, db, "Проба", nil, nil)
	if err != nil {
		t.Fatalf("RunProcessorOffline: %v", err)
	}
	if runErr != nil {
		t.Fatalf("обработка упала: %v\nсообщения: %q", runErr, msgs)
	}
	return msgs
}
