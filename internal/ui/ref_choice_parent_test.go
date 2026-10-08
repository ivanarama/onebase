package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
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

// parent_id через публичный маршрут подбора (#1819): товарная группа звонка
// выбирается из поддерева папки, указанной у выбранного направления.
//
//	choice_filter:
//	  - {field: parent_id, op: in_hierarchy, from: Объект.Направление.ТоварнаяГруппа}
//	  - {field: is_folder, op: eq, value: false}
//
// Дерево: Техника (папка) → Кухня (папка) → чайник; Техника → утюг, фен
// (фен закрыт строковым доступом); Прочее (папка) → лампа.
type parentChoiceFixture struct {
	server                        *Server
	target, owner                 *metadata.Entity
	tech, kitchen, other          uuid.UUID
	kettle, iron, dryer, lamp     uuid.UUID
	directionTech, directionEmpty uuid.UUID
	ownerID                       uuid.UUID
	user                          *auth.User
}

func newParentChoiceFixture(t *testing.T) parentChoiceFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "parent-choice.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	groups := &metadata.Entity{
		Name: "ТоварнаяГруппа", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
		},
	}
	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ТоварнаяГруппа", Type: metadata.FieldType("reference:" + groups.Name), RefEntity: groups.Name},
		},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Звонок", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			{
				ID: "group-picker", Name: "ПолеТоварнаяГруппа", Kind: metadata.FormElementField,
				DataPath: "Объект.ТоварнаяГруппа",
				ChoiceFilter: []metadata.FormChoiceCondition{
					{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление.ТоварнаяГруппа"},
					{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPtr(false)},
				},
			},
		},
	}
	owner := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "ТоварнаяГруппа", Type: metadata.FieldType("reference:" + groups.Name), RefEntity: groups.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{groups, direction, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	f := parentChoiceFixture{
		target: groups, owner: owner,
		tech: choiceHTTPUUID(0x60, 1), kitchen: choiceHTTPUUID(0x60, 2), other: choiceHTTPUUID(0x60, 3),
		kettle: choiceHTTPUUID(0x60, 11), iron: choiceHTTPUUID(0x60, 12), dryer: choiceHTTPUUID(0x60, 13),
		lamp:          choiceHTTPUUID(0x60, 14),
		directionTech: choiceHTTPUUID(0x61, 1), directionEmpty: choiceHTTPUUID(0x61, 2),
		ownerID: choiceHTTPUUID(0x62, 1),
	}
	for _, row := range []struct {
		id       uuid.UUID
		name     string
		parent   uuid.UUID
		folder   bool
		audience string
	}{
		{f.tech, "Техника", uuid.Nil, true, "anna"},
		{f.kitchen, "Кухня", f.tech, true, "anna"},
		{f.other, "Прочее", uuid.Nil, true, "anna"},
		{f.kettle, "чайник", f.kitchen, false, "anna"},
		{f.iron, "утюг", f.tech, false, "anna"},
		{f.dryer, "фен", f.tech, false, "bob"},
		{f.lamp, "лампа", f.other, false, "anna"},
	} {
		fields := map[string]any{"Наименование": row.name, "ЭтоГруппа": row.folder, "Аудитория": row.audience}
		if row.parent != uuid.Nil {
			fields["Родитель"] = row.parent.String()
		}
		if err := db.Upsert(ctx, groups.Name, row.id, fields, groups); err != nil {
			t.Fatalf("seed group %s: %v", row.name, err)
		}
	}
	for _, row := range []struct {
		id    uuid.UUID
		name  string
		group string
	}{{f.directionTech, "Техника", f.tech.String()}, {f.directionEmpty, "Без группы", ""}} {
		if err := db.Upsert(ctx, direction.Name, row.id, map[string]any{"Наименование": row.name, "ТоварнаяГруппа": row.group}, direction); err != nil {
			t.Fatalf("seed direction: %v", err)
		}
	}
	if err := db.Upsert(ctx, owner.Name, f.ownerID, map[string]any{"Направление": f.directionTech.String()}, owner); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	f.server = &Server{reg: reg, store: db}
	f.server.entitySvc = f.server.newEntityService(nil)
	f.user = &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{groups.Name: {"read"}, direction.Name: {"read"}},
		Documents: map[string][]string{owner.Name: {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			groups.Name: {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}}}}
	return f
}

func (f parentChoiceFixture) fetch(t *testing.T, direction string, selected uuid.UUID) choiceHTTPResponse {
	t.Helper()
	sources, _ := json.Marshal(map[string]string{"Объект.Направление.ТоварнаяГруппа": direction})
	query := url.Values{
		"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {"group-picker"},
		"sources": {string(sources)}, "limit": {"100"},
	}
	if selected != uuid.Nil {
		query.Set("selected_id", selected.String())
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
	return decodeChoiceHTTP(t, recorder)
}

func parentChoiceLabels(response choiceHTTPResponse) []string {
	labels := make([]string, 0, len(response.Items))
	for _, item := range response.Items {
		labels = append(labels, fmt.Sprint(item["_label"]))
	}
	sort.Strings(labels)
	return labels
}

// Элементы поддерева папки направления: сама папка, вложенные папки, чужая
// ветка и закрытое RLS не попадают ни в выдачу, ни в total.
func TestRefOptionsParentIDSubtreeOfDirectionGroup(t *testing.T) {
	f := newParentChoiceFixture(t)
	got := f.fetch(t, f.directionTech.String(), uuid.Nil)
	if labels := parentChoiceLabels(got); strings.Join(labels, ",") != "утюг,чайник" || got.Total != 2 {
		t.Fatalf("total=%d items=%v, ожидались элементы «Техники» без закрытого RLS", got.Total, labels)
	}
}

// Пустой источник закрывает выдачу: и направление не выбрано, и у выбранного
// направления нет группы. Весь справочник не показывается никогда.
func TestRefOptionsParentIDEmptySourceIsClosed(t *testing.T) {
	f := newParentChoiceFixture(t)
	for name, direction := range map[string]string{
		"направление не выбрано":   "",
		"у направления нет группы": f.directionEmpty.String(),
	} {
		t.Run(name, func(t *testing.T) {
			if got := f.fetch(t, direction, uuid.Nil); got.Total != 0 || len(got.Items) != 0 {
				t.Fatalf("пустой источник открыл выдачу: total=%d items=%v", got.Total, parentChoiceLabels(got))
			}
		})
	}
}

// selected_allowed — тот же отбор: элемент поддерева допустим, папка, чужая
// ветка и закрытое RLS — нет.
func TestRefOptionsParentIDSelectedAllowed(t *testing.T) {
	f := newParentChoiceFixture(t)
	for _, tc := range []struct {
		name     string
		selected uuid.UUID
		allowed  bool
	}{
		{"элемент во вложенной папке", f.kettle, true},
		{"элемент прямо в папке", f.iron, true},
		{"сама папка направления", f.tech, false},
		{"вложенная папка", f.kitchen, false},
		{"чужая ветка", f.lamp, false},
		{"закрытый RLS", f.dryer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := f.fetch(t, f.directionTech.String(), tc.selected)
			if got.SelectedAllowed == nil || *got.SelectedAllowed != tc.allowed {
				t.Fatalf("selected_allowed=%v, ожидалось %v", got.SelectedAllowed, tc.allowed)
			}
		})
	}
}

// Первая отрисовка формы сохранённого звонка: список поля уже отобран по
// направлению документа тем же правилом.
func TestManagedParentIDChoiceInitialRender(t *testing.T) {
	f := newParentChoiceFixture(t)
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
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
		t.Fatalf("select %q не найден", f.target.Name)
	}
	var options []string
	for option := selectNode.FirstChild; option != nil; option = option.NextSibling {
		if option.Type != html.ElementNode || option.Data != "option" {
			continue
		}
		if value, ok := htmlAttribute(option, "value"); ok && value != "" {
			options = append(options, value)
		}
	}
	sort.Strings(options)
	want := []string{f.kettle.String(), f.iron.String()}
	sort.Strings(want)
	if strings.Join(options, ",") != strings.Join(want, ",") {
		t.Fatalf("первая отрисовка: %v, ожидались элементы «Техники» %v", options, want)
	}
}
