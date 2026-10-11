package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Exercise the mounted UI route: masking the result must not leave a filter
// oracle, or reveal protected reference values through the filter options.
func TestUI_InfoRegList_ProtectedDimensionFilter(t *testing.T) {
	for _, strategy := range []string{"hide", "mask_all", "mask_tail"} {
		t.Run(strategy, func(t *testing.T) {
			goods := &metadata.Entity{Name: "Goods", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
			ir := &metadata.InfoRegister{
				Name: "Secrets",
				Dimensions: []metadata.Field{
					{Name: "Secret", Type: metadata.FieldTypeString},
					{Name: "Product", Type: "reference:Goods", RefEntity: goods.Name},
					{Name: "Public", Type: metadata.FieldTypeString},
				},
				Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
			}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{goods})
			if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
				t.Fatal(err)
			}
			s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{goods}, InfoRegs: []*metadata.InfoRegister{ir}})
			productID := uuid.New()
			if err := s.store.Upsert(ctx, goods.Name, productID, map[string]any{"Name": "PROTECTED-REFERENCE-LABEL"}, goods); err != nil {
				t.Fatal(err)
			}
			for _, row := range []struct{ secret, public, value string }{
				{"secret-one", "A", "ROW-ONE"}, {"secret-two", "B", "ROW-TWO"},
			} {
				if err := s.store.InfoRegSet(ctx, ir, map[string]any{"Secret": row.secret, "Product": productID.String(), "Public": row.public}, map[string]any{"Value": row.value}, nil); err != nil {
					t.Fatal(err)
				}
			}
			user := &auth.User{ID: "reader", Login: "reader", Roles: []*auth.Role{{Name: "reader", Permissions: auth.Permission{
				Catalogs: map[string][]string{goods.Name: {"read"}},
				InfoRegs: map[string][]string{ir.Name: {"read"}},
				FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{ir.Name: {
					"secret": {Read: strategy, Keep: 2}, "product": {Read: strategy, Keep: 2},
				}}},
			}}}}
			router := chi.NewRouter()
			s.Mount(router)
			request := func(query url.Values) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "/ui/inforeg/secrets?"+query.Encode(), nil)
				req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			// Both matching and nonmatching probes, including a protected UUID and a
			// repeated parameter, must fail rather than reveal whether a row exists.
			for _, query := range []url.Values{
				{"flt_Secret": {"secret-one"}}, {"flt_Secret": {"missing"}},
				{"flt_Product": {productID.String()}}, {"flt_Product": {"invalid-uuid"}},
				{"flt_Secret": {"", "secret-one"}},
				{"flt_Secret": {"secret-one"}, "flt_Public": {"A"}},
			} {
				rec := request(query)
				if rec.Code != http.StatusForbidden {
					t.Errorf("query %v: status=%d, want 403", query, rec.Code)
				}
			}
			for _, query := range []url.Values{nil, {"flt_Secret": {" "}}, {"flt_Public": {"A"}}, {"flt_Public": {"missing"}}} {
				rec := request(query)
				if rec.Code != http.StatusOK {
					t.Fatalf("query %v: status=%d body=%s", query, rec.Code, rec.Body.String())
				}
				body := rec.Body.String()
				for _, forbidden := range []string{`name="flt_Secret"`, `name="flt_Product"`, "PROTECTED-REFERENCE-LABEL", productID.String()} {
					if strings.Contains(body, forbidden) {
						t.Errorf("query %v: protected filter data %q leaked", query, forbidden)
					}
				}
				if !strings.Contains(body, `name="flt_Public"`) {
					t.Error("visible filter is missing")
				}
				one, two := strings.Contains(body, "ROW-ONE"), strings.Contains(body, "ROW-TWO")
				switch query.Get("flt_Public") {
				case "A":
					if !one || two {
						t.Error("visible filter did not select only row A")
					}
				case "missing":
					if one || two {
						t.Error("nonmatching visible filter returned rows")
					}
				default:
					if !one || !two {
						t.Error("empty filter did not preserve both rows")
					}
				}
			}
			// A reader without field protection retains the ordinary dimension filters.
			protectedUser := user
			user = &auth.User{ID: "plain", Login: "plain", Roles: []*auth.Role{{Name: "plain", Permissions: auth.Permission{
				Catalogs: map[string][]string{goods.Name: {"read"}},
				InfoRegs: map[string][]string{ir.Name: {"read"}},
			}}}}
			plain := request(url.Values{"flt_Secret": {"secret-one"}, "flt_Product": {productID.String()}})
			if plain.Code != http.StatusOK || !strings.Contains(plain.Body.String(), "ROW-ONE") || strings.Contains(plain.Body.String(), "ROW-TWO") {
				t.Errorf("unprotected reader: status=%d, expected only row A", plain.Code)
			}
			if !strings.Contains(plain.Body.String(), `name="flt_Secret"`) || !strings.Contains(plain.Body.String(), `name="flt_Product"`) {
				t.Error("unprotected dimension filters are missing")
			}
			user = protectedUser

			// Denial must happen before storage, even when the database is unavailable.
			s.store.Close()
			rec := request(url.Values{"flt_Secret": {"secret-one"}})
			if rec.Code != http.StatusForbidden {
				t.Errorf("closed database: status=%d, want 403", rec.Code)
			}
		})
	}
}
