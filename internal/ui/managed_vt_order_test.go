package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedValueTableColumnOrderInFormResponse(t *testing.T) {
	table := &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "Results", DataPath: "Форма.Results", Children: []*metadata.FormElement{
		{Kind: metadata.FormElementColumn, Name: "ColumnB", DataPath: "B", ReadOnly: true},
		{Kind: metadata.FormElementColumn, Name: "ColumnA", DataPath: "A", ReadOnly: true},
	}}
	entity := layoutTestEntity(table)
	entity.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "Results", TypeRef: "ValueTable", Columns: []*metadata.FormAttributeColumn{{Name: "A", TypeRef: "string"}, {Name: "B", TypeRef: "number"}, {Name: "ID", TypeRef: "string"}}}}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+entity.Name+"/new", nil, map[string]string{"entity": entity.Name})
	rec := httptest.NewRecorder()
	srv.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if b, a := strings.Index(body, "<th>B</th>"), strings.Index(body, "<th>A</th>"); b < 0 || a < b {
		t.Fatal("visible column order differs from form definition")
	}
	if !strings.Contains(body, `data-vt-fields="B|number,A|string,ID|string"`) {
		t.Fatal("repaint metadata does not preserve the rendered column order")
	}
}
