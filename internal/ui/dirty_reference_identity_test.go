package ui

import (
	"context"
	"net/http"
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
)

type dirtyReferenceValue struct{ id string }

func (v *dirtyReferenceValue) GetRefUUID() string {
	if v == nil {
		return ""
	}
	return v.id
}
func (v dirtyReferenceValue) String() string { return "same presentation" }

func TestDirtyReferenceIdentityValues(t *testing.T) {
	id := uuid.MustParse("aabbccdd-1234-4321-aaaa-abcdef012345")
	refField := metadata.Field{Name: "Branch", Type: "reference:IdentityBranches", RefEntity: "IdentityBranches"}
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"uuid-string", id.String(), id.String()},
		{"uuid-string-uppercase", strings.ToUpper(id.String()), id.String()},
		{"ref-pointer-uppercase", &interpreter.Ref{UUID: strings.ToUpper(id.String()), Name: "same presentation"}, id.String()},
		{"ref-pointer-braces", &interpreter.Ref{UUID: "{" + id.String() + "}"}, id.String()},
		{"ref-value", interpreter.Ref{UUID: id.String(), Name: "same presentation"}, id.String()},
		{"ref-value-uppercase", interpreter.Ref{UUID: strings.ToUpper(id.String())}, id.String()},
		{"object-pointer", &runtime.Object{ID: id}, id.String()},
		{"object-value", runtime.Object{ID: id}, id.String()},
		{"pointer-method-value-wrapper", dirtyReferenceValue{id: id.String()}, id.String()},
		{"uuid-value", id, id.String()},
		{"uuid-raw-array", [16]byte(id), id.String()},
		{"nil", nil, ""},
		{"nil-ref", (*interpreter.Ref)(nil), ""},
		{"nil-object", (*runtime.Object)(nil), ""},
		{"empty-string", "", ""},
		{"zero-uuid", uuid.Nil, ""},
		{"empty-ref-value", interpreter.Ref{}, ""},
		{"zero-ref-value", interpreter.Ref{UUID: uuid.Nil.String()}, ""},
		{"invalid-string-not-empty", "invalid-identity", "invalid-identity"},
		{"invalid-ref-not-empty", &interpreter.Ref{UUID: "invalid-identity", Name: "same presentation"}, "invalid-identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tpCellNorm(refField, tc.v); got != tc.want {
				t.Fatalf("reference canonicalization mismatch for %T", tc.v)
			}
		})
	}
	other := uuid.MustParse("bbccddee-1234-4321-aaaa-abcdef012345")
	for _, pair := range [][2]any{
		{&interpreter.Ref{UUID: id.String(), Name: "same"}, &interpreter.Ref{UUID: other.String(), Name: "same"}},
		{dirtyReferenceValue{id: id.String()}, dirtyReferenceValue{id: other.String()}},
		{nil, id.String()}, {id.String(), nil},
	} {
		if tpCellNorm(refField, pair[0]) == tpCellNorm(refField, pair[1]) {
			t.Fatal("genuine reference change was hidden")
		}
	}
	virtual := refField
	virtual.RefEntity = ""
	if tpCellNorm(virtual, &interpreter.Ref{UUID: strings.ToUpper(id.String())}) != id.String() {
		t.Fatal("typed virtual reference not canonical")
	}
	plain := metadata.Field{Type: metadata.FieldTypeString}
	if tpCellNorm(plain, strings.ToUpper(id.String())) == tpCellNorm(plain, id.String()) {
		t.Fatal("ordinary strings were canonicalized as references")
	}
}

func TestDirtyReferenceIdentityTableCells(t *testing.T) {
	id := uuid.MustParse("aabbccdd-1234-4321-aaaa-abcdef012345")
	tp := metadata.TablePart{Name: "Lines", Fields: []metadata.Field{{Name: "Branch", Type: "reference:IdentityBranches", RefEntity: "IdentityBranches"}}}
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			stored := []map[string]any{{"Branch": id.String()}}
			same := []map[string]any{{"Branch": interpreter.Ref{UUID: strings.ToUpper(id.String()), Name: "renamed label"}}}
			changed := []map[string]any{{"Branch": &interpreter.Ref{UUID: uuid.NewString(), Name: "renamed label"}}}
			if !tpRowsEqualStored(same, stored, tp, dialect) {
				t.Fatal("same reference marked changed")
			}
			if tpRowsEqualStored(changed, stored, tp, dialect) {
				t.Fatal("real TP reference edit ignored")
			}
		})
	}
}

// Native PageChange does not assign Branch. It only prepares save:false view
// state, just as opening the calls tab does. Both catalog targets really exist.
func TestDirtyReferenceIdentityHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		branch := &metadata.Entity{Name: "IdentityBranches", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		entity := &metadata.Entity{Name: "DirtyReferenceDocument", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Branch", Type: "reference:IdentityBranches", RefEntity: branch.Name}, {Name: "Flag", Type: metadata.FieldTypeBool},
		}, TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}}}
		if err := db.Migrate(ctx, []*metadata.Entity{branch, entity}); err != nil {
			t.Fatal(err)
		}
		a := uuid.MustParse("aabbccdd-1234-4321-aaaa-abcdef012345")
		b := uuid.MustParse("bbccddee-1234-4321-aaaa-abcdef012345")
		for _, id := range []uuid.UUID{a, b} {
			if err := db.Upsert(ctx, branch.Name, id, map[string]any{"Name": "same target label"}, branch); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			name, encoding, extra                                   string
			storedEmpty, postEmpty, other, editName, editFlag, want bool
		}{
			{name: "lowercase-unchanged"},
			{name: "uppercase-same-uuid", encoding: "upper"},
			{name: "braces-same-uuid", encoding: "braces"},
			{name: "urn-same-uuid", encoding: "urn"},
			{name: "compact-same-uuid", encoding: "compact"},
			{name: "other-uuid-is-dirty", other: true, want: true},
			{name: "other-uuid-uppercase-is-dirty", other: true, encoding: "upper", want: true},
			{name: "nonempty-to-empty-is-dirty", postEmpty: true, want: true},
			{name: "empty-to-nonempty-is-dirty", storedEmpty: true, want: true},
			{name: "empty-stays-empty", storedEmpty: true, postEmpty: true},
			{name: "genuine-entity-edit", encoding: "upper", editName: true, want: true},
			{name: "genuine-bool-edit", encoding: "upper", editFlag: true, want: true},
			{name: "genuine-table-edit", encoding: "upper", extra: `Ряд = Объект.Lines.Добавить(); Ряд.Text = "unsaved";`, want: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				form := &metadata.FormModule{Name: "NativeForm", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
					Attributes: []*metadata.FormAttribute{{Name: "Caption", TypeRef: "string", Save: false}},
					Elements:   []*metadata.FormElement{{Kind: metadata.FormElementPages, Name: "Pages", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnPageChange: "Changed"}, Children: []*metadata.FormElement{{Kind: metadata.FormElementPage, Name: "Calls"}}}},
				}
				form.ProgramAST = mustParse(t, "Процедура Changed(Элемент)\nОбъект.Caption = \"calls\";\n"+tc.extra+"\nКонецПроцедуры")
				entity.Forms = []*metadata.FormModule{form}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{branch, entity}})
				interp := interpreter.New()
				interp.LookupProc = reg.GetModuleProc
				s := &Server{store: db, reg: reg, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
				s.entitySvc = s.newEntityService(nil)
				id := uuid.New()
				var stored any = a.String()
				if tc.storedEmpty {
					stored = nil
				}
				if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Name": "own", "Branch": stored, "Flag": false}, entity); err != nil {
					t.Fatal(err)
				}
				posted := a.String()
				if tc.other {
					posted = b.String()
				}
				switch tc.encoding {
				case "upper":
					posted = strings.ToUpper(posted)
				case "braces":
					posted = "{" + posted + "}"
				case "urn":
					posted = "urn:uuid:" + posted
				case "compact":
					posted = strings.ReplaceAll(posted, "-", "")
				}
				if tc.postEmpty {
					posted = ""
				}
				body := url.Values{"_kind": {"object"}, "_form": {form.Name}, "_id": {id.String()}, "_version": {"1"}, "_element": {"Pages"}, "_event": {string(metadata.FormEventOnPageChange)}, "_page": {"Calls"}, "Name": {"own"}, "Branch": {posted}, "Flag": {"false"}}
				if tc.editName {
					body.Set("Name", "genuine unsaved edit")
				}
				if tc.editFlag {
					body.Set("Flag", "true")
				}
				router := chi.NewRouter()
				router.Post("/ui/document/{entity}/form-event", s.handleManagedFormEvent)
				r := httptest.NewRequest(http.MethodPost, "/ui/document/"+entity.Name+"/form-event", strings.NewReader(body.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				actor := &auth.User{ID: "own-reference-user", Login: "own-reference-user", Roles: []*auth.Role{{Name: "Own", Permissions: auth.Permission{Documents: map[string][]string{entity.Name: {"read", "write"}}, Catalogs: map[string][]string{branch.Name: {"read"}}}}}}
				r = r.WithContext(auth.ContextWithUser(r.Context(), actor))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("HTTP%d", w.Code)
				}
				resp := decodeFormEventResponse(t, w.Body.Bytes())
				if !resp.OK || resp.Error != "" || resp.Dirty == nil || *resp.Dirty != tc.want {
					t.Fatalf("dirty=%v want=%t error=%s", resp.Dirty, tc.want, resp.Error)
				}
				persisted, err := db.GetByID(ctx, entity.Name, id, entity)
				if err != nil || persisted["Name"] != "own" || persisted["_version"] != int64(1) {
					t.Fatal("event wrote document")
				}
				if tpCellNorm(entity.Fields[1], persisted["Branch"]) != tpCellNorm(entity.Fields[1], stored) {
					t.Fatal("event changed durable reference")
				}
			})
		}
	})
}
