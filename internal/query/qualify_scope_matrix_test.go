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

// Служебная колонка может принадлежать авто-присоединённому источнику:
// deletion_mark отсутствует у регистров, а posted — у справочников.
func TestQualifyOwnServiceColumnsBySourceMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		org := &metadata.Entity{
			Name: "ОргСлуж" + suffix, Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		doc := &metadata.Entity{
			Name: "ДокСлуж" + suffix, Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		catalog := &metadata.Entity{
			Name: "КатСлуж" + suffix, Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Документ", Type: metadata.FieldType("reference:" + doc.Name), RefEntity: doc.Name}},
		}
		reg := &metadata.InfoRegister{
			Name:       "ПрайсСлуж" + suffix,
			Dimensions: []metadata.Field{{Name: "Организация", Type: metadata.FieldType("reference:" + org.Name), RefEntity: org.Name}},
			Resources:  []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
		}
		entities := []*metadata.Entity{org, doc, catalog}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{reg}); err != nil {
			t.Fatal(err)
		}
		orgID, docID, catalogID := uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			entity *metadata.Entity
			id     uuid.UUID
			data   map[string]any
		}{
			{org, orgID, map[string]any{"Наименование": "Организация"}},
			{doc, docID, map[string]any{"Наименование": "Документ"}},
			{catalog, catalogID, map[string]any{"Документ": docID.String()}},
		} {
			if err := db.Upsert(ctx, row.entity.Name, row.id, row.data, row.entity); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.InfoRegSet(ctx, reg, map[string]any{"Организация": orgID.String()}, map[string]any{"Цена": 10}, nil); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{
			Entities: entities, InfoRegs: []*metadata.InfoRegister{reg}, Dialect: db.Dialect(),
			Params: map[string]any{"О": orgID.String(), "Д": docID.String()},
		}
		count := func(t *testing.T, source string, want int) {
			t.Helper()
			compiled, err := query.Compile(source, opts)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query(ctx, compiled.SQL, compiled.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, compiled.SQL)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatalf("нет строки результата: %v", rows.Err())
			}
			var got int
			if err := rows.Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("получено %d, ожидалось %d\nSQL: %s", got, want, compiled.SQL)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("неожиданный результат: %v", rows.Err())
			}
		}
		t.Run("регистр не владеет deletion_mark", func(t *testing.T) {
			for _, alias := range []string{"", " КАК Р"} {
				src := `ВЫБРАТЬ КОЛИЧЕСТВО(*) КАК Всего ИЗ РегистрСведений.` + reg.Name + alias +
					` ГДЕ Организация = &О И deletion_mark = Ложь`
				count(t, src, 1)
				if _, err := db.Exec(ctx, "UPDATE "+metadata.TableName(org.Name)+" SET deletion_mark = "+db.Dialect().Placeholder(1), true); err != nil {
					t.Fatal(err)
				}
				count(t, src, 0)
				if _, err := db.Exec(ctx, "UPDATE "+metadata.TableName(org.Name)+" SET deletion_mark = "+db.Dialect().Placeholder(1), false); err != nil {
					t.Fatal(err)
				}
			}
		})
		t.Run("справочник не владеет posted", func(t *testing.T) {
			src := `ВЫБРАТЬ КОЛИЧЕСТВО(*) КАК Всего ИЗ Справочник.` + catalog.Name +
				` КАК К ГДЕ Документ = &Д И posted = Истина`
			count(t, src, 0)
			if err := db.SetPosted(ctx, doc.Name, docID, true); err != nil {
				t.Fatal(err)
			}
			count(t, src, 1)
		})
		t.Run("справочник владеет deletion_mark", func(t *testing.T) {
			src := `ВЫБРАТЬ КОЛИЧЕСТВО(*) КАК Всего ИЗ Справочник.` + catalog.Name +
				` КАК К ГДЕ Документ = &Д И deletion_mark = Ложь`
			count(t, src, 1)
			if _, err := db.Exec(ctx, "UPDATE "+metadata.TableName(catalog.Name)+" SET deletion_mark = "+db.Dialect().Placeholder(1), true); err != nil {
				t.Fatal(err)
			}
			count(t, src, 0)
		})
	})
}
