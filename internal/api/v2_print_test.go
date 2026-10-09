package api

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/ui"
)

// Use the production HTTP shell, real Bearer tokens and session cookies: a
// direct call to the renderer would miss both routing and authentication.
func TestAPIV2_PrintDocumentThroughHTTP(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
			{Name: "Secret", Type: metadata.FieldTypeString},
			{Name: "Покупатель", Type: metadata.FieldTypeString, RefEntity: "Клиент"},
		},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Товар", Type: metadata.FieldTypeString},
		}}},
	}
	buyer := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Паспорт", Type: metadata.FieldTypeString},
	}}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{doc, buyer}, nil)
	form := &printform.LayoutForm{Name: "Накладная", Document: doc.Name, Layout: &printform.LayoutTemplate{
		Areas: []*printform.LayoutArea{
			{Name: "Шапка", Rows: []printform.LayoutRow{{Cells: []printform.LayoutCell{
				{Text: "Заказ {{Номер}}"}, {Text: "{{Secret}}"},
				{Text: "{{Покупатель.Наименование}}"}, {Text: "{{Покупатель.Паспорт}}"},
			}}}},
			{Name: "Строка", Rows: []printform.LayoutRow{{Cells: []printform.LayoutCell{{Parameter: "Товар"}}}}},
		},
		Binding: &printform.Binding{Repeat: []printform.RepeatBinding{{
			Area: "Строка", Source: "Товары", Parameters: map[string]string{"Товар": "Товар"},
		}}},
	}}
	h.reg.LoadLayoutForms([]*printform.LayoutForm{form})
	h.reg.LoadDSLPrintForms([]*printform.DSLPrintForm{{Name: "DSLOutsideScope", Document: doc.Name}})
	buyerID, id, hiddenID := uuid.New(), uuid.New(), uuid.New()
	if err := h.store.Upsert(ctx, buyer.Name, buyerID, map[string]any{
		"Наименование": "ООО Ромашка", "Паспорт": "REF-SECRET-481",
	}, buyer); err != nil {
		t.Fatal(err)
	}
	for recordID, owner := range map[uuid.UUID]string{id: "reader", hiddenID: "other"} {
		if err := h.store.Upsert(ctx, doc.Name, recordID, map[string]any{
			"Номер": "З-007", "Owner": owner, "Secret": "DOC-SECRET-391", "Покупатель": buyerID.String(),
		}, doc); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.store.UpsertTablePartRows(ctx, doc.Name, "Товары", id,
		[]map[string]any{{"Товар": "Печатная строка"}}, doc.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(h.store)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	roles := []*auth.Role{{Name: "print-reader", Permissions: auth.Permission{
		Documents: map[string][]string{doc.Name: {"read"}},
		Catalogs:  map[string][]string{buyer.Name: {"read"}},
		RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{
			doc.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
		FieldAccess: auth.FieldAccess{
			Documents: map[string]auth.FieldPolicies{doc.Name: {"Secret": {Read: "mask_all"}}},
			Catalogs:  map[string]auth.FieldPolicies{buyer.Name: {"Паспорт": {Read: "hide"}}},
		},
	}}}
	if err := repo.SyncRoles(ctx, roles); err != nil {
		t.Fatal(err)
	}
	tokens := make(map[string]string)
	sessions := make(map[string]string)
	for _, login := range []string{"reader", "denied", "admin"} {
		u, err := repo.Create(ctx, login, "password123", "", login == "admin")
		if err != nil {
			t.Fatal(err)
		}
		if login == "reader" {
			if err := repo.AssignRole(ctx, u.ID, roles[0].ID); err != nil {
				t.Fatal(err)
			}
		}
		_, tokens[login], err = repo.CreateAPIToken(ctx, login, u.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		sessions[login], err = repo.CreateSession(ctx, u.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
		if err != nil {
			t.Fatal(err)
		}
	}
	srv := newTestServer(h.reg, h.store, h.interp, repo, "127.0.0.1", 0, ui.Config{}, nil)
	t.Cleanup(srv.frontend.(*ui.Server).Close)
	request := func(path, login string, session bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if session {
			req.AddCookie(&http.Cookie{Name: "onebase_session", Value: sessions[login]})
		} else if login != "" {
			token := tokens[login]
			if token == "" {
				token = "invalid-token"
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w
	}
	path := "/api/v2/document/" + url.PathEscape(doc.Name) + "/" + id.String() + "/print/" + url.PathEscape(form.Name)
	uiPath := "/ui/document/" + url.PathEscape(doc.Name) + "/" + id.String() + "/print/" + url.PathEscape(form.Name)

	t.Run("HTML matches UI data and masks", func(t *testing.T) {
		for _, login := range []string{"reader", "admin"} {
			api := request(path, login, false)
			browser := request(uiPath, login, true)
			if api.Code != http.StatusOK || browser.Code != http.StatusOK {
				t.Fatalf("%s: API=%d %s UI=%d %s", login, api.Code, api.Body.String(), browser.Code, browser.Body.String())
			}
			if api.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatalf("content type = %q", api.Header().Get("Content-Type"))
			}
			// UI adds navigation buttons; the rendered sheet must be identical.
			sheet := regexp.MustCompile(`(?s)<table>.*</table>`)
			if got, want := sheet.Find(api.Body.Bytes()), sheet.Find(browser.Body.Bytes()); len(got) == 0 || !bytes.Equal(got, want) {
				t.Fatalf("REST sheet differs from UI: %s\n%s", got, want)
			}
			for _, want := range []string{"Заказ З-007", "ООО Ромашка", "Печатная строка"} {
				if !strings.Contains(api.Body.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, secret := range []string{"DOC-SECRET-391", "REF-SECRET-481"} {
				if strings.Contains(api.Body.String(), secret) != (login == "admin") {
					t.Errorf("%s: incorrect visibility of %q", login, secret)
				}
			}
		}
		if w := request(path, "reader", true); w.Code != http.StatusOK {
			t.Fatalf("REST session: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("PDF matches UI pages", func(t *testing.T) {
		for _, login := range []string{"reader", "admin"} {
			api, browser := request(path+"/pdf", login, false), request(uiPath+"/pdf", login, true)
			if api.Code != http.StatusOK || browser.Code != http.StatusOK {
				t.Fatalf("API=%d %s UI=%d %s", api.Code, api.Body.String(), browser.Code, browser.Body.String())
			}
			if api.Header().Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(api.Body.Bytes(), []byte("%PDF-")) {
				t.Fatal("expected a PDF download")
			}
			_, params, err := mime.ParseMediaType(api.Header().Get("Content-Disposition"))
			if err != nil || params["filename"] != "Накладная_З-007.pdf" {
				t.Fatalf("download filename: %v %v", params, err)
			}
			if got, want := printPDFPages(t, api.Body.Bytes()), printPDFPages(t, browser.Body.Bytes()); !bytes.Equal(got, want) {
				t.Fatal("REST PDF pages differ from UI PDF pages")
			}
		}
	})
	t.Run("authentication permissions and invalid routes", func(t *testing.T) {
		for _, suffix := range []string{"", "/pdf"} {
			for _, tc := range []struct {
				name, path, login string
				status            int
			}{
				{"no token", path, "", http.StatusUnauthorized},
				{"invalid token", path, "invalid", http.StatusUnauthorized},
				{"no read permission", path, "denied", http.StatusForbidden},
				{"row denied", strings.Replace(path, id.String(), hiddenID.String(), 1), "reader", http.StatusForbidden},
				{"invalid id", strings.Replace(path, id.String(), "invalid", 1), "admin", http.StatusBadRequest},
				{"missing row", strings.Replace(path, id.String(), uuid.NewString(), 1), "admin", http.StatusNotFound},
				{"missing entity", strings.Replace(path, url.PathEscape(doc.Name), "Unknown", 1), "admin", http.StatusNotFound},
				{"wrong entity kind", strings.Replace(path, url.PathEscape(doc.Name), url.PathEscape(buyer.Name), 1), "admin", http.StatusNotFound},
				{"missing form", strings.Replace(path, url.PathEscape(form.Name), "Unknown", 1), "admin", http.StatusNotFound},
				{"DSL excluded", strings.Replace(path, url.PathEscape(form.Name), "DSLOutsideScope", 1), "admin", http.StatusNotFound},
				{"module excluded", strings.Replace(path, url.PathEscape(form.Name), "_module", 1), "admin", http.StatusNotFound},
			} {
				t.Run(tc.name+suffix, func(t *testing.T) {
					w := request(tc.path+suffix, tc.login, false)
					if w.Code != tc.status {
						t.Fatalf("status=%d want=%d: %s", w.Code, tc.status, w.Body.String())
					}
					if !json.Valid(w.Body.Bytes()) {
						t.Fatal("REST errors must be JSON")
					}
				})
			}
		}
	})
	t.Run("OpenAPI describes non-JSON responses", func(t *testing.T) {
		w := request("/api/v2/openapi.json", "reader", false)
		var spec struct {
			Paths map[string]struct {
				Get struct {
					Responses map[string]struct{ Content map[string]any }
				}
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
			t.Fatal(err)
		}
		for suffix, content := range map[string]string{"": "text/html", "/pdf": "application/pdf"} {
			response := spec.Paths["/api/v2/document/{name}/{id}/print/{form}"+suffix].Get.Responses["200"]
			if _, ok := response.Content[content]; !ok {
				t.Errorf("OpenAPI missing %s response", content)
			}
		}
	})
}

// Compare page drawing instructions, excluding PDF timestamps, object ordering
// and font subset objects whose serialization can vary between requests.
func printPDFPages(t *testing.T, data []byte) []byte {
	t.Helper()
	var pages []byte
	for _, stream := range regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindAllSubmatch(data, -1) {
		r, err := zlib.NewReader(bytes.NewReader(stream[1]))
		if err != nil {
			continue
		}
		plain, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(plain, []byte("BT ")) {
			pages = append(pages, plain...)
		}
	}
	if len(pages) == 0 {
		t.Fatal("no PDF page content found")
	}
	return pages
}
