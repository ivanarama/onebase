package ui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Regression for #453: v0.9.6 injected a platform builtin named
// ЗаписатьСобытиеАудита before resolving application procedures. Existing
// configurations with a common-module procedure of that name were therefore
// routed to the new publish/rollback validator and could no longer save a
// document from DSL.
func TestDocsRoot_OnWriteApplicationAuditProcedureShadowsPlatformFallback(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "audit-compat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "МинимальныйДокумент",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "АудитСобытие", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	onWrite := mustParse(t, `Процедура ПриЗаписи()
  ЗаписатьСобытиеАудита("Запись", "МинимальныйДокумент", ЭтотОбъект);
КонецПроцедуры`)
	appAudit := mustParse(t, `Процедура ЗаписатьСобытиеАудита(Событие, Сущность, Объект)
  Объект.АудитСобытие = Событие;
КонецПроцедуры`)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: onWrite},
	})
	registry.LoadModules(map[string]*ast.Program{"ПрикладнойАудит": appAudit})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	interp.LookupSiblingProc = registry.GetSiblingProc
	server := &Server{
		store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore(),
	}
	server.entitySvc = server.newEntityService(nil)

	writer := newDocsRoot(server, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy).
		CallMethod("создать", nil).(*docWriter)
	writer.Set("Наименование", "test")
	writer.CallMethod("записать", nil)

	var name, auditEvent string
	if err := db.QueryRow(ctx,
		`SELECT наименование, аудитсобытие FROM минимальныйдокумент LIMIT 1`,
	).Scan(&name, &auditEvent); err != nil {
		t.Fatal(err)
	}
	if name != "test" || auditEvent != "Запись" {
		t.Fatalf("saved document = (%q, %q), want (test, Запись)", name, auditEvent)
	}
}
