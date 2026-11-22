package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/stretchr/testify/require"
)

func TestUI_DocumentLists_ValueQueryBeforeCountAndPage(t *testing.T) {
	department := &metadata.Entity{Name: "FilterDepartment", Kind: metadata.KindCatalog}
	departmentField := metadata.Field{Name: "Подразделение", Type: metadata.FieldTypeString, RefEntity: department.Name}
	assignments := &metadata.Entity{Name: "FilterAssignment", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Account", Type: metadata.FieldTypeString}, departmentField,
	}}
	const valueQuery = "ВЫБРАТЬ А.Подразделение ИЗ Справочник.FilterAssignment КАК А ГДЕ А.Account = &ТекущийПользователь"
	entities := []*metadata.Entity{department, assignments}
	var docs []*metadata.Entity
	for _, name := range []string{"ОценкаЗвонка", "ОценкаЗвонкаСветофор"} {
		doc := &metadata.Entity{Name: name, Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate}, departmentField,
		}}
		doc.Forms = []*metadata.FormModule{{
			Name: "ФормаСписка", Kind: "list", EntityName: name, LayoutKind: metadata.FormLayoutManaged,
			Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Номер", DataPath: "Номер"}},
			ListFilter: []metadata.FormListCondition{{
				Field: "Подразделение", Op: "eq", Roles: []string{"ОТКРуководитель"}, ValueQuery: valueQuery,
			}},
		}}
		entities = append(entities, doc)
		docs = append(docs, doc)
	}
	s, ctx := newSubmitTestServer(t, entities)
	own, other := uuid.New(), uuid.New()
	for _, dept := range []uuid.UUID{own, other} {
		require.NoError(t, s.store.Upsert(ctx, department.Name, dept, nil, department))
	}
	account := uuid.NewString()
	assign := func(dept uuid.UUID) {
		t.Helper()
		require.NoError(t, s.store.Upsert(ctx, assignments.Name, uuid.New(), map[string]any{"Account": account, "Подразделение": dept.String()}, assignments))
	}
	assign(own)
	date := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.store.WithTx(ctx, func(tx context.Context) error {
		for _, doc := range docs {
			for i := 0; i <= 10000; i++ {
				if err := s.store.Upsert(tx, doc.Name, uuid.New(), map[string]any{
					"Номер": fmt.Sprintf("OWN%05d", i), "Подразделение": own.String(), "Дата": date,
				}, doc); err != nil {
					return err
				}
			}
			if err := s.store.Upsert(tx, doc.Name, uuid.New(), map[string]any{
				"Номер": "FOREIGN", "Подразделение": other.String(), "Дата": date,
			}, doc); err != nil {
				return err
			}
		}
		return nil
	}))
	perm := auth.Permission{
		Catalogs:  map[string][]string{assignments.Name: {"read"}, department.Name: {"read"}},
		Documents: map[string][]string{docs[0].Name: {"read"}, docs[1].Name: {"read"}},
	}
	actor := func(id, role string, p auth.Permission) *auth.User {
		return &auth.User{ID: id, Login: id, Roles: []*auth.Role{{Name: role, Permissions: p}}}
	}
	read := func(user *auth.User, doc *metadata.Entity, params url.Values) string {
		t.Helper()
		req := reqWithChi(http.MethodGet, "/ui/document/"+doc.Name+"?"+params.Encode(), nil, map[string]string{"kind": "document", "entity": doc.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		s.list(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}
	lead := actor(account, "ОТКРуководитель", perm)
	for _, doc := range docs {
		last := url.Values{"limit": {"1"}, "page": {"10001"}, "sort": {"Номер"}, "dir": {"asc"}}
		html := read(lead, doc, last)
		require.Contains(t, html, "(10001 записей)")
		require.Contains(t, html, ">OWN10000<")
		require.NotContains(t, html, ">FOREIGN<")
		ownNumber := url.Values{"f.Номер": {"OWN00001"}, "f.Дата.from": {"2026-09-02"}, "f.Дата.to": {"2026-09-02"}}
		require.Contains(t, read(lead, doc, ownNumber), ">OWN00001<")
		ownNumber.Set("f.Дата.from", "2026-09-03")
		ownNumber.Del("f.Дата.to")
		require.NotContains(t, read(lead, doc, ownNumber), ">OWN00001<")
		foreign := url.Values{"f.Номер": {"FOREIGN"}, "lf_off": {"0"}}
		require.NotContains(t, read(lead, doc, foreign), ">FOREIGN<")
		require.Contains(t, read(actor(account, "ДругаяРоль", perm), doc, foreign), ">FOREIGN<")
		number := url.Values{"f.Номер": {"OWN00001"}}
		require.NotContains(t, read(actor(uuid.NewString(), "ОТКРуководитель", perm), doc, number), ">OWN00001<")
		denied := perm
		denied.Catalogs = nil
		require.NotContains(t, read(actor(account, "ОТКРуководитель", denied), doc, number), ">OWN00001<")
		condition := &doc.Forms[0].ListFilter[0]
		for _, invalid := range []string{
			"ВЫБРАТЬ 'not a UUID' КАК Подразделение",
			"ВЫБРАТЬ А.Подразделение, А.Account ИЗ Справочник.FilterAssignment КАК А ГДЕ А.Account = &ТекущийПользователь",
		} {
			condition.ValueQuery = invalid
			require.NotContains(t, read(lead, doc, number), ">OWN00001<")
		}
		condition.ValueQuery = valueQuery
	}
	// Duplicate source rows are ambiguous even if they carry the same UUID.
	assign(own)
	for _, doc := range docs {
		require.NotContains(t, read(lead, doc, url.Values{"f.Номер": {"OWN00001"}}), ">OWN00001<")
	}
}
