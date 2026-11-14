package dslvars

import (
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func TestEffectiveAccessThroughDSL(t *testing.T) {
	master := &auth.Role{Name: "OTK", Permissions: auth.Permission{Catalogs: map[string][]string{"А_Мастера": {"read,write"}}}}
	warehouse := &auth.Role{Name: "ExtraWarehouse", Permissions: auth.Permission{Catalogs: map[string][]string{"Склады": {"write"}}}}
	for _, tc := range []struct {
		name string
		user *auth.User
		expr string
		want bool
	}{
		{"union", &auth.User{ID: "own", Roles: []*auth.Role{master, warehouse}}, `ЕстьПравоДоступа("catalog", "А_Мастера", "write") И HasAccessRight("Справочник", "склады", "Запись")`, true},
		{"one-role-insufficient", &auth.User{ID: "own", Roles: []*auth.Role{master}}, `ЕстьПравоДоступа("catalog", "Склады", "write")`, false},
		{"admin", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа("catalog", "Склады", "write")`, true},
		{"no-roles", &auth.User{ID: "own"}, `ЕстьПравоДоступа("catalog", "Склады", "write")`, false},
		{"no-actor", nil, `ЕстьПравоДоступа("catalog", "Склады", "write")`, false},
		{"no-identity", &auth.User{IsAdmin: true}, `ЕстьПравоДоступа("catalog", "Склады", "write")`, false},
		{"unknown-object-even-admin", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа("catalog", "Missing", "write")`, false},
		{"unknown-op-even-admin", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа("catalog", "Склады", "warp")`, false},
		{"wrong-kind-even-admin", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа("document", "Склады", "write")`, false},
		{"unknown-kind-even-admin", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа("Other", "Склады", "write")`, false},
		{"no-args", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа()`, false},
		{"other-user-not-accepted", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа("catalog", "Склады", "write", "admin")`, false},
		{"non-string", &auth.User{ID: "own", IsAdmin: true}, `ЕстьПравоДоступа("catalog", Неопределено, "write")`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, reg, ctx := constantsFixture(t)
			reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{{Name: "А_Мастера", Kind: metadata.KindCatalog}, {Name: "Склады", Kind: metadata.KindCatalog}}})
			if tc.user != nil {
				ctx = auth.ContextWithUser(ctx, tc.user)
			}
			vars := Common{Ctx: ctx, Store: db, Reg: reg}.Build()
			// Building an environment reads constants as before. The predicate
			// itself must work with the storage already closed: metadata+actor only.
			db.Close()
			prog, err := parser.New(lexer.New("Функция Test()\n Возврат "+tc.expr+";\nКонецФункции", "access.os")).ParseProgram()
			if err != nil {
				t.Fatal(err)
			}
			var result any
			if err := interpreter.New().RunWithResult(prog.Procedures[0], &interpreter.MapThis{M: map[string]any{}}, &result, vars); err != nil {
				t.Fatal(err)
			}
			if result != tc.want {
				t.Fatalf("predicate=%v, want %v", result, tc.want)
			}
		})
	}
}

func TestEffectiveAccessBareInterpreterDenies(t *testing.T) {
	prog, err := parser.New(lexer.New(`Функция Test()
Возврат HasAccessRight("catalog", "Склады", "write");
КонецФункции`, "bare.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := interpreter.New().RunWithResult(prog.Procedures[0], &interpreter.MapThis{M: map[string]any{}}, &result, nil); err != nil || result != false {
		t.Fatalf("bare predicate=%v err=%v", result, err)
	}
}
