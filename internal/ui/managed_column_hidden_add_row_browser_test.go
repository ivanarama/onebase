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

// Exercise the real Add row button with hidden leading columns, including
// the case where no column can accept focus or input.
func TestManagedColumnHiddenAddRowBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	dbtest.ForEachDialect(t, testManagedColumnHiddenAddRowBrowser)
}

func testManagedColumnHiddenAddRowBrowser(t *testing.T, db *storage.DB) {
	var entities []*metadata.Entity
	for _, name := range []string{"Видимая", "Скрытая", "ВсеСкрыты"} {
		ent := columnHiddenWhenEntity(t, false)
		ent.Name = name
		form := ent.Forms[0]
		form.EntityName = name
		columns := form.Elements[1].Children
		columns[1].HiddenWhen = ""
		if name == "Скрытая" {
			columns[0].HiddenWhen = "НЕ ПоКлассификатору"
		} else if name == "ВсеСкрыты" {
			columns[0].HiddenWhen = "Истина"
			columns[1].HiddenWhen = "Истина"
		}
		form.Elements = append(form.Elements, &metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Переключить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Переключить"},
		})
		form.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnOpen: "Открыть"}
		form.ProgramAST = mustParse(t, `
Процедура Открыть()
 Стр = Объект.Найдено.Добавить();
 Стр.Улица = "скрытое значение";
 Стр.Представление = "исходная строка";
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
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "column-hidden-add-row.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=column-hidden-add-row", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("column hidden add row browser: %v", err)
	}
}
