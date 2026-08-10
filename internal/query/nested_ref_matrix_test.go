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

// Многоуровневая навигация по ссылке: Источник.Ссылка.Реквизит (issue #705).
//
// LEFT JOIN на связанную таблицу компилятор строил и раньше, а вот путь уходил
// в SQL как есть и падал сырой ошибкой движка:
//
//	SQL logic error: no such column: сигналыrag.профиль_id.наименование
//
// Тест матричный, потому что проверяет исполнение получившегося SQL, а не его
// текст: расхождения диалектов текстовая проверка не показала бы (CLAUDE.md).

func nestedRefEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "Профиль", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
		}},
		{Name: "Сигнал", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Профиль", Type: "reference:Профиль", RefEntity: "Профиль"},
		}},
	}
}

func seedNestedRef(t *testing.T, ctx context.Context, db *storage.DB, ents []*metadata.Entity) {
	t.Helper()
	profile, signal := ents[0], ents[1]
	pid := uuid.New()
	if err := db.Upsert(ctx, profile.Name, pid,
		map[string]any{"Наименование": "Основной", "Код": "OSN"}, profile); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, signal.Name, uuid.New(),
		map[string]any{"Наименование": "Сигнал-1", "Профиль": pid}, signal); err != nil {
		t.Fatal(err)
	}
}

func TestNestedRefNavigationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := nestedRefEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		seedNestedRef(t, ctx, db, ents)

		cases := []struct{ name, q, want string }{
			{
				"в выборке",
				`ВЫБРАТЬ Сигнал.Профиль.Наименование ИЗ Справочник.Сигнал`,
				"Основной",
			},
			{
				"в выборке и в условии",
				`ВЫБРАТЬ Сигнал.Профиль.Наименование ИЗ Справочник.Сигнал ГДЕ Сигнал.Профиль.Код = "OSN"`,
				"Основной",
			},
		}
		for _, c := range cases {
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
				var got string
				if !rows.Next() {
					t.Fatalf("пустая выдача\nSQL: %s", r.SQL)
				}
				if err := rows.Scan(&got); err != nil {
					t.Fatalf("скан: %v\nSQL: %s", err, r.SQL)
				}
				if got != c.want {
					t.Errorf("получено %q, ждали %q\nSQL: %s", got, c.want, r.SQL)
				}
			})
		}
	})
}

// Одноуровневые обращения не должны измениться: ссылка сама по себе — это
// по-прежнему идентификатор, а собственный реквизит берётся из своей таблицы.
func TestNestedRefNavigation_ОдноуровневыеНеЗатронуты(t *testing.T) {
	ents := nestedRefEntities()
	for _, c := range []struct{ q, want string }{
		{`ВЫБРАТЬ Сигнал.Наименование ИЗ Справочник.Сигнал`, "сигнал.наименование"},
		{`ВЫБРАТЬ Сигнал.Профиль ИЗ Справочник.Сигнал`, "сигнал.профиль_id"},
	} {
		r, err := query.Compile(c.q, query.CompileOpts{Entities: ents})
		if err != nil {
			t.Fatalf("компиляция %q: %v", c.q, err)
		}
		if !strings.Contains(r.SQL, c.want) {
			t.Errorf("для %q ждали %q в SQL, получено:\n%s", c.q, c.want, r.SQL)
		}
	}
}
