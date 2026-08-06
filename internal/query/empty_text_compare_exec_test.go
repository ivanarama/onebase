package query_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Сквозная проверка на реальных данных: запись с НЕЗАПОЛНЕННЫМ состоянием
// должна попадать в отбор «состояние <> Завершено». Это ровно та запись, ради
// которой пишут отчёт «что висит», — а в SQL NULL <> 'Завершено' даёт NULL, и
// раньше она молча выпадала.
func TestНезаполненноеСостояниеПопадаетВОтбор(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(ctx, `CREATE TABLE обращение (номер TEXT, состояниеобращения TEXT, количествоитогов NUMERIC)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{
		{"ОБР-001", "Открыто", 1},
		{"ОБР-002", "Завершено", 2},
		{"ОБР-003", nil, nil}, // создана мимо модуля: состояние не заполнено
	} {
		if _, err := db.Exec(ctx,
			"INSERT INTO обращение(номер, состояниеобращения, количествоитогов) VALUES(?, ?, ?)",
			row...); err != nil {
			t.Fatal(err)
		}
	}

	entity := &metadata.Entity{
		Name: "Обращение", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "СостояниеОбращения", Type: metadata.FieldType("enum:СостояниеОбращения"), EnumName: "СостояниеОбращения"},
			{Name: "КоличествоИтогов", Type: metadata.FieldTypeNumber},
		},
	}
	выполнить := func(текст string) []string {
		t.Helper()
		res, err := query.Compile(текст, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{entity}})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		rows, cols, err := db.RunQuery(ctx, res.SQL, res.Args)
		if err != nil {
			t.Fatalf("выполнение %s: %v", res.SQL, err)
		}
		var out []string
		for _, r := range rows {
			// Имя колонки результата — как его вернул SQL (номер), а не как
			// написано в тексте запроса.
			out = append(out, fmt.Sprint(r[cols[0]]))
		}
		return out
	}

	t.Run("незавершённые включают запись без состояния", func(t *testing.T) {
		got := выполнить(`ВЫБРАТЬ Номер ИЗ Документ.Обращение ГДЕ СостояниеОбращения <> "Завершено" УПОРЯДОЧИТЬ ПО Номер`)
		want := []string{"ОБР-001", "ОБР-003"}
		if len(got) != len(want) {
			t.Fatalf("получено %v, ожидалось %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("получено %v, ожидалось %v", got, want)
			}
		}
	})

	t.Run("отбор по пустому состоянию находит запись", func(t *testing.T) {
		got := выполнить(`ВЫБРАТЬ Номер ИЗ Документ.Обращение ГДЕ СостояниеОбращения = ""`)
		if len(got) != 1 || got[0] != "ОБР-003" {
			t.Fatalf("получено %v, ожидалось [ОБР-003]", got)
		}
	})

	t.Run("незаполненное число по-прежнему не равно нулю в отборе", func(t *testing.T) {
		// У числа пустое значение — 0, и «<> 0» для NULL не срабатывает: это
		// совпадает с прикладным смыслом и намеренно оставлено как было.
		got := выполнить(`ВЫБРАТЬ Номер ИЗ Документ.Обращение ГДЕ КоличествоИтогов <> 0 УПОРЯДОЧИТЬ ПО Номер`)
		want := []string{"ОБР-001", "ОБР-002"}
		if len(got) != len(want) {
			t.Fatalf("получено %v, ожидалось %v", got, want)
		}
	})
}
