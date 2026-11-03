package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestChoiceRowStyle_ChoiceConditionExpressions(t *testing.T) {
	st := &metadata.ChoiceRowStyle{Rules: directionsStyle()}
	f := newRowStyleFixture(t, st, "")
	for _, expr := range []string{
		"ОграниченПриемЗаявокДляКЦ",
		"ограниченприемзаявокдлякц",
		"ОграниченПриемЗаявокДляКЦ = Истина",
		"ограниченприемзаявокдлякц = Истина",
		"ОграниченПриемЗаявокДляКЦ И НЕ ЧастичноОграниченПриемЗаявокДляКЦ",
		"Наименование = \"Холодильники\"",
	} {
		t.Run(expr, func(t *testing.T) {
			st.Rules = []metadata.ChoiceStyleRule{{When: expr, Style: metadata.FormCellStyle{Color: "red"}}}
			styles := f.styles(t, f.operator)
			if !strings.Contains(styles[f.full], "color:") {
				t.Errorf("true condition %q did not style its matching row: %v", expr, styles)
			}
			if styles[f.part] != "" || styles[f.plain] != "" {
				t.Errorf("false condition styled unrelated rows: %v", styles)
			}
		})
	}
}

func TestChoiceRowStyle_RawFieldDoesNotBecomeOptionStyle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dirs := &metadata.Entity{Name: "КаталогСтилей", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "_style", Type: metadata.FieldTypeString},
		{Name: "_style_bg", Type: metadata.FieldTypeString},
	}}
	doc := &metadata.Entity{Name: "ДокументСтилей", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Выбор", Type: metadata.FieldType("reference:КаталогСтилей"), RefEntity: dirs.Name},
	}}
	if err := metadata.Validate([]*metadata.Entity{dirs, doc}, nil); err != nil {
		t.Fatal(err)
	}
	s := newSubmitTestServerOn(t, db, []*metadata.Entity{dirs, doc})
	id := uuid.New()
	if err := db.Upsert(ctx, dirs.Name, id, map[string]any{
		"Наименование": "Данные", "_style": "position:fixed;inset:0;color:red", "_style_bg": "url(/raw-field)",
	}, dirs); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		style *metadata.ChoiceRowStyle
		css   string
	}{
		{name: "not declared"},
		{name: "rule does not match", style: &metadata.ChoiceRowStyle{Rules: []metadata.ChoiceStyleRule{{When: "Ложь", Style: metadata.FormCellStyle{Color: "red"}}}}},
		{name: "user does not match", style: &metadata.ChoiceRowStyle{ForRoles: []string{"ОператорКЦ"}, Rules: directionsStyle()}},
		// The colliding field remains available as ordinary condition data.
		{name: "computed style", style: &metadata.ChoiceRowStyle{Rules: []metadata.ChoiceStyleRule{{When: `_style = "position:fixed;inset:0;color:red"`, Style: metadata.FormCellStyle{Color: "red"}}}}, css: "color:red"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dirs.ChoiceRowStyle = tc.style
			req := reqWithChi(http.MethodGet, "/ui/document/ДокументСтилей/new", nil, map[string]string{"kind": "document", "entity": doc.Name})
			req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: uuid.NewString(), IsAdmin: true}))
			rec := httptest.NewRecorder()
			s.form(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			start := strings.Index(body, `<option value="`+id.String()+`"`)
			if start < 0 {
				t.Fatal("missing option")
			}
			option := body[start : start+strings.Index(body[start:], ">")]
			if strings.Contains(option, "position:") || strings.Contains(option, "/raw-field") || (strings.Contains(option, "style=") != (tc.css != "")) {
				t.Errorf("option must contain only computed CSS: %s", option)
			}
			rows := serveRefOptions(t, s, dirs.Name, "", &auth.User{IsAdmin: true})
			if rows.Code != http.StatusOK {
				t.Fatalf("ref-options HTTP %d: %s", rows.Code, rows.Body.String())
			}
			var got struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(rows.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Items) != 1 {
				t.Fatalf("expected one option: %v", got.Items)
			}
			if got.Items[0]["_style"] != "position:fixed;inset:0;color:red" || got.Items[0]["_style_bg"] != "url(/raw-field)" {
				t.Errorf("ordinary data was changed: %v", got.Items[0])
			}
			css, _ := got.Items[0][refOptionStyleKey].(string)
			if css != tc.css || got.Items[0][refOptionStyleBgKey] != nil {
				t.Errorf("JSON must contain only computed CSS: %v", got.Items[0])
			}
		})
	}
}

func TestChoiceRowStyle_SelectedOffPageOptionKeepsStyle(t *testing.T) {
	f := newRowStyleFixture(t, &metadata.ChoiceRowStyle{Rules: directionsStyle()}, "")
	f.dirs.OrderBy = []string{"Наименование"}
	for i := 0; i < 55; i++ {
		if err := f.db.Upsert(context.Background(), f.dirs.Name, uuid.New(),
			map[string]any{"Наименование": fmt.Sprintf("А %03d", i)}, f.dirs); err != nil {
			t.Fatal(err)
		}
	}
	docID := uuid.New()
	if err := f.db.Upsert(context.Background(), f.doc.Name, docID,
		map[string]any{"Направление": f.full.String()}, f.doc); err != nil {
		t.Fatal(err)
	}
	req := reqWithChi(http.MethodGet, "/ui/document/Звонок/"+docID.String(), nil,
		map[string]string{"kind": "document", "entity": f.doc.Name, "id": docID.String()})
	req = req.WithContext(auth.ContextWithUser(req.Context(), f.operator))
	rec := httptest.NewRecorder()
	f.s.formEdit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	start := strings.Index(body, "<option value=\""+f.full.String()+"\"")
	if start < 0 {
		t.Fatal("selected row was not appended to the initial page")
	}
	option := body[start : start+strings.Index(body[start:], ">")]
	t.Log(option)
	if !strings.Contains(option, "style=\"color:") {
		t.Error("styled selected reference outside the initial page lost its option style")
	}
}
