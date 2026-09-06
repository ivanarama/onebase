package storage_test

// Уникальность по произвольному реквизиту (`indexes:` у справочника/документа).
//
// Тесты матричные по той же причине, что и у уникальности кода: гарантию даёт
// СУБД, а код отказа и формат сообщения у SQLite и PostgreSQL разные.
//
// Они же держат раздел DEVELOPER.md «Индексы и уникальность произвольных полей»
// (#1177): там дословно приведён текст отказа и сказано, что незаполненные
// значения уникальности не мешают. Разойдётся поведение с текстом — упадёт
// здесь, а не у читателя документации.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func indexedCatalog(name string, indexes []metadata.IndexSpec) *metadata.Entity {
	return &metadata.Entity{
		Name: name, Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ИНН", Type: metadata.FieldTypeString},
			{Name: "Слаг", Type: metadata.FieldTypeString},
		},
		Indexes: indexes,
	}
}

// Занятое значение произвольного реквизита отклоняется тем же человеческим
// текстом, что и занятый код: перевод стоит на общем пути записи объекта, а не
// в ветке про нумератор.
func TestEntityIndexes_UniqueArbitraryFieldMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := indexedCatalog("Контрагенты"+uuid.NewString()[:8],
			[]metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}})
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}

		first := map[string]any{"Наименование": "Рога", "ИНН": "7701234567"}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), first, ent); err != nil {
			t.Fatalf("первая запись: %v", err)
		}

		second := map[string]any{"Наименование": "Копыта", "ИНН": "7701234567"}
		err := db.Upsert(ctx, ent.Name, uuid.New(), second, ent)
		if err == nil {
			t.Fatal("дубль ИНН записан: уникальность по indexes: не работает")
		}
		if !errors.Is(err, storage.ErrCodeDuplicate) {
			t.Fatalf("ошибка = %v, ожидалась ErrCodeDuplicate", err)
		}
		text := err.Error()
		for _, want := range []string{"ИНН", "7701234567", "уже занято другой записью"} {
			if !strings.Contains(text, want) {
				t.Errorf("в сообщении нет %q: %s", want, text)
			}
		}
		if strings.Contains(text, "UNIQUE constraint") || strings.Contains(text, "SQLSTATE") {
			t.Errorf("наружу утёк текст драйвера: %s", text)
		}

		// Незаполненный реквизит пишется NULL, а NULL в уникальном индексе не
		// конфликтуют ни на одном диалекте: записей без ИНН может быть сколько
		// угодно. Именно это обещает DEVELOPER.md.
		for _, n := range []string{"Без ИНН — раз", "Без ИНН — два"} {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": n}, ent); err != nil {
				t.Fatalf("запись без ИНН отклонена (%s): %v", n, err)
			}
		}
	})
}

// Составной индекс делает уникальной ПАРУ, а не каждое поле по отдельности —
// так в examples/cms объявлена уникальность слага в пределах сайта.
func TestEntityIndexes_CompositeUniqueIsPairMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := indexedCatalog("Товары"+uuid.NewString()[:8],
			[]metadata.IndexSpec{{Fields: []string{"Наименование", "Слаг"}, Unique: true}})
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		rows := []map[string]any{
			{"Наименование": "Сайт А", "Слаг": "chasy"},
			{"Наименование": "Сайт Б", "Слаг": "chasy"}, // тот же слаг у другого сайта — законно
		}
		for _, row := range rows {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
				t.Fatalf("запись %v отклонена: %v", row, err)
			}
		}
		dup := map[string]any{"Наименование": "Сайт А", "Слаг": "chasy"}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), dup, ent); err == nil {
			t.Fatal("повтор пары записан: составная уникальность не работает")
		} else if !errors.Is(err, storage.ErrCodeDuplicate) {
			t.Fatalf("ошибка = %v, ожидалась ErrCodeDuplicate", err)
		}
	})
}
