package cli

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Write with the record-set DSL and reopen with procrun/query. This covers the
// real row adapter and JSON contract, rather than only compiler metadata.
func TestQueryPeriodAliasCLIMatrix(t *testing.T) {
	oldLocation := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = oldLocation })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := t.TempDir()
		writeProcrunFixture(t, dir, "config/app.yaml", "name: period-alias-test\nversion: \"1.0\"\n")
		writeProcrunFixture(t, dir, "inforegs/Чтение.yaml", `name: Чтение
periodic: true
dimensions:
  - name: Заявка
    type: string
resources:
  - name: Отметка
    type: string
`)
		writeProcrunFixture(t, dir, "processors/Проверка.yaml", "name: Проверка\n")
		writeProcrunFixture(t, dir, "src/Проверка.proc.os", `Процедура Выполнить()
  Набор = РегистрыСведений.Чтение.СоздатьНаборЗаписей();
  Набор.Отбор.Заявка = "одна";
  Запись = Набор.Добавить();
  Запись.Заявка = "одна";
  Запись.Отметка = "прочитано";
  Запись.Период = Дата(2026, 10, 14, 12, 0, 0);
  Набор.Записать();
  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ Р.period КАК Период, Р.period КАК Момент ИЗ РегистрСведений.Чтение КАК Р";
  Для Каждого С Из Запрос.Выполнить() Цикл
    Сообщить("direct=" + Строка(С.Период = Запись.Период) + "/" + Строка(С.Период = С.Момент) + "/" + ТипЗнч(С.Период));
  КонецЦикла;
  Запрос.Текст = "SELECT MAX(Р.period) AS пЕрИоД, MAX(Р.period) AS Момент ИЗ РегистрСведений.Чтение КАК Р GROUP BY Р.Заявка ORDER BY Период";
  Для Каждого С Из Запрос.Выполнить() Цикл
    Сообщить("aggregate=" + Строка(ЗначениеЗаполнено(С.Период)) + "/" + Строка(С.Период = С.Момент));
  КонецЦикла;
КонецПроцедуры
`)
		cmd := userReferenceCLICommand(t, db, dir, false)
		if _, err := captureStdout(t, func() error { return migrateCmd.RunE(cmd, nil) }); err != nil {
			t.Fatal(err)
		}
		cmd.Flags().String("proc", "Проверка", "")
		cmd.Flags().StringArray("set", nil, "")
		cmd.Flags().StringArray("file", nil, "")
		out, err := captureStdout(t, func() error { return procrunCmd.RunE(cmd, nil) })
		if err != nil || !strings.Contains(out, "direct=true/true/Дата") || !strings.Contains(out, "aggregate=true/true") {
			t.Fatalf("procrun: %v\n%s", err, out)
		}
		args := []string{"query", "--project", dir, "--json", "--sql", "--limit", "0"}
		for _, flag := range []string{"sqlite", "db"} {
			value, err := cmd.Flags().GetString(flag)
			if err != nil {
				t.Fatal(err)
			}
			if value != "" {
				args = append(args, "--"+flag, value)
			}
		}
		for _, text := range []string{
			"ВЫБРАТЬ Р.period КАК Период, Р.period КАК Момент ИЗ РегистрСведений.Чтение КАК Р",
			"SELECT MAX(Р.period) AS Период, MAX(Р.period) AS Момент ИЗ РегистрСведений.Чтение КАК Р GROUP BY Р.Заявка ORDER BY Период",
		} {
			out, err := executeQueryCommand(t, append(append([]string{}, args...), text)...)
			if err != nil {
				t.Fatal(err)
			}
			var got queryOutput
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Columns, []string{"период", "момент"}) || len(got.Rows) != 1 {
				t.Fatalf("JSON columns/rows: %s", out)
			}
			row := got.Rows[0]
			period, ok := row["период"].(string)
			if !ok || !strings.HasPrefix(period, "2026-10-14") || period != row["момент"] {
				t.Fatalf("JSON values: %s", out)
			}
			if _, oldKey := row["period"]; oldKey {
				t.Fatalf("obsolete output key: %s", out)
			}
		}
		proj, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer proj.Close()
		rows, err := db.InfoRegList(context.Background(), proj.InfoRegisters[0], storage.RegFilter{})
		if err != nil || len(rows) != 1 || rows[0]["Отметка"] != "прочитано" {
			t.Fatalf("stored data changed: %+v, %v", rows, err)
		}
		if rows[0]["period_key"] != "2026-10-14T12:00:00Z" {
			t.Fatalf("stored period changed: %+v", rows)
		}
	})
}
