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

// Регрессия #705: `Источник.Ссылка.Поле` не разбиралась как навигация — путь
// уходил в SQL дословно и падал `no such column: сигналыrag.профиль_id.наименование`.
// Голое `Ссылка.Поле` при этом работало, то есть ломал именно квалификатор
// источника, который пишут по привычке из 1С.

func navEntities() []*metadata.Entity {
	owner := &metadata.Entity{
		Name:   "Владелец",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	profile := &metadata.Entity{
		Name: "Профиль",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Владелец", Type: "reference:Владелец", RefEntity: "Владелец"},
		},
	}
	signal := &metadata.Entity{
		Name: "СигналыRAG",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Профиль", Type: "reference:Профиль", RefEntity: "Профиль"},
		},
	}
	return []*metadata.Entity{owner, profile, signal}
}

// Навигация с квалификатором источника доходит до данных на обоих диалектах:
// имя связанного справочника читается и по имени таблицы, и по алиасу.
func TestRefNavigation_QualifiedPathMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := navEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		profileID, signalID := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, "Профиль", profileID, map[string]any{"наименование": "Аналитик"}, ents[1]); err != nil {
			t.Fatalf("вставка профиля: %v", err)
		}
		if err := db.Upsert(ctx, "СигналыRAG", signalID, map[string]any{
			"наименование": "Сигнал-1",
			"профиль":      profileID.String(),
		}, ents[2]); err != nil {
			t.Fatalf("вставка сигнала: %v", err)
		}

		cases := []struct {
			name string
			src  string
		}{
			{"по имени источника", `ВЫБРАТЬ СигналыRAG.Профиль.Наименование ИЗ Справочник.СигналыRAG`},
			{"по алиасу источника", `ВЫБРАТЬ С.Профиль.Наименование ИЗ Справочник.СигналыRAG КАК С`},
			{"без квалификатора", `ВЫБРАТЬ Профиль.Наименование ИЗ Справочник.СигналыRAG`},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				res, err := query.Compile(c.src, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatalf("компиляция: %v\nзапрос: %s", err, c.src)
				}
				var got string
				if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&got); err != nil {
					t.Fatalf("исполнение: %v\nSQL: %s", err, res.SQL)
				}
				if got != "Аналитик" {
					t.Errorf("получено %q, ожидалось «Аналитик»\nSQL: %s", got, res.SQL)
				}
			})
		}
	})
}

// Квалифицированный путь работает и в условии, и в упорядочивании, а не только
// в проекции: чинить одну секцию бессмысленно — отчёт всё равно упал бы.
func TestRefNavigation_QualifiedPathInWhereAndOrder(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := navEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		profileID, signalID := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, "Профиль", profileID, map[string]any{"наименование": "Аналитик"}, ents[1]); err != nil {
			t.Fatalf("вставка профиля: %v", err)
		}
		if err := db.Upsert(ctx, "СигналыRAG", signalID, map[string]any{
			"наименование": "Сигнал-1",
			"профиль":      profileID.String(),
		}, ents[2]); err != nil {
			t.Fatalf("вставка сигнала: %v", err)
		}

		for _, src := range []string{
			`ВЫБРАТЬ Наименование ИЗ Справочник.СигналыRAG КАК С ГДЕ С.Профиль.Наименование = "Аналитик"`,
			`ВЫБРАТЬ Наименование ИЗ Справочник.СигналыRAG УПОРЯДОЧИТЬ ПО СигналыRAG.Профиль.Наименование`,
		} {
			res, err := query.Compile(src, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			if err != nil {
				t.Fatalf("компиляция: %v\nзапрос: %s", err, src)
			}
			var got string
			if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&got); err != nil {
				t.Fatalf("исполнение: %v\nзапрос: %s\nSQL: %s", err, src, res.SQL)
			}
			if got != "Сигнал-1" {
				t.Errorf("получено %q, ожидалось «Сигнал-1» для %s", got, src)
			}
		}
	})
}

// Квалификатор снимается только у настоящего источника. `Ссылка` под чужим
// алиасом — не навигация, и трогать её нельзя: иначе мы бы молча подменили
// колонку присоединённой таблицы полем справочника.
func TestRefNavigation_UnknownQualifierUntouched(t *testing.T) {
	ents := navEntities()
	res, err := query.Compile(
		`ВЫБРАТЬ Ссылка ИЗ Справочник.СигналыRAG ГДЕ Чужой.Профиль.Наименование = "х"`,
		query.CompileOpts{Entities: ents, Dialect: storage.SQLiteDialect{}})
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if !strings.Contains(res.SQL, "чужой.") {
		t.Errorf("незнакомый квалификатор не должен исчезать из SQL:\n%s", res.SQL)
	}
	if strings.Contains(res.SQL, "WHERE ref_профиль.наименование") {
		t.Errorf("незнакомый квалификатор молча превращён в навигацию:\n%s", res.SQL)
	}
}

// Ссылочное поле без дальнейшего пути ведёт себя как раньше: квалифицированное
// `С.Профиль` — это идентификатор ссылки, а не наименование.
func TestRefNavigation_QualifiedRefWithoutPathUnchanged(t *testing.T) {
	ents := navEntities()
	res, err := query.Compile(
		`ВЫБРАТЬ С.Профиль ИЗ Справочник.СигналыRAG КАК С`,
		query.CompileOpts{Entities: ents, Dialect: storage.SQLiteDialect{}})
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if !strings.Contains(res.SQL, "с.профиль_id") {
		t.Errorf("С.Профиль перестал быть ссылкой:\n%s", res.SQL)
	}
}

// Путь глубже одного перехода отклоняется ошибкой уровня языка запросов.
// Раньше он уходил в SQL дословно, и разработчик видел имя несуществующей
// колонки, из которого не следует, что именно не поддерживается.
func TestRefNavigation_DeepPathIsLanguageError(t *testing.T) {
	ents := navEntities()
	for _, src := range []string{
		`ВЫБРАТЬ Профиль.Владелец.Наименование ИЗ Справочник.СигналыRAG`,
		`ВЫБРАТЬ СигналыRAG.Профиль.Владелец.Наименование ИЗ Справочник.СигналыRAG`,
		`ВЫБРАТЬ Ссылка ИЗ Справочник.СигналыRAG ГДЕ Профиль.Владелец.Наименование = "х"`,
	} {
		_, err := query.Compile(src, query.CompileOpts{Entities: ents, Dialect: storage.SQLiteDialect{}})
		if err == nil {
			t.Fatalf("глубокий путь скомпилировался без ошибки: %s", src)
		}
		msg := err.Error()
		if !strings.Contains(msg, "на один уровень") {
			t.Errorf("сообщение не объясняет предел: %v", err)
		}
		if !strings.Contains(msg, "Профиль.Владелец") {
			t.Errorf("сообщение не показывает сам путь: %v", err)
		}
		if strings.Contains(msg, "no such column") {
			t.Errorf("вместо ошибки языка запросов пришла низкоуровневая SQL-ошибка: %v", err)
		}
	}
}
