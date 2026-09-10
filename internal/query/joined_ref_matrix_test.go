package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Ссылочное поле присоединённого источника (issue #1385).
//
// Ссылочные поля разрешались по плоскому словарю первого источника `ИЗ`, и как
// только тот же справочник приходил вторым — через СОЕДИНЕНИЕ — обращение к его
// реквизиту уходило в SQL сырым именем:
//
//	SQL logic error: no such column: п.главныйузел
//
// Тест матричный, потому что проверяет исполнение получившегося SQL, а не его
// текст: расхождения диалектов текстовая проверка не показала бы (CLAUDE.md).
// В бою тот же дефект виден на PostgreSQL как
// `column планыобменов.главныйузел does not exist`.

func joinedRefEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "Базы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}},
		{Name: "Планы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ГлавныйУзел", Type: "reference:Базы", RefEntity: "Базы"},
		}},
	}
}

func seedJoinedRef(t *testing.T, ctx context.Context, db *storage.DB, ents []*metadata.Entity) {
	t.Helper()
	base, plan := ents[0], ents[1]
	bid := uuid.New()
	if err := db.Upsert(ctx, base.Name, bid,
		map[string]any{"Код": "GL", "Наименование": "Главная"}, base); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, plan.Name, uuid.New(),
		map[string]any{"Код": "GL", "Наименование": "План-1", "ГлавныйУзел": bid}, plan); err != nil {
		t.Fatal(err)
	}
}

func TestJoinedRefFieldMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := joinedRefEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		seedJoinedRef(t, ctx, db, ents)

		for _, c := range []struct{ name, q, want string }{
			{
				"ссылочное поле присоединённого в ПО",
				`ВЫБРАТЬ П.Наименование ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел = Б.Ссылка`,
				"План-1",
			},
			{
				"ссылочное поле присоединённого в ГДЕ",
				`ВЫБРАТЬ П.Наименование ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код ГДЕ П.ГлавныйУзел = Б.Ссылка`,
				"План-1",
			},
			{
				"внутреннее соединение по ссылочному полю",
				`ВЫБРАТЬ П.Наименование ИЗ Справочник.Базы КАК Б ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел = Б.Ссылка`,
				"План-1",
			},
			{
				"навигация через точку у присоединённого в выборке",
				`ВЫБРАТЬ П.ГлавныйУзел.Наименование ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел = Б.Ссылка`,
				"Главная",
			},
			{
				"навигация через точку у присоединённого в ГДЕ",
				`ВЫБРАТЬ П.Наименование ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код ГДЕ П.ГлавныйУзел.Наименование = "Главная"`,
				"План-1",
			},
			{
				"обратный порядок источников не сломался",
				`ВЫБРАТЬ Б.Наименование ИЗ Справочник.Планы КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Базы КАК Б ПО П.ГлавныйУзел = Б.Ссылка`,
				"Главная",
			},
		} {
			t.Run(c.name, func(t *testing.T) {
				r, err := query.Compile(c.q, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatalf("компиляция: %v", err)
				}
				rows, err := db.Query(ctx, r.SQL, r.Args...)
				if err != nil {
					t.Fatalf("исполнение: %v\nSQL: %s", err, r.SQL)
				}
				defer rows.Close()
				if !rows.Next() {
					t.Fatalf("пустая выдача\nSQL: %s", r.SQL)
				}
				var got string
				if err := rows.Scan(&got); err != nil {
					t.Fatalf("скан: %v\nSQL: %s", err, r.SQL)
				}
				if got != c.want {
					t.Errorf("получено %q, ждали %q\nSQL: %s", got, c.want, r.SQL)
				}
			})
		}

		// Дословный запрос из заявки: до починки падал `no such column`.
		// Строк он не отдаёт (ссылка плана не равна ссылке базы) — доказывается
		// именно то, что SQL исполняется.
		t.Run("запрос из заявки исполняется", func(t *testing.T) {
			const q = `ВЫБРАТЬ П.ГлавныйУзел ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Ссылка = Б.Ссылка`
			r, err := query.Compile(q, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			if err != nil {
				t.Fatalf("компиляция: %v", err)
			}
			rows, err := db.Query(ctx, r.SQL, r.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, r.SQL)
			}
			defer rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatalf("выдача: %v\nSQL: %s", err, r.SQL)
			}
		})
	})
}

// Порядок JOIN'ов — отдельная проверка текста SQL: авто-JOIN присоединённого
// источника обязан стоять ПОСЛЕ его ПО, иначе он вклинивается между таблицей и
// её ON и рвёт запрос (план 143, п.50). Главный источник при этом сохраняет
// прежний порядок — JOIN сразу за таблицей.
func TestJoinedRefJoinOrder(t *testing.T) {
	ents := joinedRefEntities()

	t.Run("авто-JOIN присоединённого идёт после ПО", func(t *testing.T) {
		r, err := query.Compile(
			`ВЫБРАТЬ П.ГлавныйУзел.Наименование ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел = Б.Ссылка`,
			query.CompileOpts{Entities: ents})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		on := strings.Index(r.SQL, "ON п.главныйузел_id = б.id")
		auto := strings.Index(r.SQL, "LEFT JOIN базы ref_п_главныйузел")
		if on < 0 || auto < 0 {
			t.Fatalf("не нашли ни ПО источника, ни авто-JOIN:\n%s", r.SQL)
		}
		if auto < on {
			t.Errorf("авто-JOIN вклинился перед ON присоединённого источника:\n%s", r.SQL)
		}
	})

	t.Run("главный источник не изменился", func(t *testing.T) {
		r, err := query.Compile(
			`ВЫБРАТЬ П.ГлавныйУзел.Наименование ИЗ Справочник.Планы КАК П`,
			query.CompileOpts{Entities: ents})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		if !strings.Contains(r.SQL, "FROM планы AS п LEFT JOIN базы ref_главныйузел ON ref_главныйузел.id = п.главныйузел_id") {
			t.Errorf("порядок JOIN главного источника изменился:\n%s", r.SQL)
		}
	})

	t.Run("без навигации лишнего JOIN нет", func(t *testing.T) {
		r, err := query.Compile(
			`ВЫБРАТЬ П.Наименование ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел = Б.Ссылка`,
			query.CompileOpts{Entities: ents})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		if strings.Contains(r.SQL, "ref_п_главныйузел") {
			t.Errorf("авто-JOIN построен там, где хватает колонки-идентификатора:\n%s", r.SQL)
		}
		if !strings.Contains(r.SQL, "ON п.главныйузел_id = б.id") {
			t.Errorf("ссылочное поле не разрешилось в колонку:\n%s", r.SQL)
		}
	})
}
