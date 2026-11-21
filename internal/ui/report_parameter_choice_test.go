package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReportParameterChoiceHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		branches := &metadata.Entity{Name: "ReportBranches", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		directions := &metadata.Entity{Name: "ReportDirections", Kind: metadata.KindCatalog, OrderBy: []string{"Priority", "Name"}, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Branch", Type: metadata.FieldType("reference:ReportBranches"), RefEntity: branches.Name}, {Name: "Priority", Type: metadata.FieldTypeNumber}}}
		entities := []*metadata.Entity{branches, directions}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		b1, b2 := uuid.New(), uuid.New()
		for id, name := range map[uuid.UUID]string{b1: "Москва", b2: "Питер"} {
			if err := db.Upsert(ctx, branches.Name, id, map[string]any{"Name": name}, branches); err != nil {
				t.Fatal(err)
			}
		}
		d1, d2, d3 := uuid.New(), uuid.New(), uuid.New()
		for _, item := range []struct {
			id       uuid.UUID
			name     string
			branch   uuid.UUID
			priority int
		}{{d1, "ЯПервый", b1, 1}, {d2, "АВторой", b1, 2}, {d3, "Другой", b2, 1}} {
			if err := db.Upsert(ctx, directions.Name, item.id, map[string]any{"Name": item.name, "Branch": item.branch, "Priority": item.priority}, directions); err != nil {
				t.Fatal(err)
			}
		}
		rep := &reportpkg.Report{Name: "ReportChoice", Params: []reportpkg.Param{{Name: "Branch", Type: "reference:ReportBranches"}, {Name: "Direction", Type: "reference:ReportDirections", ChoiceFilter: []reportpkg.ParamChoiceFilter{{Field: "Branch", Param: "Branch"}}, ChoiceLabelSuffix: "Branch"}}, Query: `ВЫБРАТЬ Name ИЗ Справочник.ReportDirections ГДЕ (&Branch ЕСТЬ NULL ИЛИ Branch = &Branch) И (&Direction ЕСТЬ NULL ИЛИ Ссылка = &Direction)`}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities, Reports: []*reportpkg.Report{rep}})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter()}
		request := func(branch, selected, search string, offset int, user *auth.User) *httptest.ResponseRecorder {
			sources, _ := json.Marshal(map[string]string{"Branch": branch})
			q := url.Values{"form_kind": {"report"}, "form_entity": {rep.Name}, "form": {"params"}, "element": {"Direction"}, "sources": {string(sources)}, "selected_id": {selected}, "q": {search}, "limit": {"1"}}
			if offset > 0 {
				q.Set("offset", "1")
			}
			return serveRefOptions(t, s, directions.Name, q.Encode(), user)
		}
		decode := func(w *httptest.ResponseRecorder) map[string]any {
			t.Helper()
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			return got
		}
		got := decode(request(b1.String(), d3.String(), "", 0, nil))
		if got["total"] != float64(2) || got["selected_allowed"] != false {
			t.Fatalf("filtered response: %#v", got)
		}
		item := got["items"].([]any)[0].(map[string]any)
		if item["id"] != d1.String() || item["_label"] != "ЯПервый (Москва)" || len(item) != 2 {
			t.Fatalf("order/label/public fields: %#v", item)
		}
		got = decode(request(b1.String(), d2.String(), "", 1, nil))
		if got["selected_allowed"] != true || got["items"].([]any)[0].(map[string]any)["id"] != d2.String() {
			t.Fatalf("pagination: %#v", got)
		}
		got = decode(request("", "", "", 0, nil))
		if got["total"] != float64(3) {
			t.Fatalf("empty branch must include all: %#v", got)
		}
		got = decode(request(b2.String(), "", "Другой", 0, nil))
		if got["total"] != float64(1) || got["items"].([]any)[0].(map[string]any)["_label"] != "Другой (Питер)" {
			t.Fatalf("search/branch: %#v", got)
		}
		if w := request("bad", "", "", 0, nil); w.Code != 400 {
			t.Fatalf("invalid source accepted: %d", w.Code)
		}
		denied := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{directions.Name: {"read"}, branches.Name: {"read"}}}}}}
		if w := request(b1.String(), "", "", 0, denied); w.Code != 400 {
			t.Fatalf("report permission bypass: %d", w.Code)
		}
		// Initial report form uses exactly the same labels and context as the picker.
		w := httptest.NewRecorder()
		s.reportForm(w, reqWithChi("GET", "/ui/report/"+rep.Name, nil, map[string]string{"name": rep.Name}))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ЯПервый (Москва)") || !strings.Contains(w.Body.String(), "data-ref-choice-context=") {
			t.Fatalf("initial form: %d %s", w.Code, w.Body.String())
		}
		bad := url.Values{"Branch": {b1.String()}, "Direction": {d3.String()}}
		w = httptest.NewRecorder()
		s.reportRun(w, reqWithChi("POST", "/ui/report/"+rep.Name, bad, map[string]string{"name": rep.Name}))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("incompatible pair accepted: %d", w.Code)
		}
		for _, suffix := range []string{"excel", "pdf"} {
			w = httptest.NewRecorder()
			r := reqWithChi("GET", "/ui/report/"+rep.Name+"/"+suffix+"?"+bad.Encode(), nil, map[string]string{"name": rep.Name})
			if suffix == "excel" {
				s.reportExcel(w, r)
			} else {
				s.reportPDF(w, r)
			}
			if w.Code != 400 {
				t.Fatalf("%s incompatible pair: %d %s", suffix, w.Code, w.Body.String())
			}
		}
	})
}
