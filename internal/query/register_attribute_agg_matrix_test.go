package query_test

// Реквизиты регистра накопления в .Остатки()/.Обороты()/.ОстаткиИОбороты()
// собираются агрегатом (они не часть ключа измерения). На PostgreSQL у uuid и
// boolean агрегата min нет: ссылочный реквизит ронял запрос «function min(uuid)
// does not exist», булев — «function min(boolean) does not exist». SQLite
// хранит оба текстом/целым и молча принимал MIN — отсюда матрица.

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

func TestRegisterRefAndBoolAttributesInVirtualTables(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		master := &metadata.Entity{Name: "МастерРеквизит", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		reg := &metadata.Register{Name: "ОстаткиМастера",
			Dimensions: []metadata.Field{{Name: "Склад", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
			Attributes: []metadata.Field{
				{Name: "Мастер", Type: "reference:МастерРеквизит", RefEntity: "МастерРеквизит"},
				{Name: "Подтверждено", Type: metadata.FieldTypeBool},
			}}
		if err := db.Migrate(ctx, []*metadata.Entity{master}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		masterID := uuid.New()
		if err := db.Upsert(ctx, master.Name, masterID, map[string]any{"Наименование": "Иванов"}, master); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteMovements(ctx, reg.Name, "Документ", uuid.New(), []map[string]any{{
			"ВидДвижения": "Приход", "Склад": "Основной", "Количество": 3,
			"Мастер": masterID.String(), "Подтверждено": true,
		}}, reg, nil); err != nil {
			t.Fatal(err)
		}
		for _, vt := range []string{"Остатки()", "Обороты()", "ОстаткиИОбороты()"} {
			t.Run(vt, func(t *testing.T) {
				r, err := query.Compile(`ВЫБРАТЬ Р.Склад КАК Склад, Р.Мастер КАК Мастер, Р.Подтверждено КАК Подтверждено
					ИЗ РегистрНакопления.ОстаткиМастера.`+vt+` КАК Р`, query.CompileOpts{
					Entities: []*metadata.Entity{master}, Registers: []*metadata.Register{reg}, Dialect: db.Dialect(),
				})
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
				if err != nil {
					t.Fatalf("%v\nSQL: %s", err, r.SQL)
				}
				if len(rows) != 1 {
					t.Fatalf("rows=%v", rows)
				}
				byName := map[string]any{}
				for _, c := range cols {
					byName[strings.ToLower(c)] = rows[0][c]
				}
				if got := strings.ToLower(fmt.Sprint(byName["мастер"])); got != masterID.String() {
					t.Fatalf("Мастер=%v, ожидалась ссылка %s; SQL: %s", byName["мастер"], masterID, r.SQL)
				}
				if got := fmt.Sprint(byName["подтверждено"]); got != "true" && got != "1" {
					t.Fatalf("Подтверждено=%v, ожидалась истина; SQL: %s", byName["подтверждено"], r.SQL)
				}
			})
		}
	})
}
