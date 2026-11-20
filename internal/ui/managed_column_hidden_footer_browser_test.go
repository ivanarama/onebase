package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Exercise the production GET, SlickGrid footer and HTTP/DSL visibility event.
// A hidden numeric or text column must not steal the next visible column's sum.
func TestManagedColumnHiddenFooterBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	dbtest.ForEachDialect(t, testManagedColumnHiddenFooterBrowser)
}

func testManagedColumnHiddenFooterBrowser(t *testing.T, db *storage.DB) {
	var entities []*metadata.Entity
	for _, numeric := range []bool{false, true} {
		ent := columnHiddenWhenEntity(t, false)
		ent.Name = "Текст"
		if numeric {
			ent.Name = "Число"
			ent.TableParts[0].Fields[1].Type = metadata.FieldTypeNumber
		}
		ent.TableParts[0].Fields = append(ent.TableParts[0].Fields,
			metadata.Field{Name: "Сумма", Type: metadata.FieldTypeNumber})
		form := ent.Forms[0]
		form.EntityName = ent.Name
		form.Elements[1].Children = append(form.Elements[1].Children,
			&metadata.FormElement{Kind: metadata.FormElementColumn, Name: "КолСумма", DataPath: "Сумма"})
		form.Elements = append(form.Elements, &metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Переключить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Переключить"},
		})
		form.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnOpen: "Открыть"}
		form.ProgramAST = mustParse(t, `
Процедура Открыть()
 Объект.ПоКлассификатору = Истина;
 Стр = Объект.Найдено.Добавить();
 Стр.Улица = "улица";
 Стр.Представление = 7;
 Стр.Сумма = 11;
КонецПроцедуры
Процедура Переключить()
 Объект.ПоКлассификатору = НЕ Объект.ПоКлассификатору;
КонецПроцедуры
`)
		entities = append(entities, ent)
	}
	s := newSubmitTestServerOn(t, db, entities)
	router := chi.NewRouter()
	s.MountStatic(router)
	router.Get("/ui/{kind}/{entity}/new", s.form)
	router.Post("/ui/{kind}/{entity}/form-event", s.handleManagedFormEvent)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "column-hidden-footer.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=column-hidden-footer", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("column hidden footer browser: %v", err)
	}
}
