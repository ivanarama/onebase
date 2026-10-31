package ui

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/shopspring/decimal"
)

func TestManagedEntityListFormAttributeSourceEvent(t *testing.T) {
	for _, tc := range []struct {
		name, typeRef, raw, message string
		field                       metadata.Field
		value                       any
	}{
		{"string", "String", "needle", "needle", metadata.Field{Type: metadata.FieldTypeString}, "needle"},
		{"empty", "String", "", "Строка", metadata.Field{Type: metadata.FieldTypeString}, "unused"},
		{"number", "Number", "12,50", "Число", metadata.Field{Type: metadata.FieldTypeNumber}, decimal.RequireFromString("12.50")},
		{"false", "Boolean", "false", "Булево", metadata.Field{Type: metadata.FieldTypeBool}, false},
		{"true", "Boolean", "true", "Булево", metadata.Field{Type: metadata.FieldTypeBool}, true},
		{"date", "Date", "2026-10-19", "Дата", metadata.Field{Type: metadata.FieldTypeDate}, time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)},
		{"reference", "CatalogRef.Склад", "", "Основной", metadata.Field{Type: metadata.FieldType("reference:Склад"), RefEntity: "Склад"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expression := "ТипЗнч(Объект.Поиск)"
			if tc.name == "string" {
				expression = "Объект.Поиск"
			}
			if tc.name == "reference" {
				expression = "Объект.Поиск.Наименование"
			}
			srv, owner, refID := attrEventServer(t, "Процедура Проверить()\n Сообщить("+expression+");\nКонецПроцедуры")
			form := owner.Forms[0]
			form.Attributes = []*metadata.FormAttribute{{Name: "Поиск", TypeRef: tc.typeRef}}
			form.Elements = []*metadata.FormElement{
				{Name: "Search", Kind: metadata.FormElementField, DataPath: "Форма.Поиск"},
				{Name: "Check", Kind: metadata.FormElementButton, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"}},
			}
			field := tc.field
			field.Name = "Значение"
			target := &metadata.Entity{Name: "Результаты", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString}, field,
			}}
			warehouse := srv.reg.GetEntity("Склад")
			if err := srv.store.Migrate(context.Background(), []*metadata.Entity{warehouse, target, owner}); err != nil {
				t.Fatal(err)
			}
			raw, value := tc.raw, tc.value
			if tc.name == "reference" {
				raw = refID.String()
				value = refID
			}
			if err := srv.store.Upsert(context.Background(), target.Name, uuid.New(), map[string]any{"Наименование": "match", "Значение": value}, target); err != nil {
				t.Fatal(err)
			}
			srv.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{owner, warehouse, target}})
			for _, withList := range []bool{false, true} {
				if withList {
					form.Elements = append(form.Elements, &metadata.FormElement{ID: "results", Name: "Results", Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{
						Entity: target.Name, Columns: []string{"Наименование"}, Filter: []metadata.FormEntityListCondition{{Field: "Значение", Op: "eq", Source: "Форма.Поиск"}},
					}})
					if err := metadata.ValidateFormEntityLists(owner, form, srv.reg.Entities()); err != nil {
						t.Fatal(err)
					}
				}
				rec := executeFormEvent(t, srv, owner, url.Values{"_element": {"Check"}, "_event": {"Нажатие"}, "_kind": {"object"}, "Поиск": {raw}})
				response := decodeFormEventResponse(t, rec.Body.Bytes())
				if !response.OK || response.Error != "" || strings.Join(response.Messages, "|") != tc.message {
					t.Fatalf("withList=%v: %s", withList, rec.Body.String())
				}
				if tc.name == "string" && response.Values["Поиск"] != "needle" {
					t.Fatalf("lost attribute: %s", rec.Body.String())
				}
				for key := range response.Values {
					if strings.EqualFold(key, "Поиск") && key != "Поиск" {
						t.Fatalf("duplicate attribute key %q: %s", key, rec.Body.String())
					}
				}
				if withList {
					list := response.EntityLists["results"]
					if tc.name == "empty" {
						if list.Status != "empty" {
							t.Fatalf("empty source: %+v", list)
						}
					} else if list.Status != "ready" || len(list.Rows) != 1 || list.Rows[0].Cells[0] != "match" {
						t.Fatalf("wrong typed source: %+v", list)
					}
				}
			}
		})
	}
}

func TestManagedEntityListReadonlyFormAttributeEvent(t *testing.T) {
	srv, owner := setupManagedEventsServer(t, `
Процедура Открыть()
 Объект.Поиск = "trusted";
КонецПроцедуры
Процедура Проверить()
 Сообщить(Объект.Поиск);
КонецПроцедуры`, map[metadata.FormEventType]string{metadata.FormEventOnOpen: "Открыть"}, []*metadata.FormElement{
		{Name: "Search", Kind: metadata.FormElementField, DataPath: "Форма.Поиск", ReadOnly: true},
		{Name: "Check", Kind: metadata.FormElementButton, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"}},
	})
	form := owner.Forms[0]
	form.Attributes = []*metadata.FormAttribute{{Name: "Поиск", TypeRef: "String"}}
	form.Elements = append(form.Elements, &metadata.FormElement{ID: "results", Name: "Results", Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{
		Entity: owner.Name, Columns: []string{"Наименование"}, Filter: []metadata.FormEntityListCondition{{Field: "Наименование", Op: "eq", Source: "Форма.Поиск"}},
	}})
	if err := srv.store.Upsert(context.Background(), owner.Name, uuid.New(), map[string]any{"Наименование": "trusted"}, owner); err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	srv.form(page, reqWithChi("GET", "/ui/catalog/Контрагент/new", nil, map[string]string{"entity": owner.Name, "kind": "catalog"}))
	if page.Code != 200 {
		t.Fatalf("GET: %d %s", page.Code, page.Body.String())
	}
	_, rest, ok := strings.Cut(page.Body.String(), `id="ob-entity-list-config">`)
	if !ok {
		t.Fatal("missing list config")
	}
	configJSON, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("unterminated config")
	}
	var config struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil || config.State == "" {
		t.Fatalf("invalid state: %v", err)
	}
	// ПриОткрытии runs as a client form-event after the initial GET.
	opened := decodeFormEventResponse(t, executeFormEvent(t, srv, owner, url.Values{
		"_event": {string(metadata.FormEventOnOpen)}, "_kind": {"object"}, "_entity_list_state": {config.State},
	}).Body.Bytes())
	if !opened.OK || opened.Values["Поиск"] != "trusted" || opened.EntityListState == "" {
		t.Fatalf("open event: %+v", opened)
	}
	config.State = opened.EntityListState
	body := url.Values{"_element": {"Check"}, "_event": {"Нажатие"}, "_kind": {"object"}, "Поиск": {"forged"}, "_entity_list_state": {config.State}}
	rec := executeFormEvent(t, srv, owner, body)
	response := decodeFormEventResponse(t, rec.Body.Bytes())
	if !response.OK || strings.Join(response.Messages, "|") != "trusted" || response.Values["Поиск"] != "trusted" || len(response.EntityLists["results"].Rows) != 1 {
		t.Fatalf("readonly form source overwritten: %s", rec.Body.String())
	}
	body.Set("_entity_list_state", config.State+"x")
	rec = executeFormEvent(t, srv, owner, body)
	response = decodeFormEventResponse(t, rec.Body.Bytes())
	if response.OK || response.Error == "" || len(response.Messages) != 0 {
		t.Fatalf("invalid state reached handler: %s", rec.Body.String())
	}
}

func TestManagedEntityListRefreshRejectsOversizedBody(t *testing.T) {
	f := entityListFixture(t)
	rec := listHTTP(t, f, f.ownerID.String(), f.rootA.String(), url.Values{"state": {strings.Repeat("x", 64<<10)}})
	if rec.Code != 400 {
		t.Fatalf("oversized refresh accepted: %d %s", rec.Code, rec.Body.String())
	}
}

func TestManagedEntityListEventRestoresAllListSources(t *testing.T) {
	srv, owner, _ := attrEventServer(t, `Процедура Проверить()
 Сообщить(Объект.Поиск + ":" + Объект.ДругойПоиск);
 Объект.Поиск = "after one";
 Объект.ДругойПоиск = "after two";
КонецПроцедуры`)
	form := owner.Forms[0]
	form.Attributes = []*metadata.FormAttribute{{Name: "Поиск", TypeRef: "String"}, {Name: "ДругойПоиск", TypeRef: "String"}}
	form.Elements = []*metadata.FormElement{
		{Name: "Search", Kind: metadata.FormElementField, DataPath: "Форма.Поиск"},
		{Name: "Other", Kind: metadata.FormElementField, DataPath: "Форма.ДругойПоиск"},
		{Name: "Check", Kind: metadata.FormElementButton, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"}},
	}
	for _, entry := range []struct{ id, path string }{{"one", "Форма.Поиск"}, {"alias", "форма.поиск"}, {"two", "Форма.ДругойПоиск"}} {
		form.Elements = append(form.Elements, &metadata.FormElement{ID: entry.id, Name: entry.id, Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{Entity: owner.Name, Columns: []string{"Наименование"}, Filter: []metadata.FormEntityListCondition{{Field: "Наименование", Op: "eq", Source: entry.path}}}})
	}
	for _, value := range []string{"after one", "after two"} {
		if err := srv.store.Upsert(context.Background(), owner.Name, uuid.New(), map[string]any{"Наименование": value}, owner); err != nil {
			t.Fatal(err)
		}
	}
	rec := executeFormEvent(t, srv, owner, url.Values{"_element": {"Check"}, "_event": {"Нажатие"}, "_kind": {"object"}, "Поиск": {"before one"}, "ДругойПоиск": {"before two"}})
	response := decodeFormEventResponse(t, rec.Body.Bytes())
	if !response.OK || strings.Join(response.Messages, "|") != "before one:before two" {
		t.Fatalf("event: %s", rec.Body.String())
	}
	for _, entry := range []struct{ id, value string }{{"one", "after one"}, {"alias", "after one"}, {"two", "after two"}} {
		got := response.EntityLists[entry.id]
		if got.Status != "ready" || len(got.Rows) != 1 || got.Rows[0].Cells[0] != entry.value {
			t.Fatalf("%s: %+v", entry.id, got)
		}
	}
}
