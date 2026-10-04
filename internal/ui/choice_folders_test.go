package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// choice_folders проверяется через публичный маршрут подбора: ключ обещает
// именно то, что оператор увидит в окне выбора, а состав выдачи решает сервер.
type choiceFoldersFixture struct {
	server *Server
	target *metadata.Entity
	owner  *metadata.Entity
	city   uuid.UUID
	street uuid.UUID
	user   *auth.User
}

func newChoiceFoldersFixture(t *testing.T) choiceFoldersFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "choice-folders.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	target := &metadata.Entity{
		Name: "АдресныйКлассификатор", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Заявка", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{
				ID: "city", Name: "ПолеНаселённыйПункт", Kind: metadata.FormElementField,
				DataPath: "Объект.НаселённыйПункт", Choice: true, ChoiceFolders: true,
			},
			{
				ID: "street", Name: "ПолеУлица", Kind: metadata.FormElementField,
				DataPath: "Объект.Улица", Choice: true,
			},
		},
	}
	owner := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "НаселённыйПункт", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name},
			{Name: "Улица", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{target, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	city := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	street := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	if err := db.Upsert(ctx, target.Name, city, map[string]any{
		"Наименование": "Город Н", "is_folder": true,
	}, target); err != nil {
		t.Fatalf("seed city: %v", err)
	}
	if err := db.Upsert(ctx, target.Name, street, map[string]any{
		"Наименование": "улица Ленина", "parent_id": city.String(),
	}, target); err != nil {
		t.Fatalf("seed street: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	server := &Server{reg: reg, store: db}
	server.entitySvc = server.newEntityService(nil)
	user := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{target.Name: {"read"}},
		Documents: map[string][]string{owner.Name: {"read", "write"}},
	}}}}
	return choiceFoldersFixture{server: server, target: target, owner: owner, city: city, street: street, user: user}
}

func (f choiceFoldersFixture) options(t *testing.T, element string, extra url.Values) choiceHTTPResponse {
	t.Helper()
	query := url.Values{
		"form_entity": {f.owner.Name},
		"form":        {"ФормаОбъекта"},
		"element":     {element},
		"limit":       {"100"},
	}
	for key, values := range extra {
		query[key] = values
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response choiceHTTPResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

// legacyOptions — тот же маршрут без контекста формы: так ходит поле, которое
// ничего про подбор не объявляло.
func (f choiceFoldersFixture) legacyOptions(t *testing.T) choiceHTTPResponse {
	t.Helper()
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?limit=100", nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response choiceHTTPResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func choiceLabels(rows []map[string]any) []string {
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		labels = append(labels, fmt.Sprint(row["_label"]))
	}
	return labels
}

func TestRefOptionsChoiceFoldersKeepsGroupsInPicker(t *testing.T) {
	f := newChoiceFoldersFixture(t)

	t.Run("поле с ключом видит группы", func(t *testing.T) {
		response := f.options(t, "city", nil)
		labels := choiceLabels(response.Items)
		if len(labels) != 2 {
			t.Fatalf("ожидались город и улица, получено %v", labels)
		}
		if response.Total != 2 {
			t.Fatalf("итог не совпал с выдачей: total=%d items=%v", response.Total, labels)
		}
	})

	t.Run("обычный подбор того же справочника групп не видит", func(t *testing.T) {
		// Поле без ключа контекста подбора не публикует и ходит прежним
		// маршрутом: состав выдачи там не изменился.
		response := f.legacyOptions(t)
		labels := choiceLabels(response.Items)
		if len(labels) != 1 || labels[0] != "улица Ленина" {
			t.Fatalf("группа попала в обычный подбор: %v", labels)
		}
	})

	t.Run("выбранная группа считается допустимой", func(t *testing.T) {
		response := f.options(t, "city", url.Values{"selected_id": {f.city.String()}})
		if response.SelectedAllowed == nil || !*response.SelectedAllowed {
			t.Fatalf("записанный город объявлен недопустимым: %s", mustJSON(t, response))
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
