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

// Отбор по ссылочному полю при авто-JOIN: «ГДЕ Сайт = &Сайт» у справочника,
// который ПО ДРУГОЙ ссылке присоединяет каталог с ОДНОИМЁННЫМ полем.
//
// Колонка-идентификатор ссылки уезжала в WHERE неквалифицированной («сайт_id»),
// тогда как остальные свои колонки префиксовались именем таблицы (п.48). Пока
// одноимённого поля у присоединённого справочника не было, это не проявлялось.
// Стоило завести «Медиа.Сайт» — и JOIN приносил второй «сайт_id», а запросы,
// которые никто не трогал, начинали падать:
//
//	SQL logic error: ambiguous column name: сайт_id
//
// Хуже всего диагностика: падает не тот код, который меняли, и виновным
// выглядит запрос, стоявший здесь годами.
//
// Тест матричный, потому что проверяет ИСПОЛНЕНИЕ SQL: текстовая проверка
// компиляции расхождения диалектов не покажет (CLAUDE.md).

func ambiguousRefEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "Сайты", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}},
		// У картинки есть СВОЙ «Сайт» — ровно та ситуация, которая ломала запрос.
		{Name: "Медиа", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ПубличнаяСсылка", Type: metadata.FieldTypeString},
			{Name: "Сайт", Type: "reference:Сайты", RefEntity: "Сайты"},
		}},
		{Name: "Товары", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Картинка", Type: "reference:Медиа", RefEntity: "Медиа"},
			{Name: "Сайт", Type: "reference:Сайты", RefEntity: "Сайты"},
		}},
	}
}

func seedAmbiguousRef(t *testing.T, ctx context.Context, db *storage.DB, ents []*metadata.Entity) (uuid.UUID, uuid.UUID) {
	t.Helper()
	sites, media, goods := ents[0], ents[1], ents[2]

	our, foreign := uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{our: "Наш сайт", foreign: "Соседний сайт"} {
		if err := db.Upsert(ctx, sites.Name, id, map[string]any{"Наименование": name}, sites); err != nil {
			t.Fatal(err)
		}
	}

	// Картинка принадлежит СОСЕДНЕМУ сайту, а товар — нашему: если условие
	// склеится с колонкой присоединённой таблицы, выдача окажется пустой, а не
	// просто «другой».
	pic := uuid.New()
	if err := db.Upsert(ctx, media.Name, pic, map[string]any{
		"Наименование": "Обложка", "ПубличнаяСсылка": "/pub/token", "Сайт": foreign,
	}, media); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, goods.Name, uuid.New(), map[string]any{
		"Наименование": "Вода", "Картинка": pic, "Сайт": our,
	}, goods); err != nil {
		t.Fatal(err)
	}
	return our, foreign
}

func TestRefFilterWithAutoJoinMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := ambiguousRefEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		our, foreign := seedAmbiguousRef(t, ctx, db, ents)

		// Запрос тянет реквизит через одну ссылку и отбирает по другой — так
		// выглядит любая публичная выборка сайта.
		const q = `ВЫБРАТЬ Наименование, Картинка.ПубличнаяСсылка КАК Обложка
		|ИЗ Справочник.Товары
		|ГДЕ Сайт = &Сайт`

		for _, c := range []struct {
			name string
			site uuid.UUID
			want string
		}{
			{"свой сайт", our, "Вода"},
			// Отбор обязан считаться по владельцу ТОВАРА. Если бы условие
			// прилипло к «медиа.сайт_id», сюда попал бы чужой товар.
			{"сайт картинки не подменяет владельца", foreign, ""},
		} {
			t.Run(c.name, func(t *testing.T) {
				r, err := query.Compile(q, query.CompileOpts{
					Entities: ents,
					Dialect:  db.Dialect(),
					Params:   map[string]any{"Сайт": c.site},
				})
				if err != nil {
					t.Fatalf("компиляция: %v", err)
				}
				rows, err := db.Query(ctx, r.SQL, r.Args...)
				if err != nil {
					t.Fatalf("исполнение: %v\nSQL: %s", err, r.SQL)
				}
				defer rows.Close()

				got := ""
				if rows.Next() {
					var name string
					var cover any
					if err := rows.Scan(&name, &cover); err != nil {
						t.Fatalf("скан: %v\nSQL: %s", err, r.SQL)
					}
					got = name
				}
				if err := rows.Err(); err != nil {
					t.Fatalf("курсор: %v", err)
				}
				if got != c.want {
					t.Errorf("получено %q, ждали %q\nSQL: %s", got, c.want, r.SQL)
				}
			})
		}
	})
}

// Квалификация не должна протекать туда, где колонки нет: обращение через
// точку и выборка самой ссылки остаются прежними.
func TestRefFilterQualification_ОстальныеФормыНеЗатронуты(t *testing.T) {
	ents := ambiguousRefEntities()
	for _, c := range []struct{ q, want string }{
		{`ВЫБРАТЬ Наименование ИЗ Справочник.Товары ГДЕ Сайт = &Сайт`, "товары.сайт_id = "},
		{`ВЫБРАТЬ Наименование ИЗ Справочник.Товары ГДЕ Товары.Сайт = &Сайт`, "товары.сайт_id = "},
	} {
		r, err := query.Compile(c.q, query.CompileOpts{Entities: ents})
		if err != nil {
			t.Fatalf("компиляция %q: %v", c.q, err)
		}
		if !strings.Contains(strings.ToLower(r.SQL), c.want) {
			t.Errorf("в SQL нет %q\nзапрос: %s\nSQL: %s", c.want, c.q, r.SQL)
		}
	}
}
