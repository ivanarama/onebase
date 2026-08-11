package entityservice

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

// BenchmarkSave_PostDocument меряет «проведение документа» целиком: DSL-хук
// OnPost (запись движения регистра накопления) + транзакция upsert + движения +
// SetPosted. Это самая представительная и тяжёлая операция onebase — прямой
// аналог «проведения документов» из теста Гилёва. SQLite в tempdir, без внешней
// инфраструктуры; для абсолютной оценки на PostgreSQL см. `onebase bench`.
func BenchmarkSave_PostDocument(b *testing.B) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		b.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		b.Fatal(err)
	}

	// OnPost: одно движение по регистру остатков из реквизитов шапки.
	onPost := mustParseBenchProgram(b, `Процедура OnPost()
  Дв = Движения.ОстаткиТоваров.Добавить();
  Дв.Номенклатура = this.Номенклатура;
  Дв.Количество = this.Количество;
  Дв.Сумма = this.Количество * this.Цена;
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
		Store:  db,
		Reg:    registry,
		Interp: interp,
		BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
			return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := svc.Save(ctx, SaveRequest{
			Entity: doc,
			ID:     uuid.New(),
			IsNew:  true,
			Fields: map[string]any{
				"Номенклатура": "Гвоздь",
				"Количество":   float64(10),
				"Цена":         float64(5),
			},
			Action: "post",
		})
		if err != nil {
			b.Fatalf("save: %v", err)
		}
		if res.DSLError != "" {
			b.Fatalf("OnPost DSL error: %s", res.DSLError)
		}
	}
}

func mustParseBenchProgram(b *testing.B, src string) *ast.Program {
	b.Helper()
	prog, err := parser.New(lexer.New(src, "bench.os")).ParseProgram()
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	return prog
}
