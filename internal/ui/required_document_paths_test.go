package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// requiredDocumentServer extends the shared write-path parity fixture with a
// required header and, when requested, a required table-part column. Required
// is a write-time metadata invariant, so no second header migration is needed;
// the table part does need its physical table.
func requiredDocumentServer(t *testing.T, module string, withTablePart bool) (*Server, *metadata.Entity, *storage.DB, context.Context) {
	t.Helper()
	s, doc, db, ctx := parityServer(t, module)
	for i := range doc.Fields {
		if strings.EqualFold(doc.Fields[i].Name, "Статус") {
			doc.Fields[i].Required = true
		}
	}
	if withTablePart {
		doc.TableParts = []metadata.TablePart{{
			Name: "Товары",
			Fields: []metadata.Field{{
				Name: "Номенклатура", Type: metadata.FieldTypeString, Required: true,
			}},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatalf("migrate required document table part: %v", err)
		}
	}
	return s, doc, db, ctx
}

func newRequiredDocumentWriter(t *testing.T, s *Server, ctx context.Context) *docWriter {
	t.Helper()
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	proxy, ok := root.Get("Заказ").(*docProxy)
	if !ok {
		t.Fatalf("Документы.Заказ = %T, want *docProxy", root.Get("Заказ"))
	}
	w, ok := proxy.CallMethod("создать", nil).(*docWriter)
	if !ok {
		t.Fatalf("Создать() = %T, want *docWriter", w)
	}
	return w
}

func loadRequiredDocumentWriter(t *testing.T, s *Server, ctx context.Context, id uuid.UUID) *docWriter {
	t.Helper()
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	proxy, ok := root.Get("Заказ").(*docProxy)
	if !ok {
		t.Fatalf("Документы.Заказ = %T, want *docProxy", root.Get("Заказ"))
	}
	ref, ok := proxy.CallMethod("найтипоидентификатору", []any{id.String()}).(*interpreter.Ref)
	if !ok {
		t.Fatalf("НайтиПоИдентификатору() = %T, want *interpreter.Ref", ref)
	}
	w, ok := ref.CallMethod("получитьобъект", nil).(*docWriter)
	if !ok {
		t.Fatalf("ПолучитьОбъект() = %T, want *docWriter", w)
	}
	return w
}

func callRequiredDocumentMethod(w *docWriter, method string) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%v", rec)
		}
	}()
	w.CallMethod(method, nil)
	return nil
}

func seedRequiredDocument(t *testing.T, db *storage.DB, ctx context.Context, doc *metadata.Entity) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{
		"Номер": "З-1", "Статус": "Новый",
	}, doc); err != nil {
		t.Fatalf("seed required document: %v", err)
	}
	return id
}

func assertRequiredDocumentState(t *testing.T, db *storage.DB, ctx context.Context, doc *metadata.Entity, id uuid.UUID, status string, version int64, posted bool) {
	t.Helper()
	row, err := db.GetByID(ctx, doc.Name, id, doc)
	if err != nil {
		t.Fatalf("reload document: %v", err)
	}
	if got := fmt.Sprint(row["Статус"]); got != status {
		t.Errorf("persisted Статус = %q, want %q", got, status)
	}
	gotVersion, err := db.EntityVersion(ctx, doc.Name, id)
	if err != nil {
		t.Fatalf("reload version: %v", err)
	}
	if gotVersion != version {
		t.Errorf("persisted version = %d, want %d", gotVersion, version)
	}
	if got := asBool(row["posted"]); got != posted {
		t.Errorf("persisted posted = %v, want %v", got, posted)
	}
}

func TestRequiredDocument_DSLCreateMissingHeaderRejected(t *testing.T) {
	s, doc, db, ctx := requiredDocumentServer(t, "", false)
	w := newRequiredDocumentWriter(t, s, ctx)
	w.Set("Номер", "З-EMPTY")

	err := callRequiredDocumentMethod(w, "записать")
	if err == nil {
		t.Fatal("Записать() accepted a new document without required Статус")
	}
	if !strings.Contains(err.Error(), "Статус") {
		t.Fatalf("required rejection does not identify Статус: %v", err)
	}
	if _, getErr := db.GetByID(ctx, doc.Name, w.obj.ID, doc); !storage.IsNotFound(getErr) {
		t.Fatalf("rejected document was persisted: %v", getErr)
	}
}

func TestRequiredDocument_DSLOnWriteCanFillHeaderBeforeValidation(t *testing.T) {
	module := `Процедура ПриЗаписи()
  ЭтотОбъект.Статус = "Закрыт";
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, false)
	w := newRequiredDocumentWriter(t, s, ctx)
	w.Set("Номер", "З-HOOK")

	if err := callRequiredDocumentMethod(w, "записать"); err != nil {
		t.Fatalf("OnWrite-filled required header was rejected: %v", err)
	}
	assertRequiredDocumentState(t, db, ctx, doc, w.obj.ID, "Закрыт", 1, false)

	// Reload through a fresh DSL object, not the mutable writer that ran the
	// hook: this proves the value reached storage under its canonical field.
	reloaded := loadRequiredDocumentWriter(t, s, ctx, w.obj.ID)
	if got := fmt.Sprint(reloaded.Get("Статус")); got != "Закрыт" {
		t.Fatalf("fresh DSL reload Статус = %q, want %q", got, "Закрыт")
	}
}

func TestRequiredDocument_DSLOnWriteClearRollsBack(t *testing.T) {
	module := `Процедура ПриЗаписи()
  ЭтотОбъект.Статус = "";
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, false)
	id := seedRequiredDocument(t, db, ctx, doc)
	w := loadRequiredDocumentWriter(t, s, ctx, id)

	err := callRequiredDocumentMethod(w, "записать")
	if err == nil {
		t.Fatal("Записать() accepted OnWrite-cleared required Статус")
	}
	if !strings.Contains(err.Error(), "Статус") {
		t.Fatalf("required rejection does not identify Статус: %v", err)
	}
	assertRequiredDocumentState(t, db, ctx, doc, id, "Новый", 1, false)
}

func TestRequiredDocument_DSLOnPostClearRollsBackImplicitWrite(t *testing.T) {
	module := `Процедура ОбработкаПроведения()
  ЭтотОбъект.Статус = "";
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, false)
	id := seedRequiredDocument(t, db, ctx, doc)
	w := loadRequiredDocumentWriter(t, s, ctx, id)

	err := callRequiredDocumentMethod(w, "провести")
	if err == nil {
		t.Fatal("Провести() accepted OnPost-cleared required Статус")
	}
	if !strings.Contains(err.Error(), "Статус") {
		t.Fatalf("required rejection does not identify Статус: %v", err)
	}
	// Провести() first performs an implicit versioned write. The OnPost
	// rejection must roll that write back together with posted state.
	assertRequiredDocumentState(t, db, ctx, doc, id, "Новый", 1, false)
}

func TestRequiredDocument_DSLTablePartRejectionIsAtomic(t *testing.T) {
	s, doc, db, ctx := requiredDocumentServer(t, "", true)
	id := seedRequiredDocument(t, db, ctx, doc)
	tp := doc.TableParts[0]
	if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id, []map[string]any{{
		"Номенклатура": "Стул",
	}}, tp); err != nil {
		t.Fatalf("seed table part: %v", err)
	}

	w := loadRequiredDocumentWriter(t, s, ctx, id)
	row, ok := w.Get(tp.Name).(*tpProxy).CallMethod("получить", []any{float64(0)}).(*interpreter.MapThis)
	if !ok {
		t.Fatal("Товары.Получить(0) did not return a table-part row")
	}
	row.Set("Номенклатура", "   ")

	err := callRequiredDocumentMethod(w, "записать")
	if err == nil {
		t.Fatal("Записать() accepted a blank required table-part value")
	}
	if !strings.Contains(err.Error(), "Товары[1].Номенклатура") {
		t.Fatalf("table-part rejection does not identify the row and field: %v", err)
	}
	assertRequiredDocumentState(t, db, ctx, doc, id, "Новый", 1, false)
	rows, err := db.GetTablePartRows(ctx, doc.Name, tp.Name, id, tp)
	if err != nil {
		t.Fatalf("reload table part: %v", err)
	}
	if len(rows) != 1 || fmt.Sprint(rows[0]["Номенклатура"]) != "Стул" {
		t.Fatalf("rejected table-part replacement changed persisted rows: %#v", rows)
	}
}

func TestRequiredDocument_ListButtonOnPostClearRejected(t *testing.T) {
	module := `Процедура ОбработкаПроведения()
  ЭтотОбъект.Статус = "";
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, false)
	id := seedRequiredDocument(t, db, ctx, doc)

	r := reqWithChi("POST", "/ui/document/заказ/"+id.String()+"/post", url.Values{},
		map[string]string{"kind": "document", "entity": "заказ", "id": id.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("list post status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	location := rec.Header().Get("Location")
	decoded, err := url.QueryUnescape(location)
	if err != nil {
		t.Fatalf("decode posting redirect: %v", err)
	}
	if !strings.Contains(decoded, "posting_error=") || !strings.Contains(decoded, "Статус") {
		t.Fatalf("list post did not report required Статус: %q", decoded)
	}
	assertRequiredDocumentState(t, db, ctx, doc, id, "Новый", 1, false)
}
