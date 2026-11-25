package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Справочники.X.ПометитьНаУдаление(Ссылка) и СнятьПометку(Ссылка) — как у
// Документы.X: обработчик формы помечает другую запись справочника тем же
// путём, что пометка из UI. Роль без права удаления пометить не может.
func TestDSLCatalogMarkForDeletion(t *testing.T) {
	ent := &metadata.Entity{
		Name: "СкладПометка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	ent.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование"},
			{Kind: metadata.FormElementButton, Name: "Пометить",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ПометитьНажатие"}},
			{Kind: metadata.FormElementButton, Name: "Снять",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "СнятьНажатие"}},
		},
		ProgramAST: mustParse(t, `
Процедура ПометитьНажатие()
	Справочники.СкладПометка.ПометитьНаУдаление(Справочники.СкладПометка.НайтиПоНаименованию("Старый склад"));
КонецПроцедуры

Процедура СнятьНажатие()
	Справочники.СкладПометка.СнятьПометку(Справочники.СкладПометка.НайтиПоНаименованию("Старый склад"));
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	openID, oldID := uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{openID: "Основной склад", oldID: "Старый склад"} {
		if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
			t.Fatal(err)
		}
	}
	click := func(button string) formEventResponse {
		t.Helper()
		return decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
			"_id": {openID.String()}, "_element": {button}, "_event": {string(metadata.FormEventOnClick)},
			"Наименование": {"Основной склад"},
		}).Body.Bytes())
	}
	marked := func() bool {
		t.Helper()
		m, err := srv.store.IsMarkedForDeletion(ctx, ent.Name, oldID)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	if resp := click("Пометить"); !resp.OK || !marked() {
		t.Fatalf("ПометитьНаУдаление: ok=%v помечен=%v ошибка=%q", resp.OK, marked(), resp.Error)
	}
	if open, _ := srv.store.IsMarkedForDeletion(ctx, ent.Name, openID); open {
		t.Fatal("открытая запись не должна помечаться")
	}
	if resp := click("Снять"); !resp.OK || marked() {
		t.Fatalf("СнятьПометку: ok=%v помечен=%v ошибка=%q", resp.OK, marked(), resp.Error)
	}

	// Роль с чтением и записью справочника, но без удаления: отказ, пометки нет.
	user := &auth.User{ID: "u1", Login: "operator", Roles: []*auth.Role{{
		Name:        "Оператор",
		Permissions: auth.Permission{Catalogs: map[string][]string{ent.Name: {"read", "write"}}},
	}}}
	body := url.Values{
		"_id": {openID.String()}, "_element": {"Пометить"}, "_event": {string(metadata.FormEventOnClick)},
		"Наименование": {"Основной склад"},
	}
	req := httptest.NewRequest("POST", "/ui/catalog/"+ent.Name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "catalog")
	rctx.URLParams.Add("entity", ent.Name)
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), user))
	rec := httptest.NewRecorder()
	srv.handleManagedFormEvent(rec, req)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || marked() || !strings.Contains(resp.Error, "ПометитьНаУдаление") {
		t.Fatalf("без права удаления пометка должна отказать: ok=%v помечен=%v ошибка=%q", resp.OK, marked(), resp.Error)
	}
}
