package query_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Разыменование до ССЫЛОЧНОГО реквизита: «Исполнитель.Учётка» (#1784).
//
// Ссылочный реквизит хранится колонкой с суффиксом _id, а компилятор брал её
// по имени реквизита — запрос падал сырым «no such column: ref_исполнитель.учётка»,
// хотя по простому реквизиту («Исполнитель.Наименование») то же разыменование
// работало. Тест матричный: проверяет исполнение SQL, а не его текст.

var (
	derefУчётка1 = uuid.MustParse("00000000-0000-4000-8000-000000001784")
	derefУчётка2 = uuid.MustParse("00000000-0000-4000-8000-000000001785")
)

func derefEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "Учётка", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}},
		{Name: "Сотрудник", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Учётка", Type: "reference:Учётка", RefEntity: "Учётка"},
			{Name: "Owner", Type: metadata.FieldTypeString},
		}},
		{Name: "ЗадачаДереф", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Исполнитель", Type: "reference:Сотрудник", RefEntity: "Сотрудник"},
		}},
	}
}

// Два сотрудника со своими учётками, у каждого по задаче. Второй сотрудник —
// «чужой» для построчного фильтра.
func seedDeref(t *testing.T, db *storage.DB) []*metadata.Entity {
	t.Helper()
	ctx := context.Background()
	ents := derefEntities()
	if err := db.Migrate(ctx, ents); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	учётка, сотрудник, задача := ents[0], ents[1], ents[2]
	for _, u := range []struct {
		id   uuid.UUID
		name string
	}{{derefУчётка1, "первая"}, {derefУчётка2, "вторая"}} {
		if err := db.Upsert(ctx, учётка.Name, u.id, map[string]any{"Наименование": u.name}, учётка); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct {
		name, owner, номер string
		учётка             uuid.UUID
	}{{"Первый", "свой", "З-1", derefУчётка1}, {"Второй", "чужой", "З-2", derefУчётка2}} {
		id := uuid.New()
		if err := db.Upsert(ctx, сотрудник.Name, id,
			map[string]any{"Наименование": s.name, "Учётка": s.учётка, "Owner": s.owner}, сотрудник); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, задача.Name, uuid.New(),
			map[string]any{"Номер": s.номер, "Исполнитель": id}, задача); err != nil {
			t.Fatal(err)
		}
	}
	return ents
}

func derefNumbers(t *testing.T, db *storage.DB, q string, opts query.CompileOpts) []string {
	t.Helper()
	opts.Dialect = db.Dialect()
	r, err := query.Compile(q, opts)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	rows, err := db.Query(context.Background(), r.SQL, r.Args...)
	if err != nil {
		t.Fatalf("исполнение: %v\nSQL: %s", err, r.SQL)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var номер string
		if err := rows.Scan(&номер); err != nil {
			t.Fatalf("скан: %v\nSQL: %s", err, r.SQL)
		}
		got = append(got, номер)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("строки: %v\nSQL: %s", err, r.SQL)
	}
	return got
}

func assertNumbers(t *testing.T, got []string, want ...string) {
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

func TestRefAttrDereferenceMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ents := seedDeref(t, db)

		t.Run("ссылочный реквизит в условии", func(t *testing.T) {
			got := derefNumbers(t, db,
				`ВЫБРАТЬ Номер ИЗ Документ.ЗадачаДереф ГДЕ Исполнитель.Учётка = &Учётка`,
				query.CompileOpts{Entities: ents, Params: map[string]any{"Учётка": derefУчётка1}})
			assertNumbers(t, got, "З-1")
		})

		t.Run("ссылочный реквизит в выборке", func(t *testing.T) {
			r, err := query.Compile(
				`ВЫБРАТЬ Номер, Исполнитель.Учётка КАК Учётка ИЗ Документ.ЗадачаДереф УПОРЯДОЧИТЬ ПО Номер`,
				query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			if err != nil {
				t.Fatalf("компиляция: %v", err)
			}
			// Результат — ссылка на «Учётку», а не строка: колонка помечена
			// ссылочной, и DSL оборачивает её значение в ссылку (#1784, вариант 1).
			if got := r.RefColumns["учётка"]; got != "Учётка" {
				t.Fatalf("RefColumns[учётка]=%q, ожидалась ссылка на «Учётка»: %v", got, r.RefColumns)
			}
			rows, err := db.Query(context.Background(), r.SQL, r.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, r.SQL)
			}
			defer rows.Close()
			n := 0
			for rows.Next() {
				var номер string
				var учётка any
				if err := rows.Scan(&номер, &учётка); err != nil {
					t.Fatalf("скан: %v\nSQL: %s", err, r.SQL)
				}
				if учётка == nil {
					t.Fatalf("у задачи %s пустая учётка исполнителя\nSQL: %s", номер, r.SQL)
				}
				n++
			}
			if n != 2 {
				t.Fatalf("строк %d, ожидалось 2\nSQL: %s", n, r.SQL)
			}
		})

		t.Run("простой реквизит не изменился", func(t *testing.T) {
			got := derefNumbers(t, db,
				`ВЫБРАТЬ Номер ИЗ Документ.ЗадачаДереф ГДЕ Исполнитель.Наименование = "Второй"`,
				query.CompileOpts{Entities: ents})
			assertNumbers(t, got, "З-2")
		})

		// Права на присоединённую сущность разыменование не обходит: построчный
		// фильтр справочника сотрудников действует и на путь через ссылку.
		t.Run("построчный фильтр присоединённой сущности", func(t *testing.T) {
			opts := query.CompileOpts{
				Entities: ents,
				RowFilters: map[query.SourceRef]*storage.Predicate{
					{Kind: "catalog", Name: "Сотрудник"}: {Field: "Owner", Op: "eq", Value: "свой"},
				},
			}
			q := `ВЫБРАТЬ Номер ИЗ Документ.ЗадачаДереф ГДЕ Исполнитель.Учётка = &Учётка`
			opts.Params = map[string]any{"Учётка": derefУчётка2}
			assertNumbers(t, derefNumbers(t, db, q, opts))
			opts.Params = map[string]any{"Учётка": derefУчётка1}
			assertNumbers(t, derefNumbers(t, db, q, opts), "З-1")
		})
	})
}

// Присоединённая сущность попадает в источники запроса — по ним проверяются
// права роли (план 54): без права на «Сотрудник» запрос не выполнится.
func TestRefAttrDereferenceReportsJoinedSource(t *testing.T) {
	res, err := query.Compile(
		`ВЫБРАТЬ Номер ИЗ Документ.ЗадачаДереф ГДЕ Исполнитель.Учётка = &Учётка`,
		query.CompileOpts{Entities: derefEntities()})
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if !hasSource(res.Sources, "catalog", "Сотрудник") {
		t.Fatalf("присоединённый справочник не в источниках: %+v", res.Sources)
	}
}

// Тип одноимённого поля основной таблицы не должен менять ни ссылочный
// отбор, ни числовое сравнение/сортировку реквизита присоединённой сущности.
func TestRefAttrDereferenceNumberCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := derefEntities()
		ents[1].Fields = append(ents[1].Fields,
			metadata.Field{Name: "Балл", Type: metadata.FieldTypeNumber})
		ents[2].Fields = append(ents[2].Fields,
			metadata.Field{Name: "Учётка", Type: metadata.FieldTypeNumber},
			metadata.Field{Name: "Owner", Type: metadata.FieldTypeNumber},
			metadata.Field{Name: "Балл", Type: metadata.FieldTypeString})
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		for i, account := range []uuid.UUID{derefУчётка1, derefУчётка2} {
			if err := db.Upsert(ctx, ents[0].Name, account,
				map[string]any{"Наименование": "учётка"}, ents[0]); err != nil {
				t.Fatal(err)
			}
			employee := uuid.New()
			score, number := 10, "З-1"
			if i == 1 {
				score, number = 2, "З-2"
			}
			if err := db.Upsert(ctx, ents[1].Name, employee,
				map[string]any{"Учётка": account, "Owner": "свой", "Балл": score}, ents[1]); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, ents[2].Name, uuid.New(),
				map[string]any{"Номер": number, "Исполнитель": employee, "Учётка": score, "Owner": score, "Балл": "текст"}, ents[2]); err != nil {
				t.Fatal(err)
			}
		}
		for _, qualified := range []bool{false, true} {
			name, from, prefix := "без квалификатора источника", "Документ.ЗадачаДереф", ""
			if qualified {
				name, from, prefix = "с квалификатором источника", "Документ.ЗадачаДереф КАК З", "З."
			}
			t.Run(name, func(t *testing.T) {
				opts := query.CompileOpts{Entities: ents, Params: map[string]any{"У": derefУчётка1}}
				selectNumber := "ВЫБРАТЬ " + prefix + "Номер ИЗ " + from
				t.Run("ссылка при одноимённом числе", func(t *testing.T) {
					assertNumbers(t, derefNumbers(t, db, selectNumber+" ГДЕ "+prefix+"Исполнитель.Учётка = &У", opts), "З-1")
				})
				t.Run("строка при одноимённом числе", func(t *testing.T) {
					assertNumbers(t, derefNumbers(t, db, selectNumber+" ГДЕ "+prefix+`Исполнитель.Owner = "свой" УПОРЯДОЧИТЬ ПО `+prefix+"Номер", opts), "З-1", "З-2")
				})
				t.Run("числовое сравнение при одноимённой строке", func(t *testing.T) {
					assertNumbers(t, derefNumbers(t, db, selectNumber+" ГДЕ "+prefix+"Исполнитель.Балл > 9", opts), "З-1")
				})
				t.Run("числовая сортировка при одноимённой строке", func(t *testing.T) {
					assertNumbers(t, derefNumbers(t, db, selectNumber+" УПОРЯДОЧИТЬ ПО "+prefix+"Исполнитель.Балл", opts), "З-2", "З-1")
				})
			})
		}
	})
}
