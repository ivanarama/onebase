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

// Search results are consumed by the first activation. Exercise the queued
// click through native FormData, the actual event endpoint, DSL and repaint.
func TestValueTableStaleActivationBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		_, entity := setupManagedEventsServer(t, `
Процедура OpenForm()
 Results.Очистить();
 Для Н = 0 По 1 Цикл
  Стр = Results.Добавить();
  Стр.Name = "synthetic" + Строка(Н);
  Стр.ID = "id" + Строка(Н);
 КонецЦикла;
КонецПроцедуры
Процедура SelectRow()
 Сообщить(ТекущаяСтрока.Name);
 Results.Очистить();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventOnOpen: "OpenForm"}, []*metadata.FormElement{
			{Kind: metadata.FormElementTablePart, Name: "ResultsTable", DataPath: "Форма.Results",
				Children: []*metadata.FormElement{{Kind: metadata.FormElementColumn, Name: "Name", DataPath: "Name", ReadOnly: true}},
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnRowActivated: "SelectRow"}},
			{Kind: metadata.FormElementButton, Name: "Reload", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "OpenForm"}},
		})
		entity.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "Results", TypeRef: "ValueTable", Columns: []*metadata.FormAttributeColumn{
			{Name: "Name", TypeRef: "string"}, {Name: "ID", TypeRef: "string"},
		}}}
		s := newSubmitTestServerOn(t, db, []*metadata.Entity{entity})
		router := chi.NewRouter()
		s.MountStatic(router)
		router.Get("/ui/{kind}/{entity}/new", s.form)
		router.Post("/ui/{kind}/{entity}/form-event", s.handleManagedFormEvent)
		server := httptest.NewServer(router)
		defer server.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "vt-stale-activation.spec.js")
		cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
		cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=vt-stale-activation", "OB_ACCEPTANCE_URL="+server.URL)
		output, err := cmd.CombinedOutput()
		t.Logf("%s", output)
		if err != nil {
			t.Fatalf("ValueTable queued activation browser: %v", err)
		}
	})
}
