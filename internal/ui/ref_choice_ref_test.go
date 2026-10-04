package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Постоянная ссылка ref в choice_filter (#1820) через публичный маршрут
// подбора и первую отрисовку формы. Значение — из метаданных формы, браузер его
// не присылает. Записи ссылки нет или она не видна — подбор пуст при любом
// операторе (fail-closed).
//
// Причины (иерархический): Рабочие (папка) → Ремонт (папка) → замена;
// Рабочие → консультация, закрытая (RLS: только bob); Архив (папка) → старая;
// Секретная (папка, RLS: только bob) → тайна; корневая (без папки).
// У причин есть ссылка Филиал: замена, консультация — Центр; старая — Север;
// корневая — без филиала.
type refChoiceFixture struct {
	server                                 *Server
	target, owner, branches                *metadata.Entity
	working, repair, archive, secret       uuid.UUID
	replace, consult, closed, old, mystery uuid.UUID
	root                                   uuid.UUID
	center, north                          uuid.UUID
	ownerID                                uuid.UUID
	anna, noBranches                       *auth.User
}

func refChoiceElement(id, field string, conditions ...metadata.FormChoiceCondition) *metadata.FormElement {
	return &metadata.FormElement{ID: id, Name: "Поле" + field, Kind: metadata.FormElementField, DataPath: "Объект." + field, ChoiceFilter: conditions}
}

func newRefChoiceFixture(t *testing.T, db *storage.DB) refChoiceFixture {
	t.Helper()
	ctx := context.Background()
	f := refChoiceFixture{
		working: choiceHTTPUUID(0x70, 1), repair: choiceHTTPUUID(0x70, 2), archive: choiceHTTPUUID(0x70, 3),
		secret: choiceHTTPUUID(0x70, 4), replace: choiceHTTPUUID(0x70, 11), consult: choiceHTTPUUID(0x70, 12),
		closed: choiceHTTPUUID(0x70, 13), old: choiceHTTPUUID(0x70, 14), mystery: choiceHTTPUUID(0x70, 15),
		root:   choiceHTTPUUID(0x70, 16),
		center: choiceHTTPUUID(0x71, 1), north: choiceHTTPUUID(0x71, 2),
		ownerID: choiceHTTPUUID(0x72, 1),
	}
	missing := choiceHTTPUUID(0x7f, 1) // такой записи в базе нет

	branches := &metadata.Entity{
		Name: "Филиал", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	reasons := &metadata.Entity{
		Name: "Причина", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
			{Name: "Филиал", Type: metadata.FieldType("reference:" + branches.Name), RefEntity: branches.Name},
		},
	}
	notFolder := metadata.FormChoiceCondition{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPtr(false)}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Обращение", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			refChoiceElement("in-folder", "ВПапке",
				metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Ref: f.working.String()}, notFolder),
			refChoiceElement("children", "Дети",
				metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpEqual, Ref: f.working.String()}, notFolder),
			refChoiceElement("branch", "ПоФилиалу",
				metadata.FormChoiceCondition{Field: "Филиал", Op: metadata.FormChoiceOpEqual, Ref: f.center.String()}, notFolder),
			refChoiceElement("branch-or-common", "ОбщиеИФилиал",
				metadata.FormChoiceCondition{Field: "Филиал", Op: metadata.FormChoiceOpEqualOrEmpty, Ref: f.center.String()}, notFolder),
			refChoiceElement("missing", "Пропавшая",
				metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Ref: missing.String()}, notFolder),
			refChoiceElement("hidden", "Скрытая",
				metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Ref: f.secret.String()}, notFolder),
		},
	}
	ownerFields := []metadata.Field{}
	for _, name := range []string{"ВПапке", "Дети", "ПоФилиалу", "ОбщиеИФилиал", "Пропавшая", "Скрытая"} {
		ownerFields = append(ownerFields, metadata.Field{Name: name, Type: metadata.FieldType("reference:" + reasons.Name), RefEntity: reasons.Name})
	}
	owner := &metadata.Entity{Name: "Обращение", Kind: metadata.KindDocument, Fields: ownerFields, Forms: []*metadata.FormModule{form}}
	entities := []*metadata.Entity{branches, reasons, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		id   uuid.UUID
		name string
	}{{f.center, "Центр"}, {f.north, "Север"}} {
		if err := db.Upsert(ctx, branches.Name, row.id, map[string]any{"Наименование": row.name}, branches); err != nil {
			t.Fatalf("seed branch: %v", err)
		}
	}
	for _, row := range []struct {
		id       uuid.UUID
		name     string
		parent   uuid.UUID
		folder   bool
		audience string
		branch   uuid.UUID
	}{
		{f.working, "Рабочие", uuid.Nil, true, "all", uuid.Nil},
		{f.repair, "Ремонт", f.working, true, "all", uuid.Nil},
		{f.archive, "Архив", uuid.Nil, true, "all", uuid.Nil},
		{f.secret, "Секретная", uuid.Nil, true, "bob", uuid.Nil},
		{f.replace, "замена", f.repair, false, "all", f.center},
		{f.consult, "консультация", f.working, false, "all", f.center},
		{f.closed, "закрытая", f.working, false, "bob", f.center},
		{f.old, "старая", f.archive, false, "all", f.north},
		{f.mystery, "тайна", f.secret, false, "all", uuid.Nil},
		{f.root, "корневая", uuid.Nil, false, "all", uuid.Nil},
	} {
		fields := map[string]any{"Наименование": row.name, "ЭтоГруппа": row.folder, "Аудитория": row.audience}
		if row.parent != uuid.Nil {
			fields["Родитель"] = row.parent.String()
		}
		if row.branch != uuid.Nil {
			fields["Филиал"] = row.branch.String()
		}
		if err := db.Upsert(ctx, reasons.Name, row.id, fields, reasons); err != nil {
			t.Fatalf("seed reason %s: %v", row.name, err)
		}
	}
	if err := db.Upsert(ctx, owner.Name, f.ownerID, map[string]any{}, owner); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	f.server = &Server{reg: reg, store: db}
	f.server.entitySvc = f.server.newEntityService(nil)
	f.target, f.owner, f.branches = reasons, owner, branches

	// RLS: строка видна, если её аудитория — «all» или логин пользователя.
	rls := auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
		reasons.Name: {"read": {Any: []auth.RowPolicy{
			{Field: "Аудитория", Op: "eq", Value: auth.RowValue{Literal: "all"}},
			{Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}},
		}}},
	}}
	f.anna = &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{reasons.Name: {"read"}, branches.Name: {"read"}},
		Documents: map[string][]string{owner.Name: {"read", "write"}},
		RowAccess: rls,
	}}}}
	// Без права читать справочник филиалов: ссылка на филиал для него не
	// существует, и условие по ней закрывает выдачу.
	f.noBranches = &auth.User{Login: "viktor", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{reasons.Name: {"read"}},
		Documents: map[string][]string{owner.Name: {"read", "write"}},
		RowAccess: rls,
	}}}}
	return f
}

func (f refChoiceFixture) fetch(t *testing.T, user *auth.User, element string, selected uuid.UUID) choiceHTTPResponse {
	t.Helper()
	query := url.Values{"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {element}, "limit": {"100"}}
	if selected != uuid.Nil {
		query.Set("selected_id", selected.String())
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("element %s: status=%d body=%s", element, recorder.Code, recorder.Body.String())
	}
	return decodeChoiceHTTP(t, recorder)
}

func refChoiceLabels(response choiceHTTPResponse) string {
	labels := make([]string, 0, len(response.Items))
	for _, item := range response.Items {
		labels = append(labels, fmt.Sprint(item["_label"]))
	}
	sort.Strings(labels)
	return strings.Join(labels, ",")
}

// Отбор по постоянной ссылке: in_hierarchy и eq по parent_id, eq и
// eq_or_empty по обычному ссылочному реквизиту. RLS цели действует как
// всегда, total считается той же выборкой.
func TestRefOptionsChoiceRefFilters(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newRefChoiceFixture(t, db)
		for _, tc := range []struct {
			element string
			want    string
		}{
			{"in-folder", "замена,консультация"},
			{"children", "консультация"},
			{"branch", "замена,консультация"},
			{"branch-or-common", "замена,консультация,корневая,тайна"},
		} {
			t.Run(tc.element, func(t *testing.T) {
				got := f.fetch(t, f.anna, tc.element, uuid.Nil)
				if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
					t.Fatalf("total=%d items=%s, ожидалось %s", got.Total, labels, tc.want)
				}
			})
		}
	})
}

// Fail-closed: записи ссылки нет в базе, она закрыта строковым доступом или
// пользователь не вправе читать её справочник — подбор пуст и total = 0, хотя
// у «Секретной» есть видимый пользователю элемент «тайна».
func TestRefOptionsChoiceRefUnavailableIsClosed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newRefChoiceFixture(t, db)
		for _, tc := range []struct {
			name    string
			user    *auth.User
			element string
		}{
			{"записи нет в базе", f.anna, "missing"},
			{"запись закрыта RLS", f.anna, "hidden"},
			{"нет права на справочник ссылки", f.noBranches, "branch"},
			{"нет права на справочник ссылки, eq_or_empty", f.noBranches, "branch-or-common"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetch(t, tc.user, tc.element, uuid.Nil)
				if got.Total != 0 || len(got.Items) != 0 {
					t.Fatalf("недоступная ссылка открыла выдачу: total=%d items=%s", got.Total, refChoiceLabels(got))
				}
			})
		}
	})
}

// selected_allowed — тот же отбор и та же закрытость.
func TestRefOptionsChoiceRefSelectedAllowed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newRefChoiceFixture(t, db)
		for _, tc := range []struct {
			name     string
			element  string
			selected uuid.UUID
			allowed  bool
		}{
			{"элемент во вложенной папке", "in-folder", f.replace, true},
			{"элемент прямо в папке", "in-folder", f.consult, true},
			{"сама папка", "in-folder", f.working, false},
			{"чужая ветка", "in-folder", f.old, false},
			{"закрытый RLS", "in-folder", f.closed, false},
			{"корневая запись", "in-folder", f.root, false},
			{"ссылки нет в базе", "missing", f.replace, false},
			{"ссылка закрыта RLS", "hidden", f.mystery, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetch(t, f.anna, tc.element, tc.selected)
				if got.SelectedAllowed == nil || *got.SelectedAllowed != tc.allowed {
					t.Fatalf("selected_allowed=%v, ожидалось %v", got.SelectedAllowed, tc.allowed)
				}
			})
		}
	})
}

// Браузер ref не присылает и подменить не может: лишний источник в контексте
// подбора — ошибка запроса, а не другое значение условия.
func TestRefOptionsChoiceRefRejectsBrowserSource(t *testing.T) {
	db, err := storage.ConnectSQLite(context.Background(), t.TempDir()+"/ref-source.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := newRefChoiceFixture(t, db)
	query := url.Values{
		"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {"in-folder"},
		"sources": {`{"Объект.ВПапке":"` + f.archive.String() + `"}`},
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), f.anna))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("подмена источника: status=%d body=%s, ожидался 400", recorder.Code, recorder.Body.String())
	}
}

// Первая отрисовка формы: список поля уже отобран постоянной ссылкой, а у
// поля с пропавшей ссылкой вариантов нет совсем.
func TestManagedChoiceRefInitialRender(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newRefChoiceFixture(t, db)
		router := chi.NewRouter()
		f.server.Mount(router)
		request := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
		request = request.WithContext(auth.ContextWithUser(request.Context(), f.anna))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("form status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		document, err := html.Parse(strings.NewReader(recorder.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		options := func(field string) string {
			selectNode := findSelectByName(document, field)
			if selectNode == nil {
				t.Fatalf("select %q не найден", field)
			}
			var values []string
			for option := selectNode.FirstChild; option != nil; option = option.NextSibling {
				if option.Type != html.ElementNode || option.Data != "option" {
					continue
				}
				if value, ok := htmlAttribute(option, "value"); ok && value != "" {
					values = append(values, value)
				}
			}
			sort.Strings(values)
			return strings.Join(values, ",")
		}
		want := []string{f.replace.String(), f.consult.String()}
		sort.Strings(want)
		if got := options("ВПапке"); got != strings.Join(want, ",") {
			t.Fatalf("первая отрисовка: %v, ожидались элементы папки «Рабочие» %v", got, want)
		}
		if got := options("Пропавшая"); got != "" {
			t.Fatalf("пропавшая ссылка дала варианты: %v", got)
		}
	})
}
