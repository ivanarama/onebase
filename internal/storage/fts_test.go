package storage

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Полнотекстовый поиск (план 82). Тесты гоняются на SQLite (FTS5); PostgreSQL
// покрыт теми же сценариями в fts_pg_test.go под тегом integration.

func ftsTestEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{
			Name: "Контрагент",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "ИНН", Type: metadata.FieldTypeString},
				{Name: "Комментарий", Type: metadata.FieldTypeString},
			},
		},
		{
			Name:    "РасходнаяНакладная",
			Kind:    metadata.KindDocument,
			Posting: true,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Основание", Type: metadata.FieldTypeString},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
			},
		},
	}
}

func newFTSTestDB(t *testing.T) (*DB, []*metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entities := ftsTestEntities()
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	return db, entities
}

func search(t *testing.T, db *DB, text string, names ...string) []FTSHit {
	t.Helper()
	hits, err := db.SearchFullText(context.Background(), FTSQuery{Text: text, Names: names, Limit: 20})
	if err != nil {
		t.Fatalf("поиск %q: %v", text, err)
	}
	return hits
}

func hitIDs(hits []FTSHit) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(hits))
	for _, h := range hits {
		out[h.ID] = true
	}
	return out
}

func TestFullText_FindsObjectsAcrossEntities(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat, doc := entities[0], entities[1]

	romashka := uuid.New()
	if err := db.Upsert(ctx, cat.Name, romashka, map[string]any{
		"Наименование": "ООО Ромашка",
		"ИНН":          "7701234567",
		"Комментарий":  "поставщик канцтоваров",
	}, cat); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if err := db.Upsert(ctx, cat.Name, other, map[string]any{
		"Наименование": "ЗАО Василёк",
		"ИНН":          "7809876543",
	}, cat); err != nil {
		t.Fatal(err)
	}
	invoice := uuid.New()
	if err := db.Upsert(ctx, doc.Name, invoice, map[string]any{
		"Номер":     "РН-000012",
		"Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}

	hits := search(t, db, "ромашк")
	ids := hitIDs(hits)
	if !ids[romashka] || !ids[invoice] {
		t.Fatalf("ожидались справочник и документ, получено %+v", hits)
	}
	if ids[other] {
		t.Fatalf("несовпадающий объект попал в выдачу: %+v", hits)
	}
	// Совпадение в представлении (вес A) обгоняет совпадение в прочем тексте.
	if hits[0].ID != romashka {
		t.Fatalf("первым ожидался объект с совпадением в наименовании, получено %+v", hits)
	}
	if hits[0].Title != "ООО Ромашка" || hits[0].Kind != "catalog" || hits[0].Name != "Контрагент" {
		t.Fatalf("неожиданное представление совпадения: %+v", hits[0])
	}
}

func TestFullText_MatchesSecondaryFieldsAndPrefix(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat := entities[0]

	id := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{
		"Наименование": "ООО Ромашка",
		"ИНН":          "7701234567",
		"Комментарий":  "поставщик канцтоваров",
	}, cat); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"7701234567", "канцтоваров", "канцтов", "770123"} {
		if hits := search(t, db, q); len(hits) != 1 || hits[0].ID != id {
			t.Fatalf("запрос %q: ожидалось одно совпадение, получено %+v", q, hits)
		}
	}
	// Несколько слов соединяются по И: объект должен содержать оба.
	if hits := search(t, db, "ромашка канцтовар"); len(hits) != 1 {
		t.Fatalf("ожидалось совпадение по двум словам, получено %+v", hits)
	}
	if hits := search(t, db, "ромашка велосипед"); len(hits) != 0 {
		t.Fatalf("слово вне объекта не должно давать совпадение: %+v", hits)
	}
}

// Знаки препинания внутри значения не должны прятать его части от поиска.
// Разборщик PostgreSQL склеивает знак с числом («РН-000012» → «-000012»,
// «+79990001122» → «+79990001122»), поэтому индексируемый текст нормализуется
// до записи — иначе движки отвечали бы по-разному на один и тот же запрос.
func TestFullText_FindsPartsOfPunctuatedValues(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat, doc := entities[0], entities[1]

	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
		"Наименование": "ООО «Ромашка-Плюс»",
		"Комментарий":  "тел. +79990001122, e-mail sales@romashka.ru",
	}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": "РН-000012"}, doc); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"000012", "рн", "79990001122", "7999", "плюс", "sales", "romashka"} {
		if hits := search(t, db, q); len(hits) != 1 {
			t.Fatalf("запрос %q: ожидалось одно совпадение, получено %+v", q, hits)
		}
	}
}

func TestFTSNormalize(t *testing.T) {
	cases := map[string]string{
		"РН-000012":            "РН 000012",
		"  +7 (999) 000-11-22": "7 999 000 11 22",
		"ООО «Ромашка-Плюс»":   "ООО Ромашка Плюс",
		"sales@romashka.ru":    "sales romashka ru",
		"":                     "",
		"!!!":                  "",
	}
	for in, want := range cases {
		if got := ftsNormalize(in); got != want {
			t.Fatalf("ftsNormalize(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestFullText_IncrementalUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat := entities[0]

	id := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 1 {
		t.Fatalf("после записи ожидалось совпадение, получено %+v", hits)
	}

	// Переименование убирает старый текст из индекса и добавляет новый.
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Василёк"}, cat); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 0 {
		t.Fatalf("после переименования старое имя не должно находиться: %+v", hits)
	}
	if hits := search(t, db, "василёк"); len(hits) != 1 {
		t.Fatalf("после переименования новое имя должно находиться: %+v", hits)
	}

	if err := db.Delete(ctx, cat.Name, id); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "василёк"); len(hits) != 0 {
		t.Fatalf("удалённый объект остался в индексе: %+v", hits)
	}
}

func TestFullText_RolledBackWriteLeavesNoIndexRow(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat := entities[0]

	id := uuid.New()
	wantErr := context.Canceled
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		if err := db.Upsert(txCtx, cat.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
			return err
		}
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("ожидалась ошибка транзакции, получено %v", err)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 0 {
		t.Fatalf("откат записи должен откатывать и индекс, получено %+v", hits)
	}
}

func TestFullText_RestrictsToRequestedEntities(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat, doc := entities[0], entities[1]

	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{
		"Номер": "РН-1", "Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}

	hits := search(t, db, "ромашк", doc.Name)
	if len(hits) != 1 || hits[0].Name != doc.Name {
		t.Fatalf("выдача должна ограничиваться переданными объектами: %+v", hits)
	}
}

func TestFullText_ExplicitFieldListLimitsIndex(t *testing.T) {
	ctx := context.Background()
	e := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
		FullText:    []string{"Наименование"},
		FullTextSet: true,
	}
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, e.Name, uuid.New(), map[string]any{
		"Наименование": "ООО Ромашка",
		"Комментарий":  "секретная пометка",
	}, e); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 1 {
		t.Fatalf("перечисленный реквизит должен индексироваться: %+v", hits)
	}
	if hits := search(t, db, "секретная"); len(hits) != 0 {
		t.Fatalf("реквизит вне fulltext не должен индексироваться: %+v", hits)
	}
}

func TestFullText_EmptyFieldListExcludesEntity(t *testing.T) {
	ctx := context.Background()
	e := &metadata.Entity{
		Name:        "Контрагент",
		Kind:        metadata.KindCatalog,
		Fields:      []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		FullText:    nil,
		FullTextSet: true,
	}
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, e.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, e); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 0 {
		t.Fatalf("объект с пустым fulltext должен быть вне поиска: %+v", hits)
	}
}

// База, созданная версией до плана 82, не имеет таблицы _fts, пока не
// прогонят migrate. Запись объектов в такую базу обязана работать (иначе
// обновление платформы ломает прикладную работу), а поиск — отдавать пустую
// выдачу вместо ошибки.
func TestFullText_WriteAndSearchWithoutSchema(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat := entities[0]

	if _, err := db.Exec(ctx, "DROP TABLE "+ftsTable); err != nil {
		t.Fatal(err)
	}
	atomic.StoreInt32(&db.ftsState, ftsStateUnknown)

	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatalf("запись без схемы поиска не должна падать: %v", err)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 0 {
		t.Fatalf("без схемы поиска выдача должна быть пустой: %+v", hits)
	}
}

func TestFullText_RebuildRestoresIndexFromData(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat, doc := entities[0], entities[1]

	id := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{
		"Наименование": "ООО Ромашка",
		"Комментарий":  "поставщик канцтоваров",
	}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": "РН-000012"}, doc); err != nil {
		t.Fatal(err)
	}

	// Индекс потерян (правка мимо платформы, старая база) — reindex обязан
	// восстановить его из самих данных.
	if err := db.exec(ctx, "DELETE FROM "+ftsTable); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 0 {
		t.Fatalf("индекс должен быть пуст перед пересборкой: %+v", hits)
	}

	stats, err := db.RebuildFullTextIndex(ctx, entities, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[0].Indexed != 1 || stats[1].Indexed != 1 {
		t.Fatalf("неожиданная статистика пересборки: %+v", stats)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("после пересборки объект должен находиться: %+v", hits)
	}
	if hits := search(t, db, "канцтоваров"); len(hits) != 1 {
		t.Fatalf("после пересборки должны индексироваться все реквизиты: %+v", hits)
	}
	if hits := search(t, db, "000012"); len(hits) != 1 {
		t.Fatalf("после пересборки документ должен находиться по номеру: %+v", hits)
	}
}

func TestFullText_RebuildDropsRowsOfRemovedEntities(t *testing.T) {
	ctx := context.Background()
	db, entities := newFTSTestDB(t)
	cat, doc := entities[0], entities[1]

	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": "РН-000012"}, doc); err != nil {
		t.Fatal(err)
	}

	// Документ убрали из конфигурации: его строки не должны пережить пересборку,
	// иначе выдача будет вести на карточки несуществующего объекта.
	if _, err := db.RebuildFullTextIndex(ctx, []*metadata.Entity{cat}, 100, nil); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "000012"); len(hits) != 0 {
		t.Fatalf("строки исчезнувшего объекта остались в индексе: %+v", hits)
	}
	if hits := search(t, db, "ромашка"); len(hits) != 1 {
		t.Fatalf("оставшийся объект должен находиться: %+v", hits)
	}
}

func TestFullText_PredefinedItemsAreIndexed(t *testing.T) {
	ctx := context.Background()
	e := &metadata.Entity{
		Name:   "Склад",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Predefined: []*metadata.PredefinedItem{
			{Name: "ОсновнойСклад", Fields: map[string]any{"Наименование": "Основной склад"}},
		},
	}
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}

	hits := search(t, db, "основной")
	if len(hits) != 1 || hits[0].Title != "Основной склад" {
		t.Fatalf("предопределённый элемент должен индексироваться при migrate: %+v", hits)
	}
}

func TestFTSTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Ромашка договор", []string{"ромашка", "договор"}},
		{"  ООО \"Ромашка\", ИНН-770 ", []string{"ооо", "ромашка", "инн", "770"}},
		{"", nil},
		{"!!! ??? ***", nil},
		{`ромашка" OR "1`, []string{"ромашка", "or", "1"}},
	}
	for _, c := range cases {
		got := ftsTokens(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("ftsTokens(%q) = %v, ожидалось %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("ftsTokens(%q) = %v, ожидалось %v", c.in, got, c.want)
			}
		}
	}
}

// Правка существующего объекта идёт мимо upsert: UpsertVersioned с ненулевой
// ожидаемой версией делает собственный UPDATE. Через этот путь идут ВСЕ правки
// из UI (форма всегда шлёт _version), REST с If-Match и DSL. Без индексации
// там поиск отдавал старое значение — в том числе стёртое пользователем.
func TestFullText_ПравкаСВерсиейОбновляетИндекс(t *testing.T) {
	db, entities := newFTSTestDB(t)
	ctx := context.Background()
	cat := entities[0]

	id := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if len(search(t, db, "ромашка", cat.Name)) != 1 {
		t.Fatal("объект не проиндексирован при создании")
	}

	version := int64(1)
	if err := db.UpsertVersioned(ctx, cat.Name, id,
		map[string]any{"Наименование": "ООО Василёк"}, cat, &version); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "василек", cat.Name); len(hits) != 1 {
		t.Fatalf("новое имя не находится: %+v", hits)
	}
	if hits := search(t, db, "ромашка", cat.Name); len(hits) != 0 {
		t.Fatalf("старое значение осталось в индексе: %+v", hits)
	}
}

// Удаление объекта убирает его из индекса независимо от регистра имени, в
// котором пришёл вызов: REST v1 берёт имя из URL, и строгое сравнение с
// каноническим owner_name оставляло строку в индексе навсегда.
func TestFullText_УдалениеНеЗависитОтРегистраИмени(t *testing.T) {
	db, entities := newFTSTestDB(t)
	ctx := context.Background()
	cat := entities[0]

	id := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "КОНТРАГЕНТ", id); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "ромашка", cat.Name); len(hits) != 0 {
		t.Fatalf("строка индекса пережила удаление: %+v", hits)
	}
}

// «ё» и «е» ищутся одинаково: вводят обычно без точек, а свернуть их не умеет
// ни один из движков.
func TestFullText_ЁСворачиваетсяКЕ(t *testing.T) {
	db, entities := newFTSTestDB(t)
	ctx := context.Background()
	cat := entities[0]

	if err := db.Upsert(ctx, cat.Name, uuid.New(),
		map[string]any{"Наименование": "Королёв Артём"}, cat); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"королев", "Королёв", "артем", "Артём"} {
		if hits := search(t, db, q, cat.Name); len(hits) != 1 {
			t.Fatalf("запрос %q ничего не нашёл: %+v", q, hits)
		}
	}
}

// Обновление платформы на существующей базе: данные есть, индекса ещё нет.
// Migrate обязан наполнить его сам — иначе строка поиска в шапке работает,
// а находит ноль объектов, пока администратор не догадается про reindex.
func TestFullText_МиграцияНаполняетИндексНаСуществующейБазе(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entities := ftsTestEntities()
	cat := entities[0]

	// База «до обновления»: таблицы есть, полнотекстового индекса нет.
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, cat.Name, uuid.New(),
		map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "DROP TABLE "+ftsTable); err != nil {
		t.Fatal(err)
	}
	db.ftsState = ftsStateUnknown
	if hits := search(t, db, "ромашка", cat.Name); len(hits) != 0 {
		t.Fatalf("индекс не сброшен, проба некорректна: %+v", hits)
	}

	// Обновление платформы — повторный Migrate.
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, db, "ромашка", cat.Name); len(hits) != 1 {
		t.Fatalf("после миграции поиск не находит ранее записанное: %+v", hits)
	}
}

// #623: удаление из _fts ищет по одному owner_id, а первичный ключ ведёт по
// owner_name — для одиночного owner_id он неприменим, и обе СУБД брали полный
// скан общего индекса с горячего пути записи. Проверяем, что индекс по owner_id
// создан и удаление идёт по нему, а не сканом.
func TestFullText_DeleteUsesOwnerIDIndex(t *testing.T) {
	db, _ := newFTSTestDB(t)
	ctx := context.Background()

	// Индекс по owner_id должен существовать после миграции.
	var idxCount int
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='_fts' AND name='_fts_owner_id'`).Scan(&idxCount); err != nil {
		t.Fatal(err)
	}
	if idxCount != 1 {
		t.Fatalf("индекс _fts_owner_id не создан (%d)", idxCount)
	}

	// План удаления по owner_id обязан использовать индекс, а не полный скан.
	rows, err := db.Query(ctx, `EXPLAIN QUERY PLAN DELETE FROM _fts WHERE owner_id = ?`, uuid.New().String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "_fts_owner_id") {
		t.Fatalf("удаление из _fts не использует индекс по owner_id:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN _fts") && !strings.Contains(plan, "USING") {
		t.Fatalf("удаление из _fts идёт полным сканом:\n%s", plan)
	}
}
