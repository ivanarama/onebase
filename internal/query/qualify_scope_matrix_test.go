package query_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Отбор по ссылочному полю включает автоприсоединение справочника, и с ним —
// квалификацию собственных колонок таблицей источника. Квалификатор был общим
// на весь запрос — главная таблица первого SELECT. Поэтому:
//
//   - во второй ветви ОБЪЕДИНИТЬ ВСЕ поле «Дата» становилось
//     «приходы.дата» — «no such column»;
//   - служебные posted/deletion_mark, которых нет среди реквизитов, не
//     квалифицировались вовсе, и deletion_mark присоединённого справочника
//     делал их неоднозначными — «ambiguous column name».
//
// Так падал перебор документов нескольких видов за период с отбором по
// организации (перепроведение для закрытия месяца в торговой конфигурации).
func TestQualifyOwnColumnsByScopeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		org := &metadata.Entity{
			Name: "ОргКв" + suffix, Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		refType := metadata.FieldType("reference:" + org.Name)
		doc := func(name string) *metadata.Entity {
			return &metadata.Entity{
				Name: name + suffix, Kind: metadata.KindDocument, Posting: true,
				Fields: []metadata.Field{
					{Name: "Дата", Type: metadata.FieldTypeDate},
					{Name: "Организация", Type: refType, RefEntity: org.Name},
				},
			}
		}
		incoming, outgoing := doc("ПриходКв"), doc("РасходКв")
		entities := []*metadata.Entity{org, incoming, outgoing}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		ours, other := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{ours, other} {
			if err := db.Upsert(ctx, org.Name, id, map[string]any{"Наименование": "Орг"}, org); err != nil {
				t.Fatal(err)
			}
		}
		day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		write := func(ent *metadata.Entity, organization uuid.UUID, offset int, posted bool) {
			t.Helper()
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id, map[string]any{
				"Дата": day.AddDate(0, 0, offset), "Организация": organization.String(),
			}, ent); err != nil {
				t.Fatal(err)
			}
			if posted {
				if err := db.SetPosted(ctx, ent.Name, id, true); err != nil {
					t.Fatal(err)
				}
			}
		}
		write(incoming, ours, 1, true)
		write(outgoing, ours, 2, true)
		write(outgoing, ours, 3, false) // не проведён — не попадает
		write(outgoing, other, 4, true) // чужая организация — не попадает

		opts := query.CompileOpts{Entities: entities, Dialect: db.Dialect(),
			Params: map[string]any{"Орг": ours.String()}}
		run := func(t *testing.T, src string) []string {
			t.Helper()
			compiled, err := query.Compile(src, opts)
			if err != nil {
				t.Fatalf("компиляция: %v\n%s", err, src)
			}
			rows, err := db.Query(ctx, compiled.SQL, compiled.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, compiled.SQL)
			}
			defer rows.Close()
			var got []string
			for rows.Next() {
				var kind string
				var date any
				if err := rows.Scan(&date, &kind); err != nil {
					t.Fatalf("скан: %v", err)
				}
				got = append(got, kind)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			sort.Strings(got)
			return got
		}
		t.Run("объединить", func(t *testing.T) {
			got := run(t, `ВЫБРАТЬ Дата, 'приход' КАК Вид ИЗ Документ.`+incoming.Name+`
				ГДЕ Организация = &Орг И posted = Истина
				ОБЪЕДИНИТЬ ВСЕ
				ВЫБРАТЬ Дата, 'расход' ИЗ Документ.`+outgoing.Name+`
				ГДЕ Организация = &Орг И posted = Истина
				УПОРЯДОЧИТЬ ПО Дата`)
			if strings.Join(got, ",") != "приход,расход" {
				t.Fatalf("получено %v, ожидались проведённые приход и расход своей организации", got)
			}
		})
		t.Run("служебные колонки", func(t *testing.T) {
			got := run(t, `ВЫБРАТЬ Дата, 'расход' КАК Вид ИЗ Документ.`+outgoing.Name+`
				ГДЕ posted = Истина И deletion_mark = Ложь И Организация = &Орг`)
			if strings.Join(got, ",") != "расход" {
				t.Fatalf("получено %v, ожидался один проведённый расход своей организации", got)
			}
		})
	})
}
