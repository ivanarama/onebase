package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// `interactive_create: false` — аналог права «Интерактивное добавление» 1С:
// документ, который рождается из другого (заявка из звонка), из интерфейса
// создаёт только роль с явным `create` (и администратор); одного `write` мало.
// Сущность грузится из YAML, запросы идут через HTTP-обработчики — тем же
// путём, что у пользователя.

func loadNoCreateDoc(t *testing.T) *metadata.Entity {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Заявка.yaml")
	if err := os.WriteFile(p, []byte(`name: Заявка
interactive_create: false
fields:
  - name: Описание
    type: string
`), 0o600); err != nil {
		t.Fatal(err)
	}
	ent, err := metadata.LoadFile(p, metadata.KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	if !ent.NoInteractiveCreate {
		t.Fatal("interactive_create: false не прочитан из YAML")
	}
	return ent
}

func noCreateUser(login string, admin bool, ops ...string) *auth.User {
	return &auth.User{Login: login, IsAdmin: admin, Roles: []*auth.Role{{Permissions: auth.Permission{
		Documents: map[string][]string{"Заявка": ops, "Звонок": {"read", "write"}},
	}}}}
}

var (
	operatorNoCreate = noCreateUser("operator", false, "read", "write")
	managerCreate    = noCreateUser("manager", false, "read", "write", "create")
	adminCreate      = noCreateUser("admin", true)
)

func noCreateRequest(ctx context.Context, user *auth.User, method, target string, params map[string]string, body url.Values) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return req.WithContext(auth.ContextWithUser(ctx, user))
}

func TestInteractiveCreateFalse_ListAndCardByRole(t *testing.T) {
	doc := loadNoCreateDoc(t)
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	params := map[string]string{"kind": "document", "entity": "заявка"}

	list := func(u *auth.User) string {
		t.Helper()
		rec := httptest.NewRecorder()
		s.list(rec, noCreateRequest(ctx, u, "GET", "/ui/document/заявка", params, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("список (%s): code = %d, body: %s", u.Login, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	if empty := list(operatorNoCreate); strings.Contains(empty, "data-ob-list-create") || strings.Contains(empty, "создать первую") {
		t.Error("пустой список предлагает оператору создание («+ Создать» / «создать первую»)")
	}

	id := uuid.New()
	if err := s.store.Upsert(ctx, doc.Name, id, map[string]any{"Описание": "из звонка"}, doc); err != nil {
		t.Fatal(err)
	}
	full := list(operatorNoCreate)
	if strings.Contains(full, "data-ob-list-create") {
		t.Error("у оператора без create осталась кнопка «+ Создать» (и клавиша Insert)")
	}
	if !strings.Contains(full, `data-ob-row-can-copy=""`) {
		t.Error("у оператора без create не выключено копирование строки")
	}
	for _, u := range []*auth.User{managerCreate, adminCreate} {
		page := list(u)
		if !strings.Contains(page, "data-ob-list-create") || !strings.Contains(page, `data-ob-row-can-copy="1"`) {
			t.Errorf("%s (право create) не видит «+ Создать» / копирования", u.Login)
		}
	}

	card := func(u *auth.User) string {
		t.Helper()
		rec := httptest.NewRecorder()
		s.formEdit(rec, noCreateRequest(ctx, u, "GET", "/ui/document/заявка/"+id.String(),
			map[string]string{"kind": "document", "entity": "заявка", "id": id.String()}, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("карточка (%s): code = %d, body: %s", u.Login, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if strings.Contains(card(operatorNoCreate), "/new?copy=") {
		t.Error("в карточке у оператора без create осталась кнопка «Скопировать»")
	}
	if !strings.Contains(card(managerCreate), "/new?copy=") {
		t.Error("в карточке у роли с create нет «Скопировать»")
	}
}

// Кнопки скрыты, но адрес создания можно набрать руками — сервер отказывает
// без права create, и POST ничего не записывает.
func TestInteractiveCreateFalse_NewURLByRole(t *testing.T) {
	doc := loadNoCreateDoc(t)
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	params := map[string]string{"kind": "document", "entity": "заявка"}

	get := httptest.NewRecorder()
	s.form(get, noCreateRequest(ctx, operatorNoCreate, "GET", "/ui/document/заявка/new", params, nil))
	if get.Code != http.StatusForbidden || !strings.Contains(get.Body.String(), "создаётся только из других объектов") {
		t.Fatalf("GET /new без create: code = %d, ожидался 403 с причиной; body: %s", get.Code, get.Body.String())
	}

	post := httptest.NewRecorder()
	s.submit(post, noCreateRequest(ctx, operatorNoCreate, "POST", "/ui/document/заявка/new", params, url.Values{"Описание": {"в обход"}}))
	if post.Code != http.StatusForbidden {
		t.Fatalf("POST /new без create: code = %d, ожидался 403", post.Code)
	}
	rows, err := s.store.List(ctx, doc.Name, doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("POST /new без create записал документ: %d строк", len(rows))
	}

	ref := httptest.NewRecorder()
	s.refCreateRedirect(ref, noCreateRequest(ctx, operatorNoCreate, "GET", "/ui/_ref-create/Заявка", map[string]string{"entity": doc.Name}, nil))
	if ref.Code != http.StatusForbidden {
		t.Fatalf("создание из поля ссылки без create: code = %d, ожидался 403", ref.Code)
	}

	for _, u := range []*auth.User{managerCreate, adminCreate} {
		rec := httptest.NewRecorder()
		s.form(rec, noCreateRequest(ctx, u, "GET", "/ui/document/заявка/new", params, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /new у %s (право create): code = %d", u.Login, rec.Code)
		}
	}
}

// Поле ссылки на такой документ не предлагает «+» без права create.
func TestInteractiveCreateFalse_InlineCreateFromRefFieldByRole(t *testing.T) {
	doc := loadNoCreateDoc(t)
	call := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Заявка", Type: "reference:Заявка", RefEntity: doc.Name}},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{doc, call})
	render := func(u *auth.User) string {
		t.Helper()
		req := reqWithChi(http.MethodGet, "/ui/document/Звонок/new", nil, map[string]string{"kind": "document", "entity": call.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		s.form(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("форма звонка (%s): code = %d, body: %s", u.Login, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `data-ref-entity="Заявка"`) {
			t.Fatal("поле ссылки на заявку не отрисовано")
		}
		return body
	}
	if strings.Contains(render(operatorNoCreate), `data-ref-allow-create="1"`) {
		t.Error("поле ссылки предлагает оператору без create создать заявку вручную")
	}
	if !strings.Contains(render(managerCreate), `data-ref-allow-create="1"`) {
		t.Error("роль с create не может создать заявку из поля ссылки")
	}
}

// Both managed transports are public UI entry points. A new form opened while
// create was allowed must stop working after that permission is revoked.
func TestInteractiveCreateFalse_ManagedTransports(t *testing.T) {
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		for _, transport := range []string{"form-event", "form-close-intent"} {
			for _, tc := range []struct {
				name                            string
				user                            *auth.User
				unrestricted, existing, discard bool
				wantStatus                      int
			}{
				{name: "revoked-create", user: operatorNoCreate, wantStatus: http.StatusForbidden},
				{name: "with-create", user: managerCreate, wantStatus: http.StatusOK},
				{name: "admin", user: adminCreate, wantStatus: http.StatusOK},
				{name: "without-flag", user: operatorNoCreate, unrestricted: true, wantStatus: http.StatusOK},
				{name: "existing-without-create", user: operatorNoCreate, existing: true, wantStatus: http.StatusOK},
				{name: "create-without-write", user: noCreateUser("creator", false, "read", "create"), wantStatus: http.StatusForbidden},
				{name: "discard-revoked-create", user: operatorNoCreate, discard: true, wantStatus: http.StatusOK},
			} {
				if tc.discard && transport != "form-close-intent" {
					continue
				}
				t.Run(string(kind)+"/"+transport+"/"+tc.name, func(t *testing.T) {
					ent := loadNoCreateDoc(t)
					ent.Kind = kind
					ent.NoInteractiveCreate = !tc.unrestricted
					form := managedObjectForm(fieldEl("Description", "Объект.Описание"), &metadata.FormElement{
						Kind: metadata.FormElementButton, Name: "Save",
						Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ЗаписатьФорму"},
					})
					form.ProgramAST = mustParse(t, `
Процедура ЗаписатьФорму()
 Объект.Записать();
 Сообщить("handler ran");
КонецПроцедуры
Процедура ПроверитьЗакрытие(Отказ)
 Объект.Записать();
 Сообщить("close handler ran");
КонецПроцедуры
`)
					if tc.discard {
						form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
					}
					ent.Forms = []*metadata.FormModule{form}
					srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
					// The same permission matrix applies to catalogs and documents.
					user := *tc.user
					user.Roles = []*auth.Role{{Permissions: auth.Permission{
						Documents: tc.user.Roles[0].Permissions.Documents,
						Catalogs:  tc.user.Roles[0].Permissions.Documents,
					}}}
					router := chi.NewRouter()
					srv.Mount(router)
					base := "/ui/" + string(kind) + "/" + ent.Name
					body := url.Values{"_kind": {"object"}, "Описание": {"changed"}}
					if tc.existing {
						id := uuid.New()
						if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Описание": "before"}, ent); err != nil {
							t.Fatal(err)
						}
						body.Set("_id", id.String())
					} else if tc.name == "revoked-create" {
						// Open with create, then submit that still-new form as a user without it.
						openUser := user
						openUser.Roles = []*auth.Role{{Permissions: auth.Permission{
							Catalogs:  managerCreate.Roles[0].Permissions.Documents,
							Documents: managerCreate.Roles[0].Permissions.Documents,
						}}}
						req := httptest.NewRequest(http.MethodGet, base+"/new", nil)
						req = req.WithContext(auth.ContextWithUser(ctx, &openUser))
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, req)
						if rec.Code != http.StatusOK {
							t.Fatalf("open form: %d %s", rec.Code, rec.Body.String())
						}
					}
					if transport == "form-event" {
						body.Set("_element", "Save")
						body.Set("_event", string(metadata.FormEventOnClick))
					} else {
						for key, values := range closeIntentBody(uuid.NewString(), "close", "") {
							body[key] = values
						}
						if tc.discard {
							body.Set("_close_mode", "discard")
						} else {
							body.Set("_close_mode", "save")
						}
						ensureEntityCloseIntentSchema(ent, body)
					}
					req := httptest.NewRequest(http.MethodPost, base+"/"+transport, strings.NewReader(body.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
					req = req.WithContext(auth.ContextWithUser(ctx, &user))
					if transport == "form-close-intent" {
						setEntityCloseIntentHeaders(req, body)
					}
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					response := decodeCloseIntentResponse(t, rec)
					if rec.Code != tc.wantStatus {
						t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
					}
					rows, err := srv.store.List(ctx, ent.Name, ent, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					denied := tc.wantStatus == http.StatusForbidden
					if denied || tc.discard {
						if len(rows) != 0 || response.SavedID != "" || len(response.Messages) != 0 {
							t.Fatalf("forbidden/abandoned new form ran handler or saved: rows=%v response=%+v", rows, response)
						}
					} else if !response.OK || len(rows) != 1 || rows[0]["Описание"] != "changed" || (!tc.existing && response.SavedID == "") {
						t.Fatalf("authorized save/update failed: rows=%v response=%+v", rows, response)
					}
					if transport == "form-close-intent" && !denied && (response.Close == nil || !response.Close.Allowed) {
						t.Fatalf("close was denied: %+v", response)
					}
				})
			}
		}
	}
}

// Programmatic creation from a permitted source form still requires write,
// without inheriting the target entity's interactive create restriction.
func TestInteractiveCreateFalse_ProgrammaticCreationFromForm(t *testing.T) {
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		t.Run(string(kind), func(t *testing.T) {
			target := loadNoCreateDoc(t)
			target.Kind = kind
			root := "Документы"
			if kind == metadata.KindCatalog {
				root = "Справочники"
			}
			form := managedObjectForm(&metadata.FormElement{
				Kind: metadata.FormElementButton, Name: "Create",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "СоздатьЗаявку"},
			})
			form.ProgramAST = mustParse(t, fmt.Sprintf(`
Процедура СоздатьЗаявку()
 Заявка = %s.Заявка.Создать();
 Заявка.Описание = "из звонка";
 Заявка.Записать();
КонецПроцедуры
`, root))
			source := &metadata.Entity{Name: "Звонок", Kind: metadata.KindDocument, Forms: []*metadata.FormModule{form}}
			srv, ctx := newSubmitTestServer(t, []*metadata.Entity{target, source})
			router := chi.NewRouter()
			srv.Mount(router)
			for _, ops := range [][]string{{"read", "write"}, {"read"}} {
				user := noCreateUser("operator", false, ops...)
				user.Roles[0].Permissions.Catalogs = map[string][]string{target.Name: ops}
				body := url.Values{"_element": {"Create"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}
				req := httptest.NewRequest(http.MethodPost, "/ui/document/Звонок/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req = req.WithContext(auth.ContextWithUser(ctx, user))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				response := decodeCloseIntentResponse(t, rec)
				if (len(ops) == 2) != response.OK {
					t.Fatalf("ops=%v response=%+v", ops, response)
				}
				rows, err := srv.store.List(ctx, target.Name, target, storage.ListParams{})
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 1 || rows[0]["Описание"] != "из звонка" {
					t.Fatalf("programmatic write: rows=%v response=%+v", rows, response)
				}
			}
		})
	}
}

func TestInteractiveCreateFalse_UnsavedCloseReplayAfterRevocation(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
 Сообщить("cached handler message");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	ent.NoInteractiveCreate = true
	user := &auth.User{ID: "create-replay", Login: "create-replay", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{ent.Name: {"read", "write", "create"}},
	}}}}
	body := closeIntentBody(uuid.NewString(), "close", "unsaved")
	first := decodeCloseIntentResponse(t, executeFormCloseIntentAsUser(t, srv, ent, body, user))
	if first.Close == nil || !first.Close.Allowed || len(first.Messages) != 1 || first.SavedID != "" {
		t.Fatalf("authorized unsaved close did not cache handler response: %+v", first)
	}
	user.Roles[0].Permissions.Catalogs[ent.Name] = []string{"read", "write"}
	rec := executeFormCloseIntentAsUser(t, srv, ent, body, user)
	replay := decodeCloseIntentResponse(t, rec)
	if rec.Code != http.StatusOK || replay.Close == nil || !replay.Close.Allowed || !replay.Close.Terminal || len(replay.Values) != 0 || len(replay.Messages) != 0 {
		t.Fatalf("revoked create replay exposed stale handler state: status=%d response=%+v", rec.Code, replay)
	}
	rows, err := srv.store.List(context.Background(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("unsaved replay wrote rows: %v", rows)
	}
}

// Lazy children must expose the same copy action as the initially rendered list.
func TestInteractiveCreateFalse_LazyTreeCopyByRole(t *testing.T) {
	for _, tc := range []struct {
		name         string
		user         *auth.User
		unrestricted bool
		wantCopy     bool
	}{
		{name: "without-create", user: operatorNoCreate},
		{name: "with-create", user: managerCreate, wantCopy: true},
		{name: "admin", user: adminCreate, wantCopy: true},
		{name: "without-flag", user: operatorNoCreate, unrestricted: true, wantCopy: true},
		{name: "read-only", user: noCreateUser("reader", false, "read")},
		{name: "create-without-write", user: noCreateUser("creator", false, "read", "create")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ent := loadNoCreateDoc(t)
			ent.Kind = metadata.KindCatalog
			ent.Hierarchical = true
			ent.NoInteractiveCreate = !tc.unrestricted
			srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			parentID, childID := uuid.New(), uuid.New()
			if err := srv.store.Upsert(ctx, ent.Name, parentID, map[string]any{
				"Описание": "Folder", "is_folder": true,
			}, ent); err != nil {
				t.Fatal(err)
			}
			if err := srv.store.Upsert(ctx, ent.Name, childID, map[string]any{
				"Описание": "Child", "parent_id": parentID,
			}, ent); err != nil {
				t.Fatal(err)
			}
			user := *tc.user
			user.Roles = []*auth.Role{{Permissions: auth.Permission{
				Catalogs: tc.user.Roles[0].Permissions.Documents,
			}}}
			router := chi.NewRouter()
			srv.Mount(router)
			target := "/ui/_tree-children/" + url.PathEscape(ent.Name) + "?" + url.Values{
				"parent": {parentID.String()}, "depth": {"0"}, "subsystem": {"Sales"},
			}.Encode()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req = req.WithContext(auth.ContextWithUser(ctx, &user))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("tree children: code=%d body=%s", rec.Code, rec.Body.String())
			}
			var got treeChildrenResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Rows) != 1 || got.Rows[0].ID != childID.String() {
				t.Fatalf("expected the folder's child, got %+v", got.Rows)
			}
			wantURL := ""
			if tc.wantCopy {
				wantURL = "/ui/catalog/" + strings.ToLower(ent.Name) + "/new?copy=" + childID.String() + "&subsystem=Sales"
			}
			if got.Rows[0].CopyURL != wantURL {
				t.Errorf("copy_url = %q, want %q", got.Rows[0].CopyURL, wantURL)
			}
		})
	}
}
