package ui

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// The picker must not use a record or field that the caller cannot read as
// an oracle, even when the UUID comes directly from a forged browser request.
func TestRefOptionsDeepChoiceSourceAccess(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		denyRead, denyRow, masked, disclose bool
		total                               int
	}{
		{name: "readable", total: 53},
		{name: "object denied", denyRead: true},
		{name: "row denied", denyRow: true},
		{name: "field masked", masked: true},
		{name: "field disclosable", masked: true, disclose: true, total: 53},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChoiceHTTPFixture(t)
			element := f.owner.Forms[0].Elements[1]
			element.ChoiceFilter = []metadata.FormChoiceCondition{{Field: "Аудитория", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.Наименование"}}
			if err := f.server.store.Upsert(t.Context(), f.direction.Name, f.rootA, map[string]any{"Наименование": "anna", "is_folder": true}, f.direction); err != nil {
				t.Fatal(err)
			}
			permission := &f.user.Roles[0].Permissions
			if tc.denyRead {
				delete(permission.Catalogs, f.direction.Name)
			}
			if tc.denyRow {
				permission.RowAccess.Catalogs[f.direction.Name] = auth.RowPolicies{"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "bob"}}}
			}
			if tc.masked {
				permission.FieldAccess.Catalogs = map[string]auth.FieldPolicies{f.direction.Name: {"Наименование": {Read: "mask_all"}}}
			}
			if tc.disclose {
				permission.Catalogs[f.direction.Name] = []string{"read", "disclose"}
			}
			q := f.contextQuery(f.rootA)
			raw, err := json.Marshal(map[string]string{"Объект.Направление.Наименование": f.rootA.String()})
			if err != nil {
				t.Fatal(err)
			}
			q.Set("sources", string(raw))
			q.Set("limit", "100")
			rec := f.serveRefOptions(t, f.target, q)
			if rec.Code != http.StatusOK {
				t.Fatalf("picker: %d %s", rec.Code, rec.Body.String())
			}
			got := decodeChoiceHTTP(t, rec)
			if got.Total != tc.total || len(got.Items) != tc.total {
				t.Fatalf("source access leaked or lost matches: total=%d items=%d, want %d", got.Total, len(got.Items), tc.total)
			}
		})
	}
}
