package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// choice_filter по колонке табличной части выбираемого справочника (#1822)
// через публичный маршрут подбора /ui/_ref-options. Бренд подходит, если в
// его ТЧ «Направления» есть строка с выбранным направлением (EXISTS): без
// дублей в выдаче и total, с RLS цели, пустой источник — пустой подбор, а
// колонка под полевой политикой или ПДн закрывает условие.
//
// Бренды: Холодильный (ХД), Швейный (ШМ), Универсальный (ХД, ШМ, ХД — две
// строки с одним направлением), Закрытый (ХД, RLS: только bob), Без
// направлений.

type tablePartChoiceFixture struct {
	server         *Server
	brand, owner   *metadata.Entity
	fridge, sewing uuid.UUID
}

func newTablePartChoiceFixture(t *testing.T, db *storage.DB, pii bool) tablePartChoiceFixture {
	t.Helper()
	ctx := context.Background()
	f := tablePartChoiceFixture{fridge: choiceHTTPUUID(0x80, 1), sewing: choiceHTTPUUID(0x80, 2)}
	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	brand := &metadata.Entity{
		Name: "Бренд", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{
			Name: "Направления",
			Fields: []metadata.Field{{
				Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name, PII: pii,
			}},
		}},
	}
	condition := func(c metadata.FormChoiceCondition) []metadata.FormChoiceCondition {
		return []metadata.FormChoiceCondition{c}
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Заявка", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			{ID: "brand-from", Name: "ПолеБренд", Kind: metadata.FormElementField, DataPath: "Объект.Бренд",
				ChoiceFilter: condition(metadata.FormChoiceCondition{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление"})},
			{ID: "brand-ref", Name: "ПолеБрендХД", Kind: metadata.FormElementField, DataPath: "Объект.БрендХД",
				ChoiceFilter: condition(metadata.FormChoiceCondition{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, Ref: f.fridge.String()})},
			// Записи постоянной ссылки в базе нет — подбор пуст, а не весь справочник.
			{ID: "brand-missing", Name: "ПолеБрендНет", Kind: metadata.FormElementField, DataPath: "Объект.БрендНет",
				ChoiceFilter: condition(metadata.FormChoiceCondition{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, Ref: choiceHTTPUUID(0x8f, 1).String()})},
		},
	}
	owner := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "Бренд", Type: metadata.FieldType("reference:" + brand.Name), RefEntity: brand.Name},
			{Name: "БрендХД", Type: metadata.FieldType("reference:" + brand.Name), RefEntity: brand.Name},
			{Name: "БрендНет", Type: metadata.FieldType("reference:" + brand.Name), RefEntity: brand.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{direction, brand, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[uuid.UUID]string{f.fridge: "ХД", f.sewing: "ШМ"} {
		if err := db.Upsert(ctx, direction.Name, id, map[string]any{"Наименование": name}, direction); err != nil {
			t.Fatalf("seed direction: %v", err)
		}
	}
	for i, row := range []struct {
		name, audience string
		dirs           []uuid.UUID
	}{
		{"Холодильный", "all", []uuid.UUID{f.fridge}},
		{"Швейный", "all", []uuid.UUID{f.sewing}},
		{"Универсальный", "all", []uuid.UUID{f.fridge, f.sewing, f.fridge}},
		{"Закрытый", "bob", []uuid.UUID{f.fridge}},
		{"Без направлений", "all", nil},
	} {
		id := choiceHTTPUUID(0x81, i+1)
		if err := db.Upsert(ctx, brand.Name, id, map[string]any{"Наименование": row.name, "Аудитория": row.audience}, brand); err != nil {
			t.Fatalf("seed brand: %v", err)
		}
		lines := make([]map[string]any, 0, len(row.dirs))
		for _, d := range row.dirs {
			lines = append(lines, map[string]any{"Направление": d.String()})
		}
		if len(lines) > 0 {
			if err := db.UpsertTablePartRows(ctx, brand.Name, "Направления", id, lines, brand.TableParts[0]); err != nil {
				t.Fatalf("seed brand table part: %v", err)
			}
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	f.server = &Server{reg: reg, store: db}
	f.server.entitySvc = f.server.newEntityService(nil)
	f.brand, f.owner = brand, owner
	return f
}

// tablePartChoiceUser — оператор: RLS брендов по аудитории; policies — полевые
// политики Бренда.
func tablePartChoiceUser(policies auth.FieldPolicies) *auth.User {
	permission := auth.Permission{
		Catalogs:  map[string][]string{"Бренд": {"read"}, "Направление": {"read"}},
		Documents: map[string][]string{"Заявка": {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			"Бренд": {"read": {Any: []auth.RowPolicy{
				{Field: "Аудитория", Op: "eq", Value: auth.RowValue{Literal: "all"}},
				{Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}},
			}}},
		}},
	}
	if policies != nil {
		permission.FieldAccess = auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Бренд": policies}}
	}
	return &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: permission}}}
}

func (f tablePartChoiceFixture) fetch(t *testing.T, user *auth.User, element, direction string) choiceHTTPResponse {
	t.Helper()
	query := url.Values{"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {element}, "limit": {"100"}}
	if element == "brand-from" {
		sources, _ := json.Marshal(map[string]string{"Объект.Направление": direction})
		query.Set("sources", string(sources))
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.brand.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("element %s: status=%d body=%s", element, recorder.Code, recorder.Body.String())
	}
	return decodeChoiceHTTP(t, recorder)
}

func TestRefOptionsChoiceTablePart(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTablePartChoiceFixture(t, db, false)
		operator := tablePartChoiceUser(nil)
		for _, tc := range []struct {
			name, element, direction, want string
		}{
			// «Закрытый» тоже холодильный, но RLS цели его прячет; «Универсальный»
			// с двумя строками ХД приходит один раз.
			{"from ХД", "brand-from", f.fridge.String(), "Универсальный,Холодильный"},
			{"from ШМ", "brand-from", f.sewing.String(), "Универсальный,Швейный"},
			{"ref ХД", "brand-ref", "", "Универсальный,Холодильный"},
			{"пустой источник", "brand-from", "", ""},
			{"ref на несуществующую запись", "brand-missing", "", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetch(t, operator, tc.element, tc.direction)
				if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
					t.Fatalf("total=%d items=%q, ожидалось %q", got.Total, labels, tc.want)
				}
			})
		}
	})
}

// Колонка ТЧ под полевой политикой или ПДн: выдача и total отвечали бы на
// вопрос «есть ли у бренда строка с этим направлением», поэтому условие
// закрывается так же, как у замаскированного реквизита шапки. Явное read: full
// открывает, администратору маска не применяется.
func TestRefOptionsChoiceTablePartProtectedColumnIsClosed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, tc := range []struct {
			name     string
			pii      bool
			policies auth.FieldPolicies
			user     func(auth.FieldPolicies) *auth.User
			want     string
		}{
			{name: "политика на колонку", policies: auth.FieldPolicies{"Направления.Направление": {Read: "mask_all"}}, user: tablePartChoiceUser},
			{name: "политика на ТЧ целиком", policies: auth.FieldPolicies{"Направления": {Read: "hide"}}, user: tablePartChoiceUser},
			{name: "ПДн колонки, роль молчит", pii: true, user: tablePartChoiceUser},
			{name: "ПДн колонки, явный read: full", pii: true, policies: auth.FieldPolicies{"Направления.Направление": {Read: "full"}},
				user: tablePartChoiceUser, want: "Универсальный,Холодильный"},
			{name: "ПДн колонки, администратор", pii: true,
				user: func(auth.FieldPolicies) *auth.User { return &auth.User{Login: "root", IsAdmin: true} },
				want: "Закрытый,Универсальный,Холодильный"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newTablePartChoiceFixture(t, db, tc.pii)
				for _, element := range []string{"brand-from", "brand-ref"} {
					got := f.fetch(t, tc.user(tc.policies), element, f.fridge.String())
					if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
						t.Fatalf("%s: total=%d items=%q, ожидалось %q", element, got.Total, labels, tc.want)
					}
				}
			})
		}
	})
}

// Закрытый источник from (#1917, ревью круг 1): запись направления, которую
// пользователь не видит, не должна отвечать, у каких брендов она стоит в ТЧ.
// Тот же гейт, что у ref: нет права чтения справочника источника или строку
// закрывает RLS — подбор пуст, а видимое направление работает как обычно.
func TestRefOptionsChoiceTablePartClosedSourceIsClosed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTablePartChoiceFixture(t, db, false)
		noDirectionRead := tablePartChoiceUser(nil)
		delete(noDirectionRead.Roles[0].Permissions.Catalogs, "Направление")
		onlySewing := tablePartChoiceUser(nil)
		onlySewing.Roles[0].Permissions.RowAccess.Catalogs["Направление"] = auth.RowPolicies{
			"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "ШМ"}},
		}
		for _, tc := range []struct {
			name      string
			user      *auth.User
			direction uuid.UUID
			want      string
		}{
			{"нет права чтения направлений", noDirectionRead, f.fridge, ""},
			{"RLS закрывает направление", onlySewing, f.fridge, ""},
			{"RLS открывает направление", onlySewing, f.sewing, "Универсальный,Швейный"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetch(t, tc.user, "brand-from", tc.direction.String())
				if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
					t.Fatalf("total=%d items=%q, ожидалось %q", got.Total, labels, tc.want)
				}
			})
		}
	})
}
