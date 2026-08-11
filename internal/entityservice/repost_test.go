package entityservice

// Тест перепроведения документа для обмена (план 86): Service.Repost перечитывает
// уже записанный (непроведённый) документ, запускает OnPost и пишет движения —
// без Upsert и без изменения _version.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestRepost_WritesMovementsAndPosts(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name: "Поступление", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	onPost := mustParseProgramT(t, `Процедура OnPost()
  Дв = Движения.ОстаткиТоваров.Добавить();
  Дв.Номенклатура = this.Номенклатура;
  Дв.Количество = this.Количество;
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: []*metadata.Register{reg},
		Programs:  map[string]*ast.Program{doc.Name: onPost},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	svc := &Service{
		Store: db, Reg: registry, Interp: interp,
		BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
			return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
		},
	}

	// Документ записан непроведённым (как его оставляет ApplyPackage).
	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Номенклатура": "Гвоздь", "Количество": float64(10)}, doc); err != nil {
		t.Fatal(err)
	}
	verBefore, _ := db.EntityVersion(ctx, doc.Name, id)

	regCount := func() int {
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.RegisterTableName("ОстаткиТоваров")).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := regCount(); n != 0 {
		t.Fatalf("до перепроведения движений быть не должно, got %d", n)
	}

	if err := svc.Repost(ctx, "Поступление", id); err != nil {
		t.Fatalf("Repost: %v", err)
	}

	row, err := db.GetByID(ctx, doc.Name, id, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !repostBool(row["posted"]) {
		t.Error("после перепроведения документ должен быть проведён (posted=true)")
	}
	if n := regCount(); n != 1 {
		t.Fatalf("ожидали 1 движение регистра после перепроведения, got %d", n)
	}
	// _version не должен меняться (перепроведение не делает Upsert) — иначе
	// сломалась бы идемпотентность обмена по версии.
	if verAfter, _ := db.EntityVersion(ctx, doc.Name, id); verAfter != verBefore {
		t.Errorf("_version изменился при перепроведении: было %d, стало %d", verBefore, verAfter)
	}
}

// Хук перепроведения должен исполняться ВНУТРИ транзакции записи (issue #458):
// только так БлокировкаДанных из ОбработкаПроведения может взять
// pg_advisory_xact_lock до чтения остатков, а побочные записи хука
// откатываются вместе с движениями.
func TestRepost_HookRunsInsideTx(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rtx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name: "Поступление", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	onPost := mustParseProgramT(t, `Процедура OnPost()
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: onPost},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	hookSawTx := false
	svc := &Service{
		Store: db, Reg: registry, Interp: interp,
		BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
			hookSawTx = storage.HasTx(c)
			return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
		},
	}

	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Количество": float64(1)}, doc); err != nil {
		t.Fatal(err)
	}
	if err := svc.Repost(ctx, "Поступление", id); err != nil {
		t.Fatalf("Repost: %v", err)
	}
	if !hookSawTx {
		t.Error("BuildVars получил контекст без транзакции — хук Repost работает вне транзакции записи")
	}
}

func mustParseProgramT(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return prog
}

func repostBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	}
	return false
}
