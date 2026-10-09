package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestPositiveTextEqualityPreservesRows(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "ИндексныйОтбор", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Метка", Type: metadata.FieldTypeString},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldType("enum:Статусы"), EnumName: "Статусы"},
		}}
		ents := []*metadata.Entity{ent}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		for _, label := range []string{"NULL", "EMPTY", "X", "Y"} {
			fields := map[string]any{"Метка": label}
			if label != "NULL" {
				value := label
				if label == "EMPTY" {
					value = ""
				}
				fields["Текст"], fields["Статус"] = value, value
			}
			if err := db.Upsert(ctx, ent.Name, uuid.New(), fields, ent); err != nil {
				t.Fatal(err)
			}
		}
		compile := func(t *testing.T, text string, params map[string]any) query.Result {
			t.Helper()
			result, err := query.Compile(text, query.CompileOpts{Dialect: db.Dialect(), Entities: ents, Params: params})
			if err != nil {
				t.Fatalf("compile %s: %v", text, err)
			}
			return result
		}
		cases := []struct {
			name, predicate string
			want            []string
			coalesces       int
			params          map[string]any
		}{
			{"right literal", `Т.Текст = "X"`, []string{"X"}, 0, nil},
			{"left literal", `"X" = Т.Текст`, []string{"X"}, 0, nil},
			{"enum", `Т.Статус = "X"`, []string{"X"}, 0, nil},
			{"boolean parentheses", `(((Т.Текст = "X")))`, []string{"X"}, 0, nil},
			{"and", `Т.Текст = "X" И Т.Статус <> "Y"`, []string{"X"}, 1, nil},
			{"or includes null", `Т.Текст = "X" ИЛИ Т.Статус = ""`, []string{"EMPTY", "NULL", "X"}, 1, nil},
			{"and or precedence", `Т.Текст = "X" И Т.Статус = "X" ИЛИ Т.Текст = "Y"`, []string{"X", "Y"}, 0, nil},
			{"grouped or", `(Т.Текст = "X" ИЛИ Т.Текст = "Y") И Т.Статус <> "Y"`, []string{"X"}, 1, nil},
			{"not", `НЕ (Т.Текст = "X")`, []string{"EMPTY", "NULL", "Y"}, 1, nil},
			{"not without parentheses", `НЕ Т.Текст = "X"`, []string{"EMPTY", "NULL", "Y"}, 1, nil},
			{"double not", `НЕ НЕ (Т.Текст = "X")`, []string{"X"}, 1, nil},
			{"not group", `НЕ (Т.Текст = "X" ИЛИ Т.Статус = "Y")`, []string{"EMPTY", "NULL"}, 2, nil},
			{"negated sibling", `НЕ (Т.Статус = "Y") И Т.Текст = "X"`, []string{"X"}, 1, nil},
			{"positive and negative occurrences", `Т.Текст = "X" ИЛИ НЕ (Т.Текст = "Y")`, []string{"EMPTY", "NULL", "X"}, 1, nil},
			{"empty", `Т.Текст = ""`, []string{"EMPTY", "NULL"}, 1, nil},
			{"empty left", `"" = Т.Текст`, []string{"EMPTY", "NULL"}, 1, nil},
			{"not equal", `Т.Текст <> "X"`, []string{"EMPTY", "NULL", "Y"}, 1, nil},
			{"bang equal", `Т.Текст != "X"`, []string{"EMPTY", "NULL", "Y"}, 1, nil},
			{"empty parameter", `Т.Текст = &Значение`, []string{"EMPTY", "NULL"}, 1, map[string]any{"Значение": ""}},
			{"nonempty parameter", `Т.Текст = &Значение`, []string{"X"}, 1, map[string]any{"Значение": "X"}},
			{"scalar boolean", `(Т.Текст = "X") = ЛОЖЬ`, []string{"EMPTY", "NULL", "Y"}, 1, nil},
			{"case", `ВЫБОР КОГДА Т.Текст = "X" ТОГДА ИСТИНА ИНАЧЕ ЛОЖЬ КОНЕЦ`, []string{"X"}, 1, nil},
			{"case with internal or", `ВЫБОР КОГДА Т.Текст = "X" ИЛИ Т.Статус = "Y" ТОГДА ИСТИНА ИНАЧЕ ЛОЖЬ КОНЕЦ`, []string{"X", "Y"}, 2, nil},
			{"between fallback", `Т.Метка МЕЖДУ "A" И "Z" И Т.Текст = "X"`, []string{"X"}, 1, nil},
			{"in unchanged", `Т.Текст В ("X", "")`, []string{"EMPTY", "X"}, 0, nil},
			{"not in unchanged", `Т.Текст НЕ В ("X", "")`, []string{"Y"}, 0, nil},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				result := compile(t, `ВЫБРАТЬ Т.Метка ИЗ Документ.ИндексныйОтбор КАК Т ГДЕ `+c.predicate+` УПОРЯДОЧИТЬ ПО Т.Метка`, c.params)
				if got := strings.Count(result.SQL, "COALESCE("); got != c.coalesces {
					t.Errorf("COALESCE count %d, want %d: %s", got, c.coalesces, result.SQL)
				}
				rows, cols, err := db.RunQuery(ctx, result.SQL, result.Args)
				if err != nil {
					t.Fatalf("%s: %v", result.SQL, err)
				}
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row[cols[0]]))
				}
				сверитьМетки(t, got, c.want)
			})
		}
		for _, text := range []string{
			`ВЫБРАТЬ Метка ИЗ Документ.ИндексныйОтбор ГДЕ Текст = "X"`,
			`ВЫБРАТЬ Метка ИЗ Документ.ИндексныйОтбор ГДЕ "X" = Текст`,
			`ВЫБРАТЬ Т.Метка ИЗ Документ.ИндексныйОтбор КАК Т ЛЕВОЕ СОЕДИНЕНИЕ Документ.ИндексныйОтбор КАК К ПО Т.Метка = К.Метка ГДЕ Т.Текст = "X"`,
			`ВЫБРАТЬ Метка ИЗ Документ.ИндексныйОтбор ГДЕ Метка В (ВЫБРАТЬ Метка ИЗ Документ.ИндексныйОтбор ГДЕ Текст = "X")`,
			`ВЫБРАТЬ Метка ИЗ Документ.ИндексныйОтбор ГДЕ Текст = "X" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Метка ИЗ Документ.ИндексныйОтбор ГДЕ Текст = "X"`,
		} {
			result := compile(t, text, nil)
			if strings.Contains(result.SQL, "COALESCE(") {
				t.Errorf("positive scope was not optimised: %s", result.SQL)
			}
			rows, cols, err := db.RunQuery(ctx, result.SQL, result.Args)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if fmt.Sprint(row[cols[0]]) != "X" {
					t.Errorf("unexpected row %v", row)
				}
			}
			count := 1
			if strings.Contains(text, "ОБЪЕДИНИТЬ") {
				count = 2
			}
			if len(rows) != count {
				t.Errorf("row count %d, want %d", len(rows), count)
			}
		}
		t.Run("projection and having stay scalar", func(t *testing.T) {
			result := compile(t, `ВЫБРАТЬ Текст = "X" КАК Равно ИЗ Документ.ИндексныйОтбор`, nil)
			if !strings.Contains(result.SQL, "COALESCE(") {
				t.Errorf("projection lost wrapper: %s", result.SQL)
			}
			rows, _, err := db.RunQuery(ctx, result.SQL, result.Args)
			if err != nil || len(rows) != 4 {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
			result = compile(t, `ВЫБРАТЬ Текст ИЗ Документ.ИндексныйОтбор СГРУППИРОВАТЬ ПО Текст ИМЕЮЩИЕ Текст = "X"`, nil)
			if !strings.Contains(result.SQL, "COALESCE(") {
				t.Errorf("HAVING lost wrapper: %s", result.SQL)
			}
			rows, _, err = db.RunQuery(ctx, result.SQL, result.Args)
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
		})
		t.Run("ordinary index is available", func(t *testing.T) {
			// Keep a selective literal among many other values; EXPLAIN proves
			// availability of the index, not a timing promise on tiny tables.
			for i := 0; i < 512; i++ {
				fields := map[string]any{"Метка": fmt.Sprintf("filler-%04d", i), "Текст": "other"}
				if err := db.Upsert(ctx, ent.Name, uuid.New(), fields, ent); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(ctx, `CREATE INDEX idx_positive_text ON индексныйотбор (текст)`); err != nil {
				t.Fatal(err)
			}
			tx, txCtx, err := db.BeginTx(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := tx.Rollback(txCtx); err != nil {
					t.Errorf("rollback EXPLAIN transaction: %v", err)
				}
			}()
			prefix := "EXPLAIN QUERY PLAN "
			if db.Dialect().Name() == "postgres" {
				prefix = "EXPLAIN "
				if _, err := db.Exec(txCtx, "SET LOCAL enable_seqscan = off"); err != nil {
					t.Fatal(err)
				}
			}
			result := compile(t, `ВЫБРАТЬ Метка ИЗ Документ.ИндексныйОтбор ГДЕ Текст = "X"`, nil)
			plan := func(sql string) string {
				t.Helper()
				rows, _, err := db.RunQuery(txCtx, prefix+sql, nil)
				if err != nil {
					t.Fatal(err)
				}
				return fmt.Sprint(rows)
			}
			before := plan(`SELECT метка FROM индексныйотбор WHERE COALESCE(текст, '') = 'X'`)
			after := plan(result.SQL)
			t.Logf("baseline: %s; optimised: %s", before, after)
			if strings.Contains(before, "idx_positive_text") {
				t.Errorf("baseline unexpectedly uses column index: %s", before)
			}
			if !strings.Contains(after, "idx_positive_text") {
				t.Errorf("ordinary column index unavailable: %s", after)
			}
		})
	})
}
