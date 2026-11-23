package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// GET is native formEdit, including authorization, selected form, template and
// choice_filter. POST sends the selected DOM option, not a fixture-supplied UUID.
func TestCurrentReferenceDisplayHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"no-catalog-read", "outside-row-policy", "outside-choice-filter", "filtered-no-read", "readonly-no-read", "masked-label", "hidden-label", "pii-label", "hidden-parent-reference", "parent-denied", "ordinary-choice", "intentional-clear", "other-uuid", "genuine-edit"} {
			t.Run(mode, func(t *testing.T) {
				ctx := context.Background()
				target := &metadata.Entity{Name: "BranchDisplay", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Secret", Type: metadata.FieldTypeString}, {Name: "Active", Type: metadata.FieldTypeBool}}}
				if mode == "pii-label" {
					target.Fields[0].PII = true
				}
				doc := &metadata.Entity{Name: "CurrentReferenceDoc", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Branch", Type: "reference:BranchDisplay", RefEntity: target.Name}}}
				dropdown := false
				field := &metadata.FormElement{Name: "BranchField", ID: "branch", Kind: metadata.FormElementField, DataPath: "Объект.Branch", Choice: true, ChoiceDropdown: &dropdown, ReadOnlyWhen: "Locked"}
				if mode == "outside-choice-filter" || mode == "filtered-no-read" {
					yes := true
					field.ChoiceFilter = []metadata.FormChoiceCondition{{Field: "Active", Op: metadata.FormChoiceOpEqual, Value: &yes}}
				}
				if mode == "readonly-no-read" {
					field.ReadOnly = true
				}
				form := &metadata.FormModule{Name: "NativeForm", Kind: "object", EntityName: doc.Name, LayoutKind: metadata.FormLayoutManaged,
					Attributes: []*metadata.FormAttribute{{Name: "Locked", TypeRef: "bool", Save: false}},
					Elements:   []*metadata.FormElement{field, {Name: "NameField", Kind: metadata.FormElementField, DataPath: "Объект.Name"}, {Name: "Pages", Kind: metadata.FormElementPages, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnPageChange: "Changed"}, Children: []*metadata.FormElement{{Name: "Calls", Kind: metadata.FormElementPage}}}}}
				extra := ""
				if mode == "readonly-no-read" {
					extra = "Объект.Locked = Истина;"
				}
				form.ProgramAST = mustParse(t, "Процедура Changed(Элемент)\n"+extra+"\nКонецПроцедуры")
				doc.Forms = []*metadata.FormModule{form}
				if err := db.Migrate(ctx, []*metadata.Entity{target, doc}); err != nil {
					t.Fatal(err)
				}
				selected, other, id := uuid.New(), uuid.New(), uuid.New()
				for _, bid := range []uuid.UUID{selected, other} {
					if err := db.Upsert(ctx, target.Name, bid, map[string]any{"Name": "Branch visible label", "Secret": "NEVER render this field", "Active": false}, target); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Name": "own", "Branch": selected.String()}, doc); err != nil {
					t.Fatal(err)
				}
				perm := auth.Permission{Documents: map[string][]string{doc.Name: {"read", "write"}}}
				if mode == "parent-denied" {
					perm.Documents = nil
				}
				if mode == "outside-row-policy" || mode == "outside-choice-filter" || mode == "ordinary-choice" || mode == "intentional-clear" || mode == "genuine-edit" {
					perm.Catalogs = map[string][]string{target.Name: {"read"}}
				}
				if mode == "outside-row-policy" {
					perm.RowAccess.Catalogs = map[string]auth.RowPolicies{target.Name: {"read": {Field: "Active", Op: "eq", Value: auth.RowValue{Literal: true}}}}
				}
				if mode == "masked-label" {
					perm.FieldAccess.Catalogs = map[string]auth.FieldPolicies{target.Name: {"Name": {Read: "mask_all"}}}
				}
				if mode == "hidden-label" {
					perm.FieldAccess.Catalogs = map[string]auth.FieldPolicies{target.Name: {"Name": {Read: "hide"}, "Secret": {Read: "hide"}}}
				}
				if mode == "hidden-parent-reference" {
					perm.FieldAccess.Documents = map[string]auth.FieldPolicies{doc.Name: {"Branch": {Read: "hide"}}}
				}
				actor := &auth.User{ID: "own-display-user", Login: "own-display-user", Roles: []*auth.Role{{Name: "OTK", Permissions: perm}}}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target, doc}})
				interp := interpreter.New()
				interp.LookupProc = reg.GetModuleProc
				s := &Server{reg: reg, store: db, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
				s.entitySvc = s.newEntityService(nil)
				router := chi.NewRouter()
				s.Mount(router)
				request := func(method, path string, body url.Values) *httptest.ResponseRecorder {
					var payload string
					if body != nil {
						payload = body.Encode()
					}
					r := httptest.NewRequest(method, path, strings.NewReader(payload))
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r = r.WithContext(auth.ContextWithUser(r.Context(), actor))
					w := httptest.NewRecorder()
					router.ServeHTTP(w, r)
					return w
				}
				path := "/ui/document/" + doc.Name + "/" + id.String()
				w := request("GET", path, nil)
				if mode == "parent-denied" {
					if w.Code != 403 || strings.Contains(w.Body.String(), "Branch visible label") {
						t.Fatal("parent access bypassed")
					}
					return
				}
				if w.Code != 200 {
					t.Fatalf("GET HTTP%d", w.Code)
				}
				dom, err := html.Parse(strings.NewReader(w.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				selectNode := findSelectByName(dom, "Branch")
				if selectNode == nil {
					t.Fatal("reference select missing")
				}
				value, label := "", ""
				for n := selectNode.FirstChild; n != nil; n = n.NextSibling {
					if n.Type != html.ElementNode || n.Data != "option" {
						continue
					}
					if _, ok := htmlAttribute(n, "selected"); ok {
						value, _ = htmlAttribute(n, "value")
						if n.FirstChild != nil {
							label = n.FirstChild.Data
						}
					}
				}
				if mode == "hidden-parent-reference" {
					if value != "" || strings.Contains(w.Body.String(), selected.String()) || strings.Contains(w.Body.String(), "Branch visible label") {
						t.Fatal("protected parent reference disclosed")
					}
					return
				}
				if value != selected.String() {
					t.Fatal("current reference lost in native SELECT")
				}
				if strings.Contains(w.Body.String(), "NEVER render this field") {
					t.Fatal("non-label data leaked")
				}
				if mode == "masked-label" || mode == "hidden-label" || mode == "pii-label" {
					if strings.Contains(label, "Branch visible label") {
						t.Fatal("label masking bypassed")
					}
				} else if label != "Branch visible label" {
					t.Fatal("current label missing")
				}
				// The display exception must never turn into a catalog/search read grant.
				if mode == "no-catalog-read" {
					pick := request("GET", "/ui/catalog/"+target.Name, nil)
					if pick.Code != 403 {
						t.Fatal("catalog access expanded")
					}
				}
				body := url.Values{"_kind": {"object"}, "_form": {form.Name}, "_id": {id.String()}, "_version": {"1"}, "_element": {"Pages"}, "_event": {string(metadata.FormEventOnPageChange)}, "_page": {"Calls"}, "Name": {"own"}, "Branch": {value}}
				wantDirty := false
				if mode == "intentional-clear" {
					body.Set("Branch", "")
					wantDirty = true
				}
				if mode == "genuine-edit" {
					body.Set("Name", "unsaved edit")
					wantDirty = true
				}
				if mode == "other-uuid" {
					body.Set("Branch", other.String())
					wantDirty = true
				}
				event := request("POST", "/ui/document/"+doc.Name+"/form-event", body)
				if event.Code != 200 {
					t.Fatalf("event HTTP%d", event.Code)
				}
				resp := decodeFormEventResponse(t, event.Body.Bytes())
				if !resp.OK || resp.Dirty == nil || *resp.Dirty != wantDirty {
					t.Fatalf("dirty mismatch want=%t, error=%s", wantDirty, resp.Error)
				}
				// Native ordinary Save (no forged event, preserved CAS) retains the DOM ID.
				if !wantDirty {
					body.Del("_element")
					body.Del("_event")
					body.Set("_action", "save")
					save := request("POST", path, body)
					if save.Code != 302 && save.Code != 303 {
						t.Fatalf("save HTTP%d", save.Code)
					}
				}
				persisted, err := db.GetByID(ctx, doc.Name, id, doc)
				if err != nil {
					t.Fatal(err)
				}
				if refValueString(persisted["Branch"]) != selected.String() {
					t.Fatal("reference erased in storage")
				}
			})
		}
	})
}
