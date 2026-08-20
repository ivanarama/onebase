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

func TestRequiredDocument_DSLOnPostFillsHeaderAndTablePartBeforeFinalWrite(t *testing.T) {
	module := `Процедура ОбработкаПроведения()
  ЭтотОбъект.Статус = "Закрыт";
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Стр.Номенклатура = "Стол";
  КонецЦикла;
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, true)
	w := newRequiredDocumentWriter(t, s, ctx)
	w.Set("Номер", "З-POST-FILL")
	tp := doc.TableParts[0]
	proxy, ok := w.Get(tp.Name).(*tpProxy)
	if !ok {
		t.Fatalf("%s = %T, want *tpProxy", tp.Name, w.Get(tp.Name))
	}
	if row, ok := proxy.CallMethod("добавить", nil).(*interpreter.MapThis); !ok || row == nil {
		t.Fatalf("%s.Добавить() = %T, want row", tp.Name, row)
	}

	if err := callRequiredDocumentMethod(w, "провести"); err != nil {
		t.Fatalf("OnPost-filled required object was rejected: %v", err)
	}
	assertRequiredDocumentState(t, db, ctx, doc, w.obj.ID, "Закрыт", 1, true)

	// Only a fresh DB-backed object proves both final writers consumed the same
	// live maps that OnPost mutated, rather than merely validating them in memory.
	reloaded := loadRequiredDocumentWriter(t, s, ctx, w.obj.ID)
	if got := fmt.Sprint(reloaded.Get("Статус")); got != "Закрыт" {
		t.Fatalf("fresh DSL reload Статус = %q, want Закрыт", got)
	}
	rows, err := db.GetTablePartRows(ctx, doc.Name, tp.Name, w.obj.ID, tp)
	if err != nil {
		t.Fatalf("fresh table-part reload: %v", err)
	}
	if len(rows) != 1 || fmt.Sprint(rows[0]["Номенклатура"]) != "Стол" {
		t.Fatalf("fresh table-part reload = %#v, want OnPost-filled row", rows)
	}
}

func TestRequiredDocument_DSLOnPostTablePartClearRollsBackPrelude(t *testing.T) {
	module := `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Стр.Номенклатура = "";
  КонецЦикла;
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, true)
	id := seedRequiredDocument(t, db, ctx, doc)
	tp := doc.TableParts[0]
	if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id,
		[]map[string]any{{"Номенклатура": "Стул"}}, tp); err != nil {
		t.Fatalf("seed table part: %v", err)
	}
	w := loadRequiredDocumentWriter(t, s, ctx, id)

	err := callRequiredDocumentMethod(w, "провести")
	if err == nil || !strings.Contains(err.Error(), "Товары[1].Номенклатура") {
		t.Fatalf("OnPost-cleared TP required value: got %v", err)
	}
	assertRequiredDocumentState(t, db, ctx, doc, id, "Новый", 1, false)
	rows, loadErr := db.GetTablePartRows(ctx, doc.Name, tp.Name, id, tp)
	if loadErr != nil {
		t.Fatalf("reload table part: %v", loadErr)
	}
	if len(rows) != 1 || fmt.Sprint(rows[0]["Номенклатура"]) != "Стул" {
		t.Fatalf("rejected OnPost changed persisted rows: %#v", rows)
	}
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

func TestRequiredDocument_ListButtonPersistsOnPostTablePartMutation(t *testing.T) {
	module := `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Стр.Номенклатура = "Стол";
  КонецЦикла;
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, true)
	id := seedRequiredDocument(t, db, ctx, doc)
	tp := doc.TableParts[0]
	if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id,
		[]map[string]any{{"Номенклатура": "Стул"}}, tp); err != nil {
		t.Fatalf("seed table part: %v", err)
	}

	r := reqWithChi("POST", "/ui/document/заказ/"+id.String()+"/post", url.Values{},
		map[string]string{"kind": "document", "entity": "заказ", "id": id.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, r)
	if rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "posting_error=") {
		t.Fatalf("list post failed: code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	assertRequiredDocumentState(t, db, ctx, doc, id, "Новый", 2, true)
	rows, err := db.GetTablePartRows(ctx, doc.Name, tp.Name, id, tp)
	if err != nil {
		t.Fatalf("reload table part: %v", err)
	}
	if len(rows) != 1 || fmt.Sprint(rows[0]["Номенклатура"]) != "Стол" {
		t.Fatalf("list-post OnPost TP mutation was not persisted: %#v", rows)
	}
}

func TestRequiredDocument_ListButtonOnPostTablePartClearRollsBack(t *testing.T) {
	module := `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Стр.Номенклатура = "";
  КонецЦикла;
КонецПроцедуры`
	s, doc, db, ctx := requiredDocumentServer(t, module, true)
	id := seedRequiredDocument(t, db, ctx, doc)
	tp := doc.TableParts[0]
	if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id,
		[]map[string]any{{"Номенклатура": "Стул"}}, tp); err != nil {
		t.Fatalf("seed table part: %v", err)
	}

	r := reqWithChi("POST", "/ui/document/заказ/"+id.String()+"/post", url.Values{},
		map[string]string{"kind": "document", "entity": "заказ", "id": id.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, r)
	decoded, err := url.QueryUnescape(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("decode posting redirect: %v", err)
	}
	if rec.Code != http.StatusSeeOther || !strings.Contains(decoded, "Товары[1].Номенклатура") {
		t.Fatalf("list post did not reject cleared required TP: code=%d location=%q", rec.Code, decoded)
	}
	assertRequiredDocumentState(t, db, ctx, doc, id, "Новый", 1, false)
	rows, loadErr := db.GetTablePartRows(ctx, doc.Name, tp.Name, id, tp)
	if loadErr != nil {
		t.Fatalf("reload table part: %v", loadErr)
	}
	if len(rows) != 1 || fmt.Sprint(rows[0]["Номенклатура"]) != "Стул" {
		t.Fatalf("rejected list post changed persisted rows: %#v", rows)
	}
}

func TestSaveTablePartsDirect_OmittedPreservesExplicitEmptyClears(t *testing.T) {
	s, doc, db, ctx := requiredDocumentServer(t, "", true)
	id := seedRequiredDocument(t, db, ctx, doc)
	tp := doc.TableParts[0]
	seed := []map[string]any{{"Номенклатура": "Стул"}}
	if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id, seed, tp); err != nil {
		t.Fatalf("seed table part: %v", err)
	}

	if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		if err := s.saveTablePartsPostingPrelude(txCtx, doc, id, map[string][]map[string]any{
			tp.Name: {{"Номенклатура": "Промежуточное"}},
		}); err != nil {
			return err
		}
		return s.finalizeTablePartsPostingPrelude(txCtx, doc, id, map[string][]map[string]any{})
	}); err != nil {
		t.Fatalf("persist omitted table part: %v", err)
	}
	rows, err := db.GetTablePartRows(ctx, doc.Name, tp.Name, id, tp)
	if err != nil || len(rows) != 1 || fmt.Sprint(rows[0]["Номенклатура"]) != "Стул" {
		t.Fatalf("omitted table part did not preserve rows: rows=%#v err=%v", rows, err)
	}

	if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		if err := s.saveTablePartsPostingPrelude(txCtx, doc, id,
			map[string][]map[string]any{tp.Name: seed}); err != nil {
			return err
		}
		return s.finalizeTablePartsPostingPrelude(txCtx, doc, id,
			map[string][]map[string]any{tp.Name: nil})
	}); err != nil {
		t.Fatalf("clear explicit table part: %v", err)
	}
	rows, err = db.GetTablePartRows(ctx, doc.Name, tp.Name, id, tp)
	if err != nil || len(rows) != 0 {
		t.Fatalf("explicit empty table part did not clear rows: rows=%#v err=%v", rows, err)
	}
}
