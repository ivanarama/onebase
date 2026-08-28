package query_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Сквозная проверка на реальных данных: запись с НЕЗАПОЛНЕННЫМ состоянием
// должна попадать в отбор «состояние <> Завершено». Это ровно та запись, ради
// которой пишут отчёт «что висит», — а в SQL NULL <> 'Завершено' даёт NULL, и
// раньше она молча выпадала.
//
// Тест матричный, потому что правило меняет исполняемый SQL: колонка уезжает в
// COALESCE, а типизация COALESCE на PostgreSQL строгая. Проверка одного диалекта
// показала бы зелёное, а расхождение вылезло бы в рантайме прикладного отчёта.
func TestНезаполненноеСостояниеПопадаетВОтбор(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		обращение := &metadata.Entity{
			Name: "ОбращениеПустое", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Метка", Type: metadata.FieldTypeString},
				{Name: "СостояниеОбращения", Type: metadata.FieldType("enum:СостояниеОбращения"), EnumName: "СостояниеОбращения"},
				{Name: "КоличествоИтогов", Type: metadata.FieldTypeNumber},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{обращение}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, поля := range []map[string]any{
			{"Метка": "ОБР-001", "СостояниеОбращения": "Открыто", "КоличествоИтогов": 1},
			{"Метка": "ОБР-002", "СостояниеОбращения": "Завершено", "КоличествоИтогов": 2},
			// Создана мимо модуля: состояние и число не заполнены — в БД NULL.
			{"Метка": "ОБР-003"},
		} {
			if err := db.Upsert(ctx, обращение.Name, uuid.New(), поля, обращение); err != nil {
				t.Fatalf("запись %v: %v", поля["Метка"], err)
			}
		}

		метки := func(t *testing.T, текст string) []string {
			t.Helper()
			res, err := query.Compile(текст, query.CompileOpts{
				Dialect:  db.Dialect(),
				Entities: []*metadata.Entity{обращение},
			})
			if err != nil {
				t.Fatalf("компиляция %q: %v", текст, err)
			}
			rows, cols, err := db.RunQuery(ctx, res.SQL, res.Args)
			if err != nil {
				t.Fatalf("выполнение %s: %v", res.SQL, err)
			}
			var out []string
			for _, r := range rows {
				// Имя колонки результата — как его вернул SQL (метка), а не как
				// написано в тексте запроса.
				out = append(out, fmt.Sprint(r[cols[0]]))
			}
			return out
		}
		сверить := func(t *testing.T, got, want []string) {
			t.Helper()
			if len(got) != len(want) {
				t.Fatalf("получено %v, ожидалось %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("получено %v, ожидалось %v", got, want)
				}
			}
		}

		t.Run("незавершённые включают запись без состояния", func(t *testing.T) {
			сверить(t, метки(t, `ВЫБРАТЬ Метка ИЗ Документ.ОбращениеПустое
				ГДЕ СостояниеОбращения <> "Завершено" УПОРЯДОЧИТЬ ПО Метка`),
				[]string{"ОБР-001", "ОБР-003"})
		})

		// Тот же отбор, записанный через алиас источника. В запросе с соединением
		// алиас обязателен, поэтому запись без него и с ним обязаны отдавать один
		// набор строк: расхождение здесь хуже исходного дефекта — к потере записей
		// добавилась бы зависимость результата от формы записи условия.
		t.Run("через алиас источника отбор тот же", func(t *testing.T) {
			сверить(t, метки(t, `ВЫБРАТЬ Т.Метка ИЗ Документ.ОбращениеПустое КАК Т
				ГДЕ Т.СостояниеОбращения <> "Завершено" УПОРЯДОЧИТЬ ПО Т.Метка`),
				[]string{"ОБР-001", "ОБР-003"})
		})

		t.Run("отбор по пустому состоянию находит запись", func(t *testing.T) {
			сверить(t, метки(t, `ВЫБРАТЬ Метка ИЗ Документ.ОбращениеПустое ГДЕ СостояниеОбращения = ""`),
				[]string{"ОБР-003"})
			сверить(t, метки(t, `ВЫБРАТЬ Т.Метка ИЗ Документ.ОбращениеПустое КАК Т ГДЕ Т.СостояниеОбращения = ""`),
				[]string{"ОБР-003"})
		})

		t.Run("незаполненное число по-прежнему не равно нулю в отборе", func(t *testing.T) {
			// У числа пустое значение — 0, и «<> 0» для NULL не срабатывает: это
			// совпадает с прикладным смыслом и намеренно оставлено как было.
			сверить(t, метки(t, `ВЫБРАТЬ Метка ИЗ Документ.ОбращениеПустое
				ГДЕ КоличествоИтогов <> 0 УПОРЯДОЧИТЬ ПО Метка`),
				[]string{"ОБР-001", "ОБР-002"})
		})
	})
}
