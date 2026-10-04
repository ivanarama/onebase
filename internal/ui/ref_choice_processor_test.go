package ui

import (
	"encoding/json"
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
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// choice_filter в форме обработки (#1840): владелец формы — обработка,
// источник Объект.<Параметр> — её параметр. Тот же серверный путь, что у
// формы документа: условия восстанавливаются из метаданных, права запуска
// обработки и чтения цели, RLS и маски, пустая выдача без источника.

const (
	processorChoiceName    = "ПодборНеисправности"
	processorChoiceForm    = "ФормаОбработки"
	processorChoiceElement = "fault-picker"
)

type processorChoiceFixture struct {
	server   *Server
	target   *metadata.Entity
	rootA    uuid.UUID
	childA   uuid.UUID
	rootB    uuid.UUID
	names    map[string]string // id -> Наименование
	operator *auth.User
}

func processorChoiceUUID(prefix byte, number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("%02x000000-0000-0000-0000-%012d", prefix, number))
}

func newProcessorChoiceFixture(t *testing.T, db *storage.DB) processorChoiceFixture {
	t.Helper()
	ctx := t.Context()
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
	entities := []*metadata.Entity{direction, target}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	f := processorChoiceFixture{
		target: target,
		rootA:  processorChoiceUUID(0x10, 1), childA: processorChoiceUUID(0x10, 2), rootB: processorChoiceUUID(0x10, 3),
		names: map[string]string{},
	}
	for _, row := range []struct {
		id     uuid.UUID
		name   string
		parent *uuid.UUID
	}{{f.rootA, "A", nil}, {f.childA, "A.1", &f.rootA}, {f.rootB, "B", nil}} {
		fields := map[string]any{"Наименование": row.name, "is_folder": true}
		if row.parent != nil {
			fields["parent_id"] = row.parent.String()
		}
		if err := db.Upsert(ctx, direction.Name, row.id, fields, direction); err != nil {
			t.Fatalf("seed direction: %v", err)
		}
	}
	for i, row := range []struct {
		name      string
		direction uuid.UUID
		audience  string
	}{
		{"fault A", f.rootA, "anna"},
		{"fault A.1", f.childA, "anna"},
		{"fault A hidden", f.rootA, "bob"}, // закрыта строковым доступом
		{"fault B", f.rootB, "anna"},
	} {
		id := processorChoiceUUID(0x20, i+1)
		f.names[id.String()] = row.name
		if err := db.Upsert(ctx, target.Name, id, map[string]any{
			"Наименование": row.name, "Направление": row.direction.String(), "Аудитория": row.audience,
		}, target); err != nil {
			t.Fatalf("seed target: %v", err)
		}
	}

	proc, err := processor.ParseBytes([]byte(fmt.Sprintf(`
name: %s
params:
  - {name: Направление, type: "reference:Направление", default: "%s"}
  - {name: Неисправность, type: "reference:Неисправность"}
`, processorChoiceName, f.rootA)))
	if err != nil {
		t.Fatalf("parse processor: %v", err)
	}
	proc.Forms = []*metadata.FormModule{{
		Name: processorChoiceForm, Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			{
				ID: processorChoiceElement, Name: "ПолеНеисправность", Kind: metadata.FormElementField,
				DataPath: "Объект.Неисправность",
				ChoiceFilter: []metadata.FormChoiceCondition{{
					Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление",
				}},
			},
		},
	}}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	reg.LoadProcessors([]*processor.Processor{proc})
	f.server = &Server{reg: reg, store: db}
	f.server.entitySvc = f.server.newEntityService(nil)
	f.operator = processorChoiceUser(true, nil)
	return f
}

// processorChoiceUser — оператор видит только свои неисправности (RLS по
// аудитории); canRun — право запуска обработки.
func processorChoiceUser(canRun bool, targetPolicies auth.FieldPolicies) *auth.User {
	permission := auth.Permission{
		Catalogs: map[string][]string{"Направление": {"read"}, "Неисправность": {"read"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			"Неисправность": {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}
	// Явный запрет — пустая карта processors: {} (отсутствие секции по плану
	// 162 пока означает allow-all).
	permission.Processors = map[string][]string{}
	if canRun {
		permission.Processors[processorChoiceName] = []string{"run"}
	}
	if len(targetPolicies) > 0 {
		permission.FieldAccess = auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Неисправность": targetPolicies}}
	}
	return &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: permission}}}
}

func (f processorChoiceFixture) serve(t *testing.T, user *auth.User, path string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func (f processorChoiceFixture) query(source string) url.Values {
	sources, _ := json.Marshal(map[string]string{"Объект.Направление": source})
	return url.Values{
		"form_entity": {processorChoiceName},
		"form_kind":   {choiceFormKindProcessor},
		"form":        {processorChoiceForm},
		"element":     {processorChoiceElement},
		"sources":     {string(sources)},
	}
}

func (f processorChoiceFixture) refOptions(t *testing.T, user *auth.User, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return f.serve(t, user, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode())
}

func (f processorChoiceFixture) labels(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var out []string
	for _, item := range decodeChoiceHTTP(t, recorder).Items {
		out = append(out, f.names[refValueString(item["id"])])
	}
	sort.Strings(out)
	return out
}

func TestProcessorFormChoiceFilterInitialRender(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newProcessorChoiceFixture(t, db)
		recorder := f.serve(t, f.operator, "/ui/processor/"+url.PathEscape(processorChoiceName))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET формы обработки: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		document, err := html.Parse(strings.NewReader(recorder.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		selectNode := findSelectByName(document, "Неисправность")
		if selectNode == nil {
			t.Fatal("поле Неисправность не найдено в форме обработки")
		}
		raw, ok := htmlAttribute(selectNode, "data-ref-choice-context")
		if !ok {
			t.Fatal("у поля формы обработки нет контекста подбора — подбор ушёл бы без отбора")
		}
		var pickerContext managedChoiceContext
		if err := json.Unmarshal([]byte(raw), &pickerContext); err != nil {
			t.Fatalf("контекст подбора %q: %v", raw, err)
		}
		if pickerContext.FormEntity != processorChoiceName || pickerContext.FormKind != choiceFormKindProcessor ||
			pickerContext.Form != processorChoiceForm || pickerContext.Element != processorChoiceElement ||
			pickerContext.Sources["Объект.Направление"] != "Направление" {
			t.Fatalf("контекст подбора = %#v", pickerContext)
		}
		var got []string
		for option := selectNode.FirstChild; option != nil; option = option.NextSibling {
			if option.Type != html.ElementNode || option.Data != "option" {
				continue
			}
			if value, _ := htmlAttribute(option, "value"); value != "" {
				got = append(got, f.names[value])
			}
		}
		sort.Strings(got)
		// Параметр Направление по умолчанию — A: первая отрисовка показывает
		// неисправности ветки A, кроме закрытой строковым доступом.
		if want := []string{"fault A", "fault A.1"}; strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("первый список формы обработки = %v, ожидалось %v", got, want)
		}
	})
}

func TestProcessorFormChoiceFilterRefOptions(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newProcessorChoiceFixture(t, db)

		t.Run("отбор по параметру обработки с RLS цели", func(t *testing.T) {
			if got := f.labels(t, f.refOptions(t, f.operator, f.query(f.rootA.String()))); strings.Join(got, "|") != "fault A|fault A.1" {
				t.Fatalf("ветка A: %v", got)
			}
			if got := f.labels(t, f.refOptions(t, f.operator, f.query(f.rootB.String()))); strings.Join(got, "|") != "fault B" {
				t.Fatalf("ветка B: %v", got)
			}
		})

		t.Run("пустой источник — пустая выдача, а не весь справочник", func(t *testing.T) {
			if got := f.labels(t, f.refOptions(t, f.operator, f.query(""))); len(got) != 0 {
				t.Fatalf("без значения параметра показан справочник: %v", got)
			}
		})

		t.Run("реквизит отбора под полевой политикой не участвует", func(t *testing.T) {
			for _, strategy := range []string{"hide", "mask_all"} {
				masked := processorChoiceUser(true, auth.FieldPolicies{"Направление": {Read: strategy}})
				if got := f.labels(t, f.refOptions(t, masked, f.query(f.rootA.String()))); len(got) != 0 {
					t.Fatalf("политика %q обойдена подбором формы обработки: %v", strategy, got)
				}
			}
		})

		t.Run("подмена контекста отвергается", func(t *testing.T) {
			cases := map[string]func(url.Values){
				"без form_kind — пространство сущностей": func(q url.Values) { q.Del("form_kind") },
				"неизвестный form_kind":                  func(q url.Values) { q.Set("form_kind", "report") },
				"чужая форма":                            func(q url.Values) { q.Set("form", "ФормаОбъекта") },
				"неизвестный элемент":                    func(q url.Values) { q.Set("element", "direction") },
				"неизвестная обработка":                  func(q url.Values) { q.Set("form_entity", "НетТакой") },
				"необъявленный источник": func(q url.Values) {
					q.Set("sources", `{"Объект.Неисправность":"`+f.rootA.String()+`"}`)
				},
				"испорченное значение источника": func(q url.Values) { q.Set("sources", `{"Объект.Направление":"не-uuid"}`) },
			}
			for name, mutate := range cases {
				query := f.query(f.rootA.String())
				mutate(query)
				if recorder := f.refOptions(t, f.operator, query); recorder.Code != http.StatusBadRequest {
					t.Fatalf("%s: status=%d body=%s", name, recorder.Code, recorder.Body.String())
				}
			}
		})

		t.Run("без права запуска обработки контекст не принимается", func(t *testing.T) {
			noRun := processorChoiceUser(false, nil)
			if recorder := f.refOptions(t, noRun, f.query(f.rootA.String())); recorder.Code != http.StatusBadRequest {
				t.Fatalf("подбор формы обработки без права run: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})

		t.Run("цель подбора должна совпадать с полем формы", func(t *testing.T) {
			recorder := f.serve(t, f.operator, "/ui/_ref-options/"+url.PathEscape("Направление")+"?"+f.query(f.rootA.String()).Encode())
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("подмена цели: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	})
}
