package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

type choiceHTTPFixture struct {
	server         *Server
	direction      *metadata.Entity
	target         *metadata.Entity
	owner          *metadata.Entity
	rootA          uuid.UUID
	rootB          uuid.UUID
	pageTwo        uuid.UUID
	hidden         uuid.UUID
	legacySelected uuid.UUID
	foreignOther   uuid.UUID
	ownerID        uuid.UUID
	user           *auth.User
}

func choiceHTTPUUID(prefix byte, number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("%02x000000-0000-0000-0000-%012d", prefix, number))
}

func newChoiceHTTPFixture(t *testing.T) choiceHTTPFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "choice-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	target := &metadata.Entity{
		Name: "Неисправность", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
		},
	}
	choiceElement := &metadata.FormElement{
		ID: "fault-picker", Name: "ПолеНеисправность", Kind: metadata.FormElementField,
		DataPath: "Объект.Неисправность",
		ChoiceFilter: []metadata.FormChoiceCondition{{
			Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление",
		}},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Заявка", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			choiceElement,
		},
	}
	owner := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "Неисправность", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{direction, target, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	rootA := choiceHTTPUUID(0x10, 1)
	childA := choiceHTTPUUID(0x10, 2)
	rootB := choiceHTTPUUID(0x10, 3)
	for _, row := range []struct {
		id     uuid.UUID
		name   string
		parent *uuid.UUID
	}{
		{rootA, "A", nil},
		{childA, "A.1", &rootA},
		{rootB, "B", nil},
	} {
		fields := map[string]any{"Наименование": row.name, "is_folder": true}
		if row.parent != nil {
			fields["parent_id"] = row.parent.String()
		}
		if err := db.Upsert(ctx, direction.Name, row.id, fields, direction); err != nil {
			t.Fatalf("seed direction: %v", err)
		}
	}

	for i := 1; i <= 51; i++ {
		name := fmt.Sprintf("element %03d", i)
		if i <= 2 {
			name = fmt.Sprintf("needle %03d", i)
		}
		branch := rootA
		if i%2 == 0 {
			branch = childA
		}
		if err := db.Upsert(ctx, target.Name, choiceHTTPUUID(0x20, i), map[string]any{
			"Наименование": name, "Направление": branch.String(), "Аудитория": "anna",
		}, target); err != nil {
			t.Fatalf("seed target %d: %v", i, err)
		}
	}
	hidden := choiceHTTPUUID(0x20, 52)
	legacySelected := choiceHTTPUUID(0x20, 53)
	foreignOther := choiceHTTPUUID(0x20, 54)
	for _, row := range []struct {
		id        uuid.UUID
		name      string
		direction uuid.UUID
		audience  string
	}{
		{hidden, "needle hidden", rootA, "bob"},
		{legacySelected, "needle legacy B", rootB, "anna"},
		{foreignOther, "needle other B", rootB, "anna"},
	} {
		if err := db.Upsert(ctx, target.Name, row.id, map[string]any{
			"Наименование": row.name, "Направление": row.direction.String(), "Аудитория": row.audience,
		}, target); err != nil {
			t.Fatalf("seed target extra: %v", err)
		}
	}
	ownerID := choiceHTTPUUID(0x30, 1)
	if err := db.Upsert(ctx, owner.Name, ownerID, map[string]any{
		"Направление": rootA.String(), "Неисправность": legacySelected.String(),
	}, owner); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	user := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{direction.Name: {"read"}, target.Name: {"read"}},
		Documents: map[string][]string{owner.Name: {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			target.Name: {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}}}}
	server := &Server{reg: reg, store: db}
	server.entitySvc = server.newEntityService(nil)
	return choiceHTTPFixture{
		server: server, direction: direction, target: target, owner: owner,
		rootA: rootA, rootB: rootB, pageTwo: choiceHTTPUUID(0x20, 51), hidden: hidden,
		legacySelected: legacySelected, foreignOther: foreignOther, ownerID: ownerID, user: user,
	}
}

func (f choiceHTTPFixture) contextQuery(source uuid.UUID) url.Values {
	sources, _ := json.Marshal(map[string]string{"Объект.Направление": source.String()})
	return url.Values{
		"form_entity": {f.owner.Name},
		"form":        {"ФормаОбъекта"},
		"element":     {"fault-picker"},
		"sources":     {string(sources)},
	}
}

func (f choiceHTTPFixture) serveRefOptions(t *testing.T, target *metadata.Entity, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

type choiceHTTPResponse struct {
	Items           []map[string]any `json:"items"`
	Total           int              `json:"total"`
	SelectedAllowed *bool            `json:"selected_allowed"`
}

func decodeChoiceHTTP(t *testing.T, recorder *httptest.ResponseRecorder) choiceHTTPResponse {
	t.Helper()
	var response choiceHTTPResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %d %q: %v", recorder.Code, recorder.Body.String(), err)
	}
	return response
}

func TestRefOptionsChoiceFilterCombinesSearchRLSAndTotal(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	query := f.contextQuery(f.rootA)
	query.Set("q", "needle")
	query.Set("limit", "100")
	recorder := f.serveRefOptions(t, f.target, query)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeChoiceHTTP(t, recorder)
	if response.Total != 2 || len(response.Items) != 2 {
		t.Fatalf("filtered total/items=%d/%d, want 2/2: %#v", response.Total, len(response.Items), response.Items)
	}
	for _, item := range response.Items {
		label := fmt.Sprint(item["_label"])
		if !strings.Contains(label, "needle") || label == "needle hidden" || strings.Contains(label, " B") {
			t.Fatalf("search/choice/RLS are not ANDed: %#v", response.Items)
		}
	}
}

func TestRefOptionsChoiceFilterTrustBoundaryAndCompatibility(t *testing.T) {
	f := newChoiceHTTPFixture(t)

	t.Run("missing known source is an empty page", func(t *testing.T) {
		query := f.contextQuery(f.rootA)
		query.Set("sources", `{}`)
		recorder := f.serveRefOptions(t, f.target, query)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		response := decodeChoiceHTTP(t, recorder)
		if response.Total != 0 || len(response.Items) != 0 {
			t.Fatalf("missing source returned rows: %#v", response)
		}
	})

	for name, mutate := range map[string]func(url.Values, *choiceHTTPFixture){
		"extra source": func(query url.Values, _ *choiceHTTPFixture) {
			query.Set("sources", `{"Объект.Направление":"`+f.rootA.String()+`","Объект.Подмена":"`+f.rootA.String()+`"}`)
		},
		"unknown form":    func(query url.Values, _ *choiceHTTPFixture) { query.Set("form", "Подмена") },
		"unknown element": func(query url.Values, _ *choiceHTTPFixture) { query.Set("element", "Подмена") },
		"invalid source value": func(query url.Values, _ *choiceHTTPFixture) {
			query.Set("sources", `{"Объект.Направление":"not-a-uuid"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			query := f.contextQuery(f.rootA)
			mutate(query, &f)
			if recorder := f.serveRefOptions(t, f.target, query); recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	t.Run("route entity must match metadata target", func(t *testing.T) {
		if recorder := f.serveRefOptions(t, f.direction, f.contextQuery(f.rootA)); recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400; body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("legacy request keeps old response", func(t *testing.T) {
		query := url.Values{"q": {"needle"}, "limit": {"100"}, "selected_id": {f.pageTwo.String()}}
		recorder := f.serveRefOptions(t, f.target, query)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		response := decodeChoiceHTTP(t, recorder)
		if response.Total != 4 || len(response.Items) != 4 {
			t.Fatalf("legacy response unexpectedly filtered by form: %#v", response)
		}
		if response.SelectedAllowed != nil {
			t.Fatalf("legacy response gained selected_allowed: %#v", response)
		}
	})
}

func TestRefOptionsSelectedAllowedIgnoresPageAndSearchButKeepsChoiceAndRLS(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	query := f.contextQuery(f.rootA)
	query.Set("limit", "50")
	query.Set("selected_id", f.pageTwo.String())
	recorder := f.serveRefOptions(t, f.target, query)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeChoiceHTTP(t, recorder)
	if response.SelectedAllowed == nil || !*response.SelectedAllowed {
		t.Fatalf("second-page selected id was rejected: %#v", response)
	}
	for _, item := range response.Items {
		if fmt.Sprint(item["id"]) == f.pageTwo.String() {
			t.Fatalf("test selected id is not on the second page: %#v", response.Items)
		}
	}

	query.Set("q", "no-such-label")
	if got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, query)); got.SelectedAllowed == nil || !*got.SelectedAllowed || len(got.Items) != 0 {
		t.Fatalf("search incorrectly affected selected_allowed: %#v", got)
	}

	query = f.contextQuery(f.rootB)
	query.Set("selected_id", f.pageTwo.String())
	if got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, query)); got.SelectedAllowed == nil || *got.SelectedAllowed {
		t.Fatalf("foreign hierarchy selection was allowed: %#v", got)
	}

	query = f.contextQuery(f.rootA)
	query.Set("selected_id", f.hidden.String())
	if got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, query)); got.SelectedAllowed == nil || *got.SelectedAllowed {
		t.Fatalf("RLS-hidden selection was allowed: %#v", got)
	}
}

func TestRefOptionsOwnerAndChoiceFilterAreANDed(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	f.target.Owner = "Организация"
	f.target.Fields = append(f.target.Fields, metadata.Field{
		Name: metadata.StandardOwnerField, ID: metadata.StandardOwnerFieldID,
		Type: metadata.FieldType("reference:Организация"), RefEntity: "Организация",
	})
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.target}); err != nil {
		t.Fatalf("migrate owner field: %v", err)
	}

	ownerA, ownerB := uuid.New(), uuid.New()
	allowed, wrongOwner, wrongChoice := uuid.New(), uuid.New(), uuid.New()
	for _, row := range []struct {
		id        uuid.UUID
		direction uuid.UUID
		owner     uuid.UUID
		name      string
	}{
		{allowed, f.rootA, ownerA, "intersection allowed"},
		{wrongOwner, f.rootA, ownerB, "intersection wrong owner"},
		{wrongChoice, f.rootB, ownerA, "intersection wrong choice"},
	} {
		if err := f.server.store.Upsert(context.Background(), f.target.Name, row.id, map[string]any{
			"Наименование":              row.name,
			"Направление":               row.direction.String(),
			"Аудитория":                 "anna",
			metadata.StandardOwnerField: row.owner.String(),
		}, f.target); err != nil {
			t.Fatalf("seed combined row %q: %v", row.name, err)
		}
	}

	query := f.contextQuery(f.rootA)
	filter, _ := json.Marshal(map[string]string{metadata.StandardOwnerField: ownerA.String()})
	query.Set("flt", string(filter))
	query.Set("q", "intersection")
	query.Set("limit", "100")
	response := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, query))
	if response.Total != 1 || len(response.Items) != 1 || fmt.Sprint(response.Items[0]["id"]) != allowed.String() {
		t.Fatalf("owner and choice_filter are not ANDed: %#v", response)
	}

	for name, selected := range map[string]uuid.UUID{
		"matching row": allowed, "wrong owner": wrongOwner, "wrong choice": wrongChoice,
	} {
		t.Run(name, func(t *testing.T) {
			selectedQuery := f.contextQuery(f.rootA)
			selectedQuery.Set("flt", string(filter))
			selectedQuery.Set("selected_id", selected.String())
			got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, selectedQuery))
			want := selected == allowed
			if got.SelectedAllowed == nil || *got.SelectedAllowed != want {
				t.Fatalf("selected_allowed=%v, want %v", got.SelectedAllowed, want)
			}
		})
	}
}

func TestManagedInitialOptionsComposeOwnerAndChoiceFilter(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	f.target.Owner = "Организация"
	f.target.Fields = append(f.target.Fields, metadata.Field{
		Name: metadata.StandardOwnerField, ID: metadata.StandardOwnerFieldID,
		Type: metadata.FieldType("reference:Организация"), RefEntity: "Организация",
	})
	f.owner.Fields = append(f.owner.Fields, metadata.Field{
		Name: "Организация", Type: metadata.FieldType("reference:Организация"), RefEntity: "Организация",
	})
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.target, f.owner}); err != nil {
		t.Fatalf("migrate owner fields: %v", err)
	}
	ownerA, ownerB := uuid.New(), uuid.New()
	allowed := uuid.New()
	for _, row := range []struct {
		id        uuid.UUID
		direction uuid.UUID
		owner     uuid.UUID
		name      string
	}{
		{allowed, f.rootA, ownerA, "initial intersection allowed"},
		{uuid.New(), f.rootA, ownerB, "initial wrong owner"},
		{uuid.New(), f.rootB, ownerA, "initial wrong choice"},
	} {
		if err := f.server.store.Upsert(context.Background(), f.target.Name, row.id, map[string]any{
			"Наименование":              row.name,
			"Направление":               row.direction.String(),
			"Аудитория":                 "anna",
			metadata.StandardOwnerField: row.owner.String(),
		}, f.target); err != nil {
			t.Fatalf("seed initial row %q: %v", row.name, err)
		}
	}

	data := map[string]any{"Values": map[string]string{
		"Направление": f.rootA.String(), "Организация": ownerA.String(), "Неисправность": "",
	}}
	f.server.applyManagedChoiceFilters(context.Background(), f.owner, f.owner.Forms[0], data)
	options := data["ManagedChoiceOptions"].(map[string][]map[string]any)["fault-picker"]
	if len(options) != 1 || fmt.Sprint(options[0]["id"]) != allowed.String() {
		t.Fatalf("initial owner and choice_filter are not ANDed: %#v", options)
	}
}

func htmlAttribute(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}

func findSelectByName(node *html.Node, name string) *html.Node {
	if node.Type == html.ElementNode && node.Data == "select" {
		if value, _ := htmlAttribute(node, "name"); value == name {
			return node
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findSelectByName(child, name); found != nil {
			return found
		}
	}
	return nil
}

func TestManagedChoiceFilterInitialRenderKeepsOnlyMarkedLegacyValue(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet,
		"/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("form status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	document, err := html.Parse(strings.NewReader(recorder.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	selectNode := findSelectByName(document, f.target.Name)
	if selectNode == nil {
		t.Fatalf("managed reference select %q not found", f.target.Name)
	}
	rawContext, ok := htmlAttribute(selectNode, "data-ref-choice-context")
	if !ok {
		t.Fatal("choice_filter context is absent from managed select")
	}
	var pickerContext managedChoiceContext
	if err := json.Unmarshal([]byte(rawContext), &pickerContext); err != nil {
		t.Fatalf("picker context %q: %v", rawContext, err)
	}
	if pickerContext.FormEntity != f.owner.Name || pickerContext.Form != "ФормаОбъекта" || pickerContext.Element != "fault-picker" || pickerContext.Sources["Объект.Направление"] != "Направление" {
		t.Fatalf("picker context = %#v", pickerContext)
	}

	legacyCount := 0
	foreignOther := false
	for option := selectNode.FirstChild; option != nil; option = option.NextSibling {
		if option.Type != html.ElementNode || option.Data != "option" {
			continue
		}
		value, _ := htmlAttribute(option, "value")
		switch value {
		case f.legacySelected.String():
			legacyCount++
			if marker, _ := htmlAttribute(option, "data-ob-choice-outside-filter"); marker != "1" {
				t.Fatalf("legacy option is not marked: %#v", option.Attr)
			}
			if _, selected := htmlAttribute(option, "selected"); !selected {
				t.Fatal("legacy option stopped being selected on initial render")
			}
		case f.foreignOther.String():
			foreignOther = true
		}
	}
	if legacyCount != 1 || foreignOther {
		t.Fatalf("legacy/foreign options: selected count=%d foreign other=%v", legacyCount, foreignOther)
	}
}

func TestManagedChoiceFilterLegacyValueSurvivesOrdinarySave(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	body := url.Values{
		"Направление":   {f.rootA.String()},
		"Неисправность": {f.legacySelected.String()},
		"_action":       {""},
	}
	request := reqWithChi(http.MethodPost,
		"/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), body,
		map[string]string{"entity": f.owner.Name, "id": f.ownerID.String()})
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
	recorder := httptest.NewRecorder()
	f.server.submitEdit(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("ordinary save status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	stored, err := f.server.store.GetByID(context.Background(), f.owner.Name, f.ownerID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(stored["Неисправность"]); got != f.legacySelected.String() {
		t.Fatalf("ordinary save erased legacy outside-filter value: got %q, want %s", got, f.legacySelected)
	}
}

func TestRefPickerChoiceFilterClientContract(t *testing.T) {
	source := string(uiJS)
	for _, required := range []string{
		"function obRefChoiceQuery(sel)",
		"data-ref-choice-context",
		"data-ob-choice-outside-filter",
		"&selected_id=",
		"&sources=",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("ui.js does not contain %q", required)
		}
	}
}
