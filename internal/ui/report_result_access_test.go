package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Real HTTP report execution must classify native denial identically inside
// result_proc and direct SQL. User text, syntax errors and SQL failures are not ACL.
func TestReportResultQueryAccessStatusMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		source := &metadata.Entity{Name: "ResultAccessSource", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		missing := &metadata.Entity{Name: "ResultAccessMissing", Kind: metadata.KindCatalog, Fields: source.Fields}
		if err := db.Migrate(ctx, []*metadata.Entity{source}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, source.Name, uuid.New(), map[string]any{"Name": "SECRET_SOURCE_MARKER"}, source); err != nil {
			t.Fatal(err)
		}
		text := `ВЫБРАТЬ Name ИЗ Справочник.ResultAccessSource`
		body := func(queryText string, rethrow bool) string {
			call := `Строки=З.Выполнить();`
			if rethrow {
				call = `Строки=Неопределено; Попытка Строки=З.Выполнить(); Исключение ВызватьИсключение; КонецПопытки;`
			}
			return fmt.Sprintf(`Функция Build()
З=Новый Запрос; З.Текст="%s"; %s
Д=Новый ТабличныйДокумент; Д.Ячейка(1,1).Значение=Строки.Количество(); Возврат Д;
КонецФункции`, queryText, call)
		}
		reports := []*reportpkg.Report{
			{Name: "AccessDirect", Query: text},
			{Name: "AccessWithQuery", Query: `ВЫБРАТЬ 1 КАК Marker`, ResultProc: "Build"},
			{Name: "AccessQueryless", ResultProc: "Build"},
			{Name: "AccessRethrow", ResultProc: "Build"},
			{Name: "AccessFakeText", ResultProc: "Build"},
			{Name: "AccessBadQuery", ResultProc: "Build"},
			{Name: "AccessSQLFailure", ResultProc: "Build"},
		}
		programs := map[string]*ast.Program{
			"AccessWithQuery": mustParse(t, body(text, false)), "AccessQueryless": mustParse(t, body(text, false)), "AccessRethrow": mustParse(t, body(text, true)),
			"AccessFakeText": mustParse(t, `Функция Build() ВызватьИсключение "нет доступа к объекту: ResultAccessSource"; КонецФункции`),
			// PostgreSQL accepts a zero-column SELECT; an unmatched parenthesis is invalid on both dialects.
			"AccessBadQuery":   mustParse(t, body(`ВЫБРАТЬ (`, false)),
			"AccessSQLFailure": mustParse(t, body(`ВЫБРАТЬ Name ИЗ Справочник.ResultAccessMissing`, false)),
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{source, missing}, Reports: reports, Programs: programs})
		interp := interpreter.New()
		interp.LookupProc = reg.GetModuleProc
		s := &Server{reg: reg, store: db, interp: interp, messages: NewMessageStore(), lockMgr: runtime.NewLockManager(), ops: newOperationLimiter()}
		s.entitySvc = s.newEntityService(nil)
		t.Cleanup(s.Close)
		runs := map[string][]string{}
		for _, r := range reports {
			runs[r.Name] = []string{"run"}
		}
		denied := &auth.User{ID: uuid.NewString(), Login: "denied", Roles: []*auth.Role{{Name: "NoSource", Permissions: auth.Permission{Reports: runs}}}}
		allowed := &auth.User{ID: uuid.NewString(), Login: "allowed", Roles: []*auth.Role{{Name: "SourceRead", Permissions: auth.Permission{Reports: runs, Catalogs: map[string][]string{source.Name: {"read"}, missing.Name: {"read"}}}}}}
		for _, name := range []string{"AccessDirect", "AccessWithQuery", "AccessQueryless", "AccessRethrow"} {
			t.Run("denied/"+name, func(t *testing.T) {
				w := tabularHTTP(s, denied, "session", "POST", "/ui/report/"+name, nil)
				if w.Code != http.StatusForbidden || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "SECRET_SOURCE_MARKER") {
					t.Fatalf("denial must be403 without result: HTTP%d", w.Code)
				}
			})
		}
		for _, name := range []string{"AccessDirect", "AccessWithQuery", "AccessQueryless"} {
			t.Run("allowed/"+name, func(t *testing.T) {
				w := tabularHTTP(s, allowed, "session", "POST", "/ui/report/"+name, nil)
				if name == "AccessDirect" {
					if w.Code != 200 || !strings.Contains(w.Body.String(), "SECRET_SOURCE_MARKER") {
						t.Fatal("direct positive control failed")
					}
					return
				}
				if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/ui/tabular/") {
					t.Fatalf("native positive control HTTP%d: %s", w.Code, w.Body.String())
				}
				if view := tabularHTTP(s, allowed, "session", "GET", w.Header().Get("Location"), nil); view.Code != 200 {
					t.Fatalf("native viewer HTTP%d", view.Code)
				}
			})
		}
		for _, name := range []string{"AccessFakeText", "AccessBadQuery", "AccessSQLFailure"} {
			t.Run("genuine-error/"+name, func(t *testing.T) {
				w := tabularHTTP(s, allowed, "session", "POST", "/ui/report/"+name, nil)
				if w.Code != 500 || w.Header().Get("Location") != "" {
					t.Fatalf("ordinary error misclassified HTTP%d", w.Code)
				}
			})
		}
	})
}
