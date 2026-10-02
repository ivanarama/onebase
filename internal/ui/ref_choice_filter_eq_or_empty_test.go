package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// eqOrEmptyFixture — реклама, отобранная по филиалу звонка через
// {field: Филиал, op: eq_or_empty, from: Объект.Филиал}: записи своего
// филиала и общие (без филиала). Одна общая запись закрыта строковым доступом.
type eqOrEmptyFixture struct {
	choiceHTTPFixture
	branchA, branchB           uuid.UUID
	adA1, adA2, adB, common1   uuid.UUID
	common2, commonHiddenByRLS uuid.UUID
}

func newEqOrEmptyFixture(t *testing.T) eqOrEmptyFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "eq-or-empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	branch := &metadata.Entity{
		Name: "Филиал", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	ad := &metadata.Entity{
		Name: "Реклама", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Филиал", Type: metadata.FieldType("reference:" + branch.Name), RefEntity: branch.Name},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
		},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Звонок", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "branch", Name: "ПолеФилиал", Kind: metadata.FormElementField, DataPath: "Объект.Филиал"},
			{
				ID: "ad-picker", Name: "ПолеРеклама", Kind: metadata.FormElementField, DataPath: "Объект.Реклама",
				ChoiceFilter: []metadata.FormChoiceCondition{{
					Field: "Филиал", Op: metadata.FormChoiceOpEqualOrEmpty, From: "Объект.Филиал",
				}},
			},
		},
	}
	call := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Филиал", Type: metadata.FieldType("reference:" + branch.Name), RefEntity: branch.Name},
			{Name: "Реклама", Type: metadata.FieldType("reference:" + ad.Name), RefEntity: ad.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{branch, ad, call}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	f := eqOrEmptyFixture{
		branchA: choiceHTTPUUID(0x40, 1), branchB: choiceHTTPUUID(0x40, 2),
		adA1: choiceHTTPUUID(0x50, 1), adA2: choiceHTTPUUID(0x50, 2), adB: choiceHTTPUUID(0x50, 3),
		common1: choiceHTTPUUID(0x50, 4), common2: choiceHTTPUUID(0x50, 5), commonHiddenByRLS: choiceHTTPUUID(0x50, 6),
	}
	for id, name := range map[uuid.UUID]string{f.branchA: "A", f.branchB: "B"} {
		if err := db.Upsert(ctx, branch.Name, id, map[string]any{"Наименование": name}, branch); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		id       uuid.UUID
		name     string
		branch   uuid.UUID
		audience string
	}{
		{f.adA1, "реклама A1", f.branchA, "anna"},
		{f.adA2, "реклама A2", f.branchA, "anna"},
		{f.adB, "реклама B", f.branchB, "anna"},
		{f.common1, "общая 1", uuid.Nil, "anna"},
		{f.common2, "общая 2", uuid.Nil, "anna"},
		{f.commonHiddenByRLS, "общая чужая", uuid.Nil, "bob"},
	} {
		fields := map[string]any{"Наименование": row.name, "Аудитория": row.audience}
		if row.branch != uuid.Nil {
			fields["Филиал"] = row.branch.String()
		}
		if err := db.Upsert(ctx, ad.Name, row.id, fields, ad); err != nil {
			t.Fatal(err)
		}
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	user := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{branch.Name: {"read"}, ad.Name: {"read"}},
		Documents: map[string][]string{call.Name: {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			ad.Name: {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}}}}
	server := &Server{reg: reg, store: db}
	server.entitySvc = server.newEntityService(nil)
	f.choiceHTTPFixture = choiceHTTPFixture{server: server, target: ad, owner: call, user: user}
	return f
}

// query — запрос подбора из формы звонка; source — значение Объект.Филиал
// («» — филиал не выбран).
func (f eqOrEmptyFixture) query(source string) url.Values {
	sources, _ := json.Marshal(map[string]string{"Объект.Филиал": source})
	return url.Values{
		"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {"ad-picker"},
		"sources": {string(sources)}, "limit": {"100"},
	}
}

func (f eqOrEmptyFixture) fetch(t *testing.T, query url.Values) choiceHTTPResponse {
	t.Helper()
	recorder := f.serveRefOptions(t, f.target, query)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return decodeChoiceHTTP(t, recorder)
}

func choiceItemIDs(response choiceHTTPResponse) []string {
	ids := make([]string, 0, len(response.Items))
	for _, item := range response.Items {
		ids = append(ids, fmt.Sprint(item["id"]))
	}
	sort.Strings(ids)
	return ids
}

func sortedIDs(ids ...uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	sort.Strings(out)
	return out
}

// Подбор через публичный маршрут: записи филиала источника и общие; чужой
// филиал и закрытое RLS не видны ни в выдаче, ни в total.
func TestRefOptionsEqualOrEmptyKeepsBranchAndCommonRows(t *testing.T) {
	f := newEqOrEmptyFixture(t)
	got := f.fetch(t, f.query(f.branchA.String()))
	want := sortedIDs(f.adA1, f.adA2, f.common1, f.common2)
	if ids := choiceItemIDs(got); fmt.Sprint(ids) != fmt.Sprint(want) || got.Total != len(want) {
		t.Fatalf("филиал A: total=%d items=%v, ожидались %v (свои + общие, без B и без закрытой RLS)", got.Total, ids, want)
	}
}

// Пустой источник у eq_or_empty — только общие записи, а не весь справочник и
// не пустая выдача (у eq пустой источник закрывает подбор).
func TestRefOptionsEqualOrEmptyEmptySourceShowsOnlyCommon(t *testing.T) {
	f := newEqOrEmptyFixture(t)
	for name, query := range map[string]url.Values{
		"пустое значение":     f.query(""),
		"источник не прислан": func() url.Values { q := f.query(""); q.Set("sources", `{}`); return q }(),
	} {
		t.Run(name, func(t *testing.T) {
			got := f.fetch(t, query)
			want := sortedIDs(f.common1, f.common2)
			if ids := choiceItemIDs(got); fmt.Sprint(ids) != fmt.Sprint(want) || got.Total != len(want) {
				t.Fatalf("total=%d items=%v, ожидались только общие %v", got.Total, ids, want)
			}
		})
	}
}

// selected_allowed следует тому же отбору: общая запись допустима при любом
// филиале, запись чужого филиала и закрытая RLS — нет.
func TestRefOptionsEqualOrEmptySelectedAllowed(t *testing.T) {
	f := newEqOrEmptyFixture(t)
	for _, tc := range []struct {
		name     string
		source   string
		selected uuid.UUID
		allowed  bool
	}{
		{"общая при филиале A", f.branchA.String(), f.common1, true},
		{"своя при филиале A", f.branchA.String(), f.adA2, true},
		{"чужой филиал", f.branchA.String(), f.adB, false},
		{"общая, закрытая RLS", f.branchA.String(), f.commonHiddenByRLS, false},
		{"общая без филиала", "", f.common2, true},
		{"своя без филиала", "", f.adA1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := f.query(tc.source)
			query.Set("selected_id", tc.selected.String())
			got := f.fetch(t, query)
			if got.SelectedAllowed == nil || *got.SelectedAllowed != tc.allowed {
				t.Fatalf("selected_allowed=%v, ожидалось %v", got.SelectedAllowed, tc.allowed)
			}
		})
	}
}
