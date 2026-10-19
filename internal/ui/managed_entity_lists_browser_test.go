package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestEntityListsBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	f := entityListFixture(t)
	// Validation accepts case-insensitive source paths; the rendered control
	// remains select[name="Направление"]. Exercise that binding in every scenario.
	f.owner.Forms[0].Elements[2].EntityList.Filter[0].Source = "объект.направление"
	f.owner.Forms[0].Elements[1].ChoiceFilter = nil
	// Each widget has its own transport spelling for the same canonical field.
	f.owner.Forms[0].Elements = append(f.owner.Forms[0].Elements, &metadata.FormElement{
		ID: "second-history", Name: "SecondHistory", Kind: metadata.FormElementEntityList,
		EntityList: &metadata.FormEntityListSpec{Entity: f.target.Name, Columns: []string{"Наименование"}, OrderBy: []string{"Наименование asc"}, Limit: 2,
			Filter: []metadata.FormEntityListCondition{{Field: "Направление", Op: "eq", Source: "Объект.Направление"}}},
	})
	for _, entry := range []struct{ id, name string }{{f.rootA.String(), "A"}, {f.rootB.String(), "B"}} {
		uid, err := uuid.Parse(entry.id)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.server.store.Upsert(context.Background(), f.direction.Name, uid, map[string]any{"Наименование": entry.name, "is_folder": false}, f.direction); err != nil {
			t.Fatal(err)
		}
	}
	// Load handler code through the same public managed loader as configuration.
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "test.form.yaml")
	if err := os.WriteFile(yamlPath, []byte("form: {name: ФормаОбъекта, kind: object, entity: Заявка}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code := fmt.Sprintf("Процедура ВеткаB()\n Объект.Направление = %q;\nКонецПроцедуры\nПроцедура ЗаписатьB()\n Объект.Направление = %q;\n Объект.Записать();\nКонецПроцедуры\n", f.rootB.String(), f.rootB.String())
	if err := os.WriteFile(filepath.Join(dir, "test.form.os"), []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.NewManagedFormLoader().LoadFormFile(yamlPath, f.owner.Name)
	if err != nil {
		t.Fatal(err)
	}
	f.owner.Forms[0].ProgramAST = loaded.ProgramAST
	f.owner.Forms[0].Elements = append(f.owner.Forms[0].Elements, &metadata.FormElement{ID: "branch-b", Name: "ВеткаB", Kind: metadata.FormElementButton, TitleMap: map[string]string{"ru": "ВеткаB"}, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ВеткаB"}})

	f.owner.TableParts = []metadata.TablePart{{Name: "Items", Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}}
	f.owner.Forms[0].Elements = append(f.owner.Forms[0].Elements,
		&metadata.FormElement{ID: "items", Name: "Items", Kind: metadata.FormElementTablePart, DataPath: "Объект.Items", NoGrid: true},
		&metadata.FormElement{ID: "write-b", Name: "ЗаписатьB", Kind: metadata.FormElementButton, TitleMap: map[string]string{"ru": "ЗаписатьB"}, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ЗаписатьB"}})
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.owner}); err != nil {
		t.Fatal(err)
	}
	if err := f.server.store.UpsertTablePartRows(context.Background(), f.owner.Name, "Items", f.ownerID, []map[string]any{{"Text": "keep me"}}, f.owner.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	f.server = New(f.server.reg, f.server.store, interpreter.New(), nil, Config{}, nil)
	router := chi.NewRouter()
	f.server.MountStatic(router)
	router.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), f.user)))
			})
		})
		f.server.Mount(r)
	})
	srv := httptest.NewServer(router)
	defer func() { f.server.CloseEventStreams(); srv.Close(); f.server.Close() }()
	ids, _ := json.Marshal(map[string]string{"a": f.rootA.String(), "b": f.rootB.String(), "owner": f.ownerID.String()})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "entity-lists.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=entity-lists", "OB_ACCEPTANCE_URL="+srv.URL, "OB_LIST_IDS="+string(ids))
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("entity lists browser: %v", err)
	}
}
