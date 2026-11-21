package dslvars

import (
	"context"
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
)

type effectiveAccessContextSource struct{ ctx context.Context }

func (s *effectiveAccessContextSource) Ctx() context.Context { return s.ctx }

// Metadata resolution must agree with the native object kinds; the same
// environment also must observe its live actor rather than cache a prior grant.
func TestEffectiveAccessKindsAndLiveActor(t *testing.T) {
	db, reg, ctx := constantsFixture(t)
	reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{{Name: "Каталог", Kind: metadata.KindCatalog}, {Name: "Документ", Kind: metadata.KindDocument}},
		Registers: []*metadata.Register{{Name: "Накопление"}},
		InfoRegs:  []*metadata.InfoRegister{{Name: "Сведения"}},
		Reports:   []*report.Report{{Name: "Отчёт"}},
	})
	reg.LoadProcessors([]*processor.Processor{{Name: "Обработка"}})
	role := &auth.Role{Name: "OwnRights", Permissions: auth.Permission{
		Catalogs:   map[string][]string{"Каталог": {"write"}},
		Documents:  map[string][]string{"Документ": {"post"}},
		Registers:  map[string][]string{"Накопление": {"read"}},
		InfoRegs:   map[string][]string{"Сведения": {"write"}},
		Reports:    map[string][]string{"Отчёт": {"run"}},
		Processors: map[string][]string{"Обработка": {"run"}},
	}}
	actor := &auth.User{ID: "own", Roles: []*auth.Role{role}}
	live := &effectiveAccessContextSource{ctx: auth.ContextWithUser(ctx, actor)}
	vars := Common{Ctx: ctx, CtxSource: live, Store: db, Reg: reg}.Build()
	db.Close() // The predicate itself must not read constants, roles or objects.
	for _, tc := range []struct{ kind, name, op string }{
		{"Справочник", "каталог", "Запись"}, {"document", "документ", "post"},
		{"register", "накопление", "read"}, {"inforeg", "сведения", "write"},
		{"report", "отчёт", "run"}, {"processor", "обработка", "run"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			program, err := parser.New(lexer.New(fmt.Sprintf("Функция Test()\nВозврат HasAccessRight(%q,%q,%q);\nКонецФункции", tc.kind, tc.name, tc.op), "access.os")).ParseProgram()
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range []struct {
				name string
				ctx  context.Context
				want bool
			}{
				{"granted", auth.ContextWithUser(ctx, actor), true},
				{"revoked", auth.ContextWithUser(ctx, &auth.User{ID: "own"}), false},
				{"anonymous", ctx, false}, {"nil-context", nil, false},
			} {
				live.ctx = step.ctx
				var result any
				if err := interpreter.New().RunWithResult(program.Procedures[0], &interpreter.MapThis{M: map[string]any{}}, &result, vars); err != nil || result != step.want {
					t.Fatalf("%s: got=%v want=%v err=%v", step.name, result, step.want, err)
				}
			}
		})
	}
}
