package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Exercise the mounted initial GET, not buildReportParams or a policy helper.
// Changing only protected data must not change labels, page membership/order,
// or the selected UUID appended beyond the initial page.
func TestReportParamReferencePrivacyHTTP(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "ParamSensitive", Kind: metadata.KindCatalog, Presentation: []string{"Label"}, OrderBy: []string{"Label"}, Fields: []metadata.Field{
			{Name: "Label", Type: metadata.FieldTypeString},
			{Name: "Phone", Type: metadata.FieldTypeString, PII: true},
			{Name: "Address", Type: metadata.FieldTypeString, ReportPII: true},
			{Name: "Allowed", Type: metadata.FieldTypeBool},
		}}
		public := &metadata.Entity{Name: "ParamPublic", Kind: metadata.KindCatalog, Presentation: []string{"Label"}, OrderBy: []string{"Label"}, Fields: []metadata.Field{{Name: "Label", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{entity, public}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		idAt := func(i int) uuid.UUID {
			return uuid.MustParse(fmt.Sprintf("10000000-0000-0000-0000-%012d", i))
		}
		selected := idAt(refPickerDefaultLimit + 2).String()
		for i, label := range []string{"SAFE_Z", "SAFE_A"} {
			if err := db.Upsert(ctx, public.Name, idAt(i+1), map[string]any{"Label": label}, public); err != nil {
				t.Fatal(err)
			}
		}
		rep := &reportpkg.Report{Name: "ParamPrivacy", Query: "SELECT COUNT(*) AS N FROM Catalog.ParamSensitive", Params: []reportpkg.Param{
			{Name: "Target", Type: "reference:ParamSensitive", Default: selected},
			{Name: "PublicTarget", Type: "reference:ParamPublic"},
		}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities, Reports: []*reportpkg.Report{rep}})
		s := &Server{store: db, reg: reg}
		router := chi.NewRouter()
		s.Mount(router)
		write := func(changed bool) {
			t.Helper()
			for i := 1; i <= refPickerDefaultLimit+2; i++ {
				label, phone, address := fmt.Sprintf("BASE_%03d", i), "BASE_PHONE", "BASE_ADDRESS"
				if changed {
					// Reverse label order as well as replacing every hidden string.
					label = fmt.Sprintf("PRIVATE_PHONE_HOUSE_FLAT_%03d", refPickerDefaultLimit+3-i)
					phone, address = "PRIVATE_PHONE", "PRIVATE_HOUSE_FLAT"
				}
				if err := db.Upsert(ctx, entity.Name, idAt(i), map[string]any{"Label": label, "Phone": phone, "Address": address, "Allowed": i == 2}, entity); err != nil {
					t.Fatal(err)
				}
			}
		}
		request := func(path string, user *auth.User) string {
			t.Helper()
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r = r.WithContext(auth.ContextWithUser(r.Context(), user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP %d for %s", w.Code, path)
			}
			return w.Body.String()
		}
		options := func(body string) []string {
			t.Helper()
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			selectNode := findSelectByName(doc, "Target")
			if selectNode == nil {
				t.Fatal("reference parameter missing")
			}
			var result []string
			for node := selectNode.FirstChild; node != nil; node = node.NextSibling {
				if node.Type != html.ElementNode || node.Data != "option" {
					continue
				}
				id, _ := htmlAttribute(node, "value")
				if id == "" {
					continue
				}
				label := ""
				if node.FirstChild != nil {
					label = node.FirstChild.Data
				}
				if label != id {
					t.Fatal("protected target did not retain its technical title")
				}
				result = append(result, id)
			}
			return result
		}
		otk := &auth.User{Login: "otk", Roles: []*auth.Role{{Permissions: auth.Permission{
			Reports:  map[string][]string{rep.Name: {"run"}},
			Catalogs: map[string][]string{entity.Name: {"read", "disclose"}, public.Name: {"read"}},
		}}}}
		for _, user := range []*auth.User{{IsAdmin: true}, otk} {
			write(false)
			before := options(request("/ui/report/ParamPrivacy", user))
			if len(before) != refPickerDefaultLimit+1 || before[len(before)-1] != selected {
				t.Fatal("bounded page or selected UUID beyond the page lost")
			}
			write(true)
			body := request("/ui/report/ParamPrivacy", user)
			if strings.Contains(body, "PRIVATE_") || strings.Contains(body, "BASE_") {
				t.Fatal("protected reference data reached the initial report form")
			}
			if a, z := strings.Index(body, ">SAFE_A</option>"), strings.Index(body, ">SAFE_Z</option>"); a < 0 || z <= a {
				t.Fatal("public reference title or metadata order changed")
			}
			if after := options(body); !reflect.DeepEqual(before, after) {
				t.Fatal("hidden data changed report reference membership/order")
			}
			// The ordinary picker remains governed by the user's existing rights.
			if body := request("/ui/_ref-options/ParamSensitive", user); !strings.Contains(body, "PRIVATE_PHONE_HOUSE_FLAT_") {
				t.Fatal("ordinary privileged reference labels changed")
			}
		}
		noRead := &auth.User{Login: "report-only", Roles: []*auth.Role{{Permissions: auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}}}}}
		if got := options(request("/ui/report/ParamPrivacy", noRead)); len(got) != 0 {
			t.Fatal("report context granted target read access")
		}
		restricted := &auth.User{Login: "restricted", Roles: []*auth.Role{{Permissions: auth.Permission{
			Reports:  map[string][]string{rep.Name: {"run"}},
			Catalogs: map[string][]string{entity.Name: {"read"}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{entity.Name: {
				"read": {Field: "Allowed", Op: "eq", Value: auth.RowValue{Literal: true, LiteralSet: true}},
			}}},
		}}}}
		if got := options(request("/ui/report/ParamPrivacy", restricted)); !reflect.DeepEqual(got, []string{idAt(2).String()}) {
			t.Fatal("list or selected-value append bypassed RLS")
		}
	})
}
