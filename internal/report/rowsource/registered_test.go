package rowsource

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
)

func registeredDefinition() *report.RowSourceDefinition {
	return &report.RowSourceDefinition{Procedure: "Build", Columns: []report.RowSourceColumn{{Name: "Works", Type: "string"}}}
}
func TestRowSourceRegisteredBinding(t *testing.T) {
	ctx, db, reg, _, _, id := fixture(t)
	reg.GetReport("RowSource").RowSource = registeredDefinition()
	called := 0
	result, err := RunRegistered(ctx, db, reg, "rOwSoUrCe", Query{Text: initialQuery}, func(ctx context.Context, name string) (string, error) {
		called++
		if name != "RowSource" {
			t.Fatalf("noncanonical loader key %q", name)
		}
		return enrichCode, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || len(result.Rows) != 1 || result.Rows[0]["request"] != id.String() || result.Rows[0]["Works"] != "Работа А; \nРабота А; \nЗаметка для клиента" {
		t.Fatalf("registered result changed: %+v", result)
	}
	if len(result.Columns[len(result.Columns)-1].Origins) == 0 || result.Generation != reg.Generation() {
		t.Fatal("registered path lost lineage/generation")
	}
}
func TestRowSourceRegisteredRejectsBeforeLoader(t *testing.T) {
	for _, mode := range []string{"absent", "unknown", "invalid", "denied", "cancel", "external"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, _, _, _ := fixture(t)
			rep := reg.GetReport("RowSource")
			rep.RowSource = registeredDefinition()
			name := "RowSource"
			switch mode {
			case "absent":
				rep.RowSource = nil
			case "external":
				rep.External = true
			case "unknown":
				name = "../foreign"
			case "invalid":
				rep.RowSource.Columns[0].Type = "reference:RowRequest"
			case "denied":
				ctx = auth.ContextWithUser(ctx, &auth.User{Login: "reader", Roles: []*auth.Role{{Name: "R", Permissions: auth.Permission{Reports: map[string][]string{}}}}})
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			called := false
			result, err := RunRegistered(ctx, db, reg, name, Query{Text: initialQuery}, func(context.Context, string) (string, error) { called = true; return enrichCode, nil })
			if err == nil || result != nil || called {
				t.Fatal("invalid binding reached loader or produced result")
			}
		})
	}
}
func TestRowSourceRegisteredLoaderAndGeneration(t *testing.T) {
	for _, mode := range []string{"error", "reload", "revoked", "unsafe_code", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, ent, _, _ := fixture(t)
			rep := reg.GetReport("RowSource")
			rep.RowSource = registeredDefinition()
			perm := auth.Permission{Reports: map[string][]string{"RowSource": {"run"}}, Documents: map[string][]string{"RowRequest": {"read"}}}
			user := &auth.User{Login: "reader", Roles: []*auth.Role{{Name: "R", Permissions: perm}}}
			ctx = auth.ContextWithUser(ctx, user)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			result, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: initialQuery}, func(context.Context, string) (string, error) {
				switch mode {
				case "error":
					return "", errors.New("synthetic missing module")
				case "reload":
					reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}, Reports: []*report.Report{rep}})
				case "revoked":
					delete(user.Roles[0].Permissions.Reports, "RowSource")
				case "unsafe_code":
					return strings.Replace(enrichCode, "Возврат Результат;", "Выполнить(\"danger\"); Возврат Результат;", 1), nil
				case "cancel":
					cancel()
				}
				return enrichCode, nil
			})
			if err == nil || result != nil {
				t.Fatal("stale/revoked/unsafe registered source accepted")
			}
		})
	}
}
