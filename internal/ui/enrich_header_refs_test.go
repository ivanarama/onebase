package ui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// П.37: ссылочные поля ШАПКИ при проведении должны обогащаться до *Ref,
// чтобы Строка(this.Склад) давало имя, а не UUID (симметрично строкам ТЧ).
func TestEnrichHeaderRefs_UUIDToRef(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sklad := &metadata.Entity{
		Name:   "Склад",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc := &metadata.Entity{
		Name: "ПоступлениеТоваров",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Склад", Type: "reference:Склад", RefEntity: "Склад"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{sklad, doc}); err != nil {
		t.Fatal(err)
	}

	// записываем склад
	skladID := uuid.New()
	if err := db.Upsert(ctx, "Склад", skladID, map[string]any{"Наименование": "Основной"}, sklad); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{sklad, doc}})
	s := &Server{store: db, reg: registry}
	s.entitySvc = s.newEntityService(nil)

	// шапка документа: Склад приходит сырым UUID-строкой (как из формы)
	obj := &runtime.Object{
		ID:     uuid.New(),
		Type:   "ПоступлениеТоваров",
		Kind:   metadata.KindDocument,
		Fields: map[string]any{"номер": "ПОС-1", "склад": skladID.String()},
	}

	s.enrichHeaderRefs(ctx, doc, obj)

	v := obj.Get("Склад")
	ref, ok := v.(*interpreter.Ref)
	if !ok {
		t.Fatalf("Склад → %T, ожидался *interpreter.Ref", v)
	}
	if ref.UUID != skladID.String() {
		t.Errorf("UUID не сохранился: %s", ref.UUID)
	}
	if ref.Name != "Основной" {
		t.Errorf("Name = %q, ожидалось «Основной» (Строка() дал бы имя)", ref.Name)
	}
	// не-ссылочное поле не трогаем
	if obj.Get("Номер") != "ПОС-1" {
		t.Errorf("номер изменился: %v", obj.Get("Номер"))
	}
}

// Корень бага: enrichHeaderRefs делал obj.Set(f.Name, ref) — это писало
// Ref под lowercase-ключом, а оригинальный PascalCase-ключ с UUID-строкой
// оставался. Из-за случайного порядка обхода Go-мапы obj.Get() мог вернуть
// то Ref, то UUID — недетерминированно. Фикс: замена in-place по
// оригинальному ключу. Проверяем что НЕТ дубля ключей и нет сырого UUID.
func TestEnrichHeaderRefs_NoDuplicateKey(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	contr := &metadata.Entity{
		Name:   "Контрагент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc := &metadata.Entity{
		Name: "Счёт",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: "reference:Контрагент", RefEntity: "Контрагент"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{contr, doc}); err != nil {
		t.Fatal(err)
	}
	cid := uuid.New()
	if err := db.Upsert(ctx, "Контрагент", cid, map[string]any{"Наименование": "ООО Ромашка"}, contr); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{contr, doc}})
	s := &Server{store: db, reg: registry}
	s.entitySvc = s.newEntityService(nil)

	// Ключ в PascalCase (как кладёт форма) с UUID-строкой.
	obj := &runtime.Object{
		ID:     uuid.New(),
		Type:   "Счёт",
		Kind:   metadata.KindDocument,
		Fields: map[string]any{"Контрагент": cid.String()},
	}
	s.enrichHeaderRefs(ctx, doc, obj)

	// Должен остаться РОВНО один ключ для контрагента, и он = *Ref.
	var refKeys int
	for k, v := range obj.Fields {
		if k == "Контрагент" || k == "контрагент" {
			refKeys++
			if _, ok := v.(*interpreter.Ref); !ok {
				t.Errorf("ключ %q держит %T, ожидался *Ref (сырой UUID не заменён)", k, v)
			}
		}
	}
	if refKeys != 1 {
		t.Errorf("ожидался 1 ключ контрагента, найдено %d (дубль ключей!): %v", refKeys, obj.Fields)
	}
}

// Поле, уже являющееся *Ref (проведение из обработки), сохраняет идентичность
// ссылки, но получает manager текущего контекста. Это не даёт старому manager
// выйти из открытой SQLite-транзакции за вторым соединением (#436).
func TestEnrichHeaderRefs_RebindsExistingRefManager(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sklad := &metadata.Entity{
		Name:   "Склад",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc := &metadata.Entity{
		Name: "ПоступлениеТоваров",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Склад", Type: "reference:Склад", RefEntity: "Склад"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{sklad, doc}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{sklad, doc}})
	s := &Server{store: db, reg: registry}
	s.entitySvc = s.newEntityService(nil)

	orig := &interpreter.Ref{UUID: uuid.New().String(), Name: "ИзОбработки", Type: "Склад"}
	obj := &runtime.Object{
		ID:     uuid.New(),
		Type:   "ПоступлениеТоваров",
		Kind:   metadata.KindDocument,
		Fields: map[string]any{"склад": orig},
	}
	s.enrichHeaderRefs(ctx, doc, obj)

	v := obj.Get("Склад")
	ref, ok := v.(*interpreter.Ref)
	if !ok {
		t.Fatalf("Склад → %T, ожидался *interpreter.Ref", v)
	}
	if ref == orig {
		t.Error("существующий *Ref сохранил manager старого контекста")
	}
	if ref.UUID != orig.UUID || ref.Name != orig.Name || ref.Type != orig.Type {
		t.Fatalf("перепривязка изменила ссылку: got=%+v want=%+v", ref, orig)
	}
	if ref.Manager == nil {
		t.Fatal("перепривязанная ссылка не получила manager текущего контекста")
	}
}
