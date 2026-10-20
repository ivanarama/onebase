package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the registered migrate/procrun path on both databases, including
// re-opening the database between commands (not a detached row adapter).
func TestProcrunInfoRegRowIndexMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := t.TempDir()
		writeProcrunFixture(t, dir, "config/app.yaml", "name: row-index-test\nversion: \"1.0\"\n")
		writeProcrunFixture(t, dir, "catalogs/Клиент.yaml", "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n")
		for _, periodic := range []bool{false, true} {
			name := "БезПериода"
			period := ""
			if periodic {
				name = "СПериодом"
				period = `Стр["пЕрИоД"] = Дата(2026, 10, 14, 12, 0, 0);`
			}
			writeProcrunFixture(t, dir, "inforegs/"+name+".yaml", fmt.Sprintf(`name: %s
periodic: %t
dimensions:
  - name: Ключ
    type: string
  - name: Клиент
    type: reference:Клиент
resources:
  - name: Отметка
    type: string
  - name: Число
    type: number
  - name: Флаг
    type: bool
  - name: Дата
    type: date
  - name: ПустаяСсылка
    type: reference:Клиент
`, name, periodic))
			writeProcrunFixture(t, dir, "processors/"+name+".yaml", "name: "+name+"\n")
			body := fmt.Sprintf(`Процедура Выполнить()
  Клиент = Справочники.Клиент.Создать();
  Клиент.Наименование = "%s";
  Клиент.Записать();
  Ссылка = Справочники.Клиент.НайтиПоНаименованию("%s");
  Менеджер = РегистрыСведений.%s;
  Н = Менеджер.СоздатьНаборЗаписей();
  Н.Отбор.Ключ = "вне";
  Стр = Н.Добавить();
  Стр["кЛиЕнТ"] = Ссылка;
  Стр["Отметка"] = "сохранить";
  %s
  Н.Записать();
  Н.Отбор.Ключ = "внутри";
  Н.Очистить();
  Стр = Н.Добавить();
  Стр["кЛюЧ"] = "внутри";
  Стр["Клиент"] = Ссылка;
  Стр["оТмЕтКа"] = "добавлено";
  %s
  Сообщить("empty=" + ТипЗнч(Стр["Число"]) + "/" + Строка(Стр["Число"] = Стр.Число));
  Сообщить("bool=" + ТипЗнч(Стр["Флаг"]) + "/" + Строка(Стр["Флаг"] = Стр.Флаг));
  Сообщить("date=" + ТипЗнч(Стр["Дата"]) + "/" + Строка(Стр["Дата"] = Стр.Дата));
  Сообщить("ref=" + Строка(Стр["Клиент"] = Ссылка));
  Сообщить("empty-ref=" + Строка(ТипЗнч(Стр["ПустаяСсылка"]) = ТипЗнч(Стр.ПустаяСсылка)) + "/" + Строка(ЗначениеЗаполнено(Стр["ПустаяСсылка"])));
  Сообщить("resolved=" + ЗначениеРеквизитаОбъекта(Стр["Клиент"], "Наименование"));
  Н.Записать();
  Н.Прочитать();
  Стр = Н.Получить(0);
  Стр["Отметка"] = Стр["Отметка"] + "|получено";
  Для Каждого Запись Из Н Цикл
    Запись["Отметка"] = Запись["Отметка"] + "|цикл";
    Сообщить("loaded-empty=" + ТипЗнч(Запись["Число"]) + "/" + Строка(Запись["Число"] = 0));
    Сообщить("loaded-ref=" + Строка(Запись["Клиент"] = Ссылка));
  КонецЦикла;
  Н.Записать();
  Н.Прочитать();
  Сообщить("saved=" + Н.Получить(0)["Отметка"]);
  Стр = Н.Получить(0);
`, name, name, name, period, period)
			if periodic {
				body += `Сообщить("period=" + Строка(Стр["Период"] = Дата(2026, 10, 14, 12, 0, 0)));` + "\n"
			}
			for _, expr := range []string{`Х = Стр["Опечатка"];`, `Стр["Опечатка"] = 1;`, `Х = Стр[1];`, `Стр[1] = 1;`} {
				body += "Попытка\n" + expr + "\nСообщить(\"не поймано\");\nИсключение\nСообщить(\"поймано\");\nКонецПопытки;\n"
			}
			if !periodic {
				body += `Попытка
  Х = Стр["Период"];
  Сообщить("не поймано");
Исключение
  Сообщить("нет периода");
КонецПопытки;
`
			}
			// A raw key created through the existing dotted contract is still
			// not a declared field; presence in the map must not grant index access.
			body += `Стр.Опечатка = 1;
Попытка
  Х = Стр["Опечатка"];
  Сообщить("не поймано");
Исключение
  Сообщить("не объявлено");
КонецПопытки;
КонецПроцедуры
`
			writeProcrunFixture(t, dir, "src/"+name+".proc.os", body)
		}
		if _, err := captureStdout(t, func() error {
			return migrateCmd.RunE(userReferenceCLICommand(t, db, dir, false), nil)
		}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"БезПериода", "СПериодом"} {
			cmd := userReferenceCLICommand(t, db, dir, false)
			cmd.Flags().String("proc", name, "")
			cmd.Flags().StringArray("set", nil, "")
			cmd.Flags().StringArray("file", nil, "")
			out, err := captureStdout(t, func() error { return procrunCmd.RunE(cmd, nil) })
			if err != nil {
				t.Fatalf("%s: %v\n%s", name, err, out)
			}
			for _, want := range []string{"empty=Число/true", "bool=Булево/true", "date=Дата/true", "ref=true", "empty-ref=true/false", "resolved=" + name, "loaded-empty=Число/true", "loaded-ref=true", "saved=добавлено|получено|цикл", "не объявлено"} {
				if !strings.Contains(out, want) {
					t.Fatalf("%s: missing %q in %q", name, want, out)
				}
			}
			if strings.Contains(out, "не поймано") || strings.Count(out, "поймано") != 4 {
				t.Fatalf("invalid field/index accepted: %q", out)
			}
			if name == "СПериодом" && !strings.Contains(out, "period=true") || name == "БезПериода" && !strings.Contains(out, "нет периода") {
				t.Fatalf("period contract: %q", out)
			}
		}
		proj, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer proj.Close()
		for _, ir := range proj.InfoRegisters {
			rows, err := db.InfoRegList(context.Background(), ir, storage.RegFilter{})
			if err != nil || len(rows) != 2 {
				t.Fatalf("rows: %+v, %v", rows, err)
			}
			for _, row := range rows {
				want := "сохранить"
				if row["Ключ"] == "внутри" {
					want = "добавлено|получено|цикл"
				}
				if row["Отметка"] != want || row["Число"] != nil || row["Флаг"] != nil || row["Дата"] != nil || row["ПустаяСсылка"] != nil {
					t.Fatalf("index write changed another slice or materialized NULL: %+v", row)
				}
			}
		}
	})
}
