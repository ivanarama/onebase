package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Regression #436: a reference copied into a table-part row before Записать()
// carries a manager bound to the processor's pre-transaction context. OnWrite
// must rebind it to the current transaction; otherwise ПолучитьОбъект() waits
// for a second connection forever because SQLite deliberately uses a pool of 1.
func TestDocWriterOnWriteRebindsTablePartReferenceToTransaction(t *testing.T) {
	// Это предохранитель от исходного deadlock, а не performance assertion.
	// Две секунды оказались меньше честного времени на загруженном Windows
	// runner: исторический отказ #1062 завершился за 5.28s, тогда как тот же код
	// в соседних прогонах проходил. Настоящий deadlock по-прежнему ограничен.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	outgoing := &metadata.Entity{
		Name:   "ИсходящееПисьмо",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	packet := &metadata.Entity{
		Name: "ПакетПочтовойОтправки",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "ПроверенныйНомер", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{
			Name: "Письма",
			Fields: []metadata.Field{{
				Name: "ИсходящееПисьмо", RefEntity: outgoing.Name,
			}},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{outgoing, packet}); err != nil {
		t.Fatal(err)
	}
	outgoingID := uuid.New()
	if err := db.Upsert(ctx, outgoing.Name, outgoingID, map[string]any{"Номер": "ИП-001"}, outgoing); err != nil {
		t.Fatal(err)
	}

	program := mustParse(t, `Процедура ПриЗаписи()
  Для Каждого СтрокаПакета Из ЭтотОбъект.Письма Цикл
    ИсходящийОбъект = СтрокаПакета.ИсходящееПисьмо.ПолучитьОбъект();
    ЭтотОбъект.ПроверенныйНомер = ИсходящийОбъект.Номер;
  КонецЦикла;
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{outgoing, packet},
		Programs: map[string]*ast.Program{packet.Name: program},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{
		store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore(),
	}

	// Both proxies start with the outer context. The reference therefore has a
	// stale manager by the time packet.write opens its transaction.
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	outgoingProxy := root.Get(outgoing.Name).(*docProxy)
	staleRef := &interpreter.Ref{
		UUID: outgoingID.String(), Name: "ИП-001",
		Type: outgoing.Name, Manager: outgoingProxy,
	}
	writer := root.Get(packet.Name).(*docProxy).CallMethod("создать", nil).(*docWriter)
	writer.Set("Номер", "ПП-001")
	row := writer.Get("Письма").(*tpProxy).CallMethod("добавить", nil).(*interpreter.MapThis)
	row.Set("ИсходящееПисьмо", staleRef)

	if err := writer.write(); err != nil {
		t.Fatalf("Записать с ПолучитьОбъект в ПриЗаписи: %v", err)
	}
	if got := writer.obj.Get("ПроверенныйНомер"); got != "ИП-001" {
		t.Fatalf("ПроверенныйНомер = %v, want ИП-001", got)
	}
}
