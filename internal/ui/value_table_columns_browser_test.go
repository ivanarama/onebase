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
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Real GET -> native FormData -> obFire -> HTTP/DSL -> repaint. Readonly
// placements must remain display-only while readonly columns preserve values.
func TestValueTableColumnsBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	column := func(name string, ro bool) *metadata.FormElement {
		return &metadata.FormElement{Kind: metadata.FormElementColumn, Name: name, DataPath: name, ReadOnly: ro}
	}
	table := func(name string, ro bool) *metadata.FormElement {
		return &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: name, DataPath: "Форма.Results", ReadOnly: ro,
			Children: []*metadata.FormElement{column("Name", false), column("Locked", true), column("VisibleFlag", true)},
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnRowActivated: "SelectRow"}}
	}
	s, ent := setupManagedEventsServer(t, `
Процедура OpenForm()
 Results.Очистить();
 Для Н = 0 По 1 Цикл
  Стр = Results.Добавить();
  Стр.Name = "row" + Строка(Н);
  Стр.Locked = 7;
  Стр.Flag = Н = 0;
  Стр.VisibleFlag = Н = 0;
  Стр.ID = "id" + Строка(Н);
 КонецЦикла;
КонецПроцедуры
Процедура SelectRow()
 Сообщить(ТекущаяСтрока.Name);
 Сообщить(ТекущаяСтрока.Flag);
 Сообщить(ТекущаяСтрока.VisibleFlag);
КонецПроцедуры
Процедура ReplaceRows()
 Стр = Results.Получить(0);
 Стр.ID = "replacement";
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventOnOpen: "OpenForm"},
		[]*metadata.FormElement{table("ReadonlyResults", true), table("WritableResults", false),
			{Kind: metadata.FormElementButton, Name: "Replace", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ReplaceRows"}},
		})
	ent.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "Results", TypeRef: "ValueTable", Columns: []*metadata.FormAttributeColumn{
		{Name: "Name", TypeRef: "string"}, {Name: "Locked", TypeRef: "number"}, {Name: "VisibleFlag", TypeRef: "bool"}, {Name: "Flag", TypeRef: "bool"}, {Name: "ID", TypeRef: "string"},
	}}}
	router := chi.NewRouter()
	s.MountStatic(router)
	router.Get("/ui/{kind}/{entity}/new", s.form)
	router.Post("/ui/{kind}/{entity}/form-event", s.handleManagedFormEvent)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "vt-columns.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=vt-columns", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("ValueTable browser: %v", err)
	}
}
