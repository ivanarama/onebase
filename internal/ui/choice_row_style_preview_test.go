package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Имена обычных реквизитов не становятся служебным CSS и не теряют preview.
// Оба HTTP-пути используют те же строки и права, что форма и подбор пользователя.
func TestChoiceRowStyle_PreviewKeepsOrdinaryFields(t *testing.T) {
	for _, field := range []string{"_style", "_STYLE", "_style_bg", "_STYLE_BG", "Памятка"} {
		t.Run(field, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				raw := "position:fixed;inset:0;<img src=x onerror=alert(1)>\nПамятка"
				target := &metadata.Entity{Name: "КаталогСтилей", Kind: metadata.KindCatalog, ChoicePreview: field, Fields: []metadata.Field{
					{Name: "Наименование", Type: metadata.FieldTypeString},
					{Name: field, Type: metadata.FieldTypeString},
				}}
				form := &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", EntityName: "ДокументСтилей", LayoutKind: metadata.FormLayoutManaged,
					Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ПолеВыбор", DataPath: "Объект.Выбор", ChoiceContext: map[string]string{"Контекст": "Объект.Контекст"}}}}
				owner := &metadata.Entity{Name: "ДокументСтилей", Kind: metadata.KindDocument, Forms: []*metadata.FormModule{form}, Fields: []metadata.Field{
					{Name: "Выбор", Type: metadata.FieldType("reference:КаталогСтилей"), RefEntity: target.Name},
					{Name: "Контекст", Type: metadata.FieldTypeString},
				}}
				if err := metadata.Validate([]*metadata.Entity{target, owner}, nil); err != nil {
					t.Fatal(err)
				}
				s := newSubmitTestServerOn(t, db, []*metadata.Entity{target, owner})
				id := uuid.New()
				if err := db.Upsert(ctx, target.Name, id, map[string]any{"Наименование": "Данные", field: raw}, target); err != nil {
					t.Fatal(err)
				}
				user := &auth.User{ID: uuid.NewString(), IsAdmin: true}
				rule := metadata.ChoiceStyleRule{When: strings.ToLower(field) + ` <> ""`, Style: metadata.FormCellStyle{Color: "red", Background: "#fef9c3"}}
				for _, tc := range []struct {
					name   string
					style  *metadata.ChoiceRowStyle
					styled bool
				}{
					{name: "absent"},
					{name: "false rule", style: &metadata.ChoiceRowStyle{Rules: []metadata.ChoiceStyleRule{{When: strings.ToLower(field) + ` = ""`, Style: rule.Style}}}},
					{name: "other user", style: &metadata.ChoiceRowStyle{ForRoles: []string{"ОператорКЦ"}, Rules: []metadata.ChoiceStyleRule{rule}}},
					{name: "true rule", style: &metadata.ChoiceRowStyle{Rules: []metadata.ChoiceStyleRule{rule}}, styled: true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						target.ChoiceRowStyle = tc.style
						responses := []*httptest.ResponseRecorder{
							serveRefOptions(t, s, target.Name, "", user),
							postPreviewPage(t, s, target.Name, map[string]any{"limit": 10, "source": map[string]string{"entity": owner.Name, "element": "ПолеВыбор"}}, user),
						}
						for i, rec := range responses {
							if rec.Code != http.StatusOK {
								t.Fatalf("endpoint %d HTTP %d: %s", i, rec.Code, rec.Body.String())
							}
							var got struct {
								Items []map[string]any `json:"items"`
							}
							if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
								t.Fatal(err)
							}
							if len(got.Items) != 1 {
								t.Fatalf("endpoint %d: %v", i, got.Items)
							}
							row := got.Items[0]
							if row[field] != raw {
								t.Errorf("endpoint %d ordinary %s=%q, want %q", i, field, row[field], raw)
							}
							if i == 1 && row["_preview"] != raw {
								t.Errorf("POST preview=%q, want %q", row["_preview"], raw)
							}
							css, _ := row[refOptionStyleKey].(string)
							bg, _ := row[refOptionStyleBgKey].(string)
							if tc.styled {
								if css != "color:red;background:#fef9c3" || bg != "#fef9c3" {
									t.Errorf("computed style=%q bg=%q", css, bg)
								}
							} else if css != "" || bg != "" {
								t.Errorf("ordinary data became CSS: %q / %q", css, bg)
							}
						}
						req := reqWithChi(http.MethodGet, "/ui/document/ДокументСтилей/new", nil, map[string]string{"kind": "document", "entity": owner.Name})
						req = req.WithContext(auth.ContextWithUser(req.Context(), user))
						rec := httptest.NewRecorder()
						s.form(rec, req)
						if rec.Code != http.StatusOK {
							t.Fatalf("form HTTP %d: %s", rec.Code, rec.Body.String())
						}
						text := rec.Body.String()
						start := strings.Index(text, `<option value="`+id.String()+`"`)
						if start < 0 {
							t.Fatal("rendered form has no option")
						}
						option := text[start : start+strings.Index(text[start:], ">")]
						if strings.Contains(option, "position:fixed") || strings.Contains(option, "onerror") || strings.Contains(option, "style=") != tc.styled {
							t.Errorf("untrusted option style: %s", option)
						}
					})
				}
			})
		})
	}
}
