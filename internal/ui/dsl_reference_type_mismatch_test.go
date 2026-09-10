package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Регрессия #1280 идёт через настоящий DSL-исходник и публичный Записать(), а
// не вызывает storage-нормализатор напрямую. Одна и та же проверка запускается
// на SQLite и PostgreSQL: несовместимая строка должна стать пользовательской
// ошибкой, а не NULL в шапке или строке табличной части.
func TestDocsRoot_WriteRejectsReferenceTypeMismatchMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{
			Name: "КонтрагентСсылки", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		refType := metadata.FieldType("reference:" + target.Name)
		doc := &metadata.Entity{
			Name: "ЗаказСсылки", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Клиент", Type: refType, RefEntity: target.Name},
			},
			TableParts: []metadata.TablePart{{
				Name:   "Товары",
				Fields: []metadata.Field{{Name: "Товар", Type: refType, RefEntity: target.Name}},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{target, doc}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		clientID, productID := uuid.New(), uuid.New()
		for id, name := range map[uuid.UUID]string{clientID: "Клиент", productID: "Товар"} {
			if err := db.Upsert(ctx, target.Name, id, map[string]any{"Наименование": name}, target); err != nil {
				t.Fatalf("seed %s: %v", name, err)
			}
		}

		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target, doc}})
		interp := interpreter.New()
		interp.LookupProc = registry.GetModuleProc
		s := &Server{
			store: db, reg: registry, interp: interp,
			lockMgr: runtime.NewLockManager(), messages: NewMessageStore(),
		}
		s.entitySvc = s.newEntityService(nil)

		valid := fmt.Sprintf(`
  Док = Документы.ЗаказСсылки.Создать();
  Док.Номер = "З-DSL";
  Док.Клиент = "%s";
  Стр = Док.Товары.Добавить();
  Стр.Товар = "%s";
  Док.Записать();
`, clientID, productID)
		if _, err := runDSLBody(t, s, valid); err != nil {
			t.Fatalf("valid reference write: %v", err)
		}

		docID := referenceDSLDocumentID(t, ctx, db, doc)
		assertDSLReferenceSnapshot(t, ctx, db, doc, docID, clientID, productID)

		_, err := runDSLBody(t, s, `
  Док = Документы.ЗаказСсылки.НайтиПоНомеру("З-DSL").ПолучитьОбъект();
  Док.Клиент = "не UUID";
  Док.Записать();
`)
		assertDSLReferenceError(t, err, "Клиент", "reference:"+target.Name, "Строка")
		assertDSLReferenceSnapshot(t, ctx, db, doc, docID, clientID, productID)

		_, err = runDSLBody(t, s, `
  Док = Документы.ЗаказСсылки.НайтиПоНомеру("З-DSL").ПолучитьОбъект();
  Стр = Док.Товары.Получить(0);
  Стр.Товар = "не UUID";
  Док.Записать();
`)
		assertDSLReferenceError(t, err, "Товар", "reference:"+target.Name, "Строка")
		assertDSLReferenceSnapshot(t, ctx, db, doc, docID, clientID, productID)
	})
}

func referenceDSLDocumentID(t *testing.T, ctx context.Context, db *storage.DB, doc *metadata.Entity) uuid.UUID {
	t.Helper()
	query := fmt.Sprintf("SELECT CAST(id AS TEXT) FROM %s WHERE номер = %s",
		metadata.TableName(doc.Name), db.Dialect().Placeholder(1))
	var idText string
	if err := db.QueryRow(ctx, query, "З-DSL").Scan(&idText); err != nil {
		t.Fatalf("load DSL document id: %v", err)
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		t.Fatalf("parse DSL document id %q: %v", idText, err)
	}
	return id
}

func assertDSLReferenceError(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Записать() accepted an incompatible reference value")
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("user error %q does not contain %q", err, part)
		}
	}
}

func assertDSLReferenceSnapshot(t *testing.T, ctx context.Context, db *storage.DB, doc *metadata.Entity,
	docID, clientID, productID uuid.UUID) {
	t.Helper()
	row, err := db.GetByID(ctx, doc.Name, docID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(row["Клиент"]); got != clientID.String() {
		t.Errorf("Клиент = %q, want %s", got, clientID)
	}
	rows, err := db.GetTablePartRows(ctx, doc.Name, doc.TableParts[0].Name, docID, doc.TableParts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("Товары rows = %d, want 1: %#v", len(rows), rows)
	}
	if got := fmt.Sprint(rows[0]["Товар"]); got != productID.String() {
		t.Errorf("Товар = %q, want %s", got, productID)
	}
}
