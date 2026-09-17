package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func multilineHTTPControl(t *testing.T, page, name string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var find func(*html.Node) *html.Node
	find = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && (n.Data == "textarea" || n.Data == "input") {
			for _, a := range n.Attr {
				if a.Key == "name" && a.Val == name {
					return n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if got := find(c); got != nil {
				return got
			}
		}
		return nil
	}
	if got := find(doc); got != nil {
		return got
	}
	t.Fatalf("control %q missing from HTTP response", name)
	return nil
}

func multilineHTTPAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func multilineHTTPHasAttr(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if a.Key == name {
			return true
		}
	}
	return false
}

func TestMultilineHTTPObjectRoundTrip(t *testing.T) {
	const value = "Первая строка\r\nВторая <script>alert('x')</script> & текст\nТретья"
	const hint = "Подсказка <с примером> & кавычками \""
	for _, mode := range []string{"auto", "auto-plain", "inherited", "override-false", "override-true"} {
		t.Run(mode, func(t *testing.T) {
			ent := &metadata.Entity{Name: "Заметки", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Текст", Type: metadata.FieldTypeString, Multiline: mode != "auto-plain" && mode != "override-true"},
				{Name: "Код", Type: metadata.FieldTypeString},
			}}
			managed := !strings.HasPrefix(mode, "auto")
			if managed {
				el := fieldEl("ПолеТекст", "Объект.Текст")
				el.Height, el.Hint, el.AccessKey, el.Required = 500, hint, "t", true
				el.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "ТекстИзменен"}
				if strings.HasPrefix(mode, "override-") {
					enabled := mode == "override-true"
					el.Multiline = &enabled
				}
				ent.Forms = []*metadata.FormModule{managedObjectForm(el, fieldEl("ПолеКод", "Объект.Код"))}
			}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			get := func(id string) string {
				t.Helper()
				params := map[string]string{"kind": "catalog", "entity": ent.Name, "id": id}
				rec := httptest.NewRecorder()
				if id == "" {
					s.form(rec, reqWithChi(http.MethodGet, "/ui/catalog/Заметки/new", nil, params))
				} else {
					s.formEdit(rec, reqWithChi(http.MethodGet, "/ui/catalog/Заметки/"+id+"/edit", nil, params))
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("GET=%d: %s", rec.Code, rec.Body.String())
				}
				return rec.Body.String()
			}
			page := get("")
			control := multilineHTTPControl(t, page, "Текст")
			wantTag := "textarea"
			if mode == "override-false" || mode == "auto-plain" {
				wantTag = "input"
			}
			if control.Data != wantTag {
				t.Fatalf("control=%s, want %s", control.Data, wantTag)
			}
			if wantTag == "textarea" {
				rows := "5"
				if managed {
					rows = "500"
				}
				if multilineHTTPAttr(control, "rows") != rows {
					t.Fatalf("rows=%q, want %s", multilineHTTPAttr(control, "rows"), rows)
				}
			}
			if managed && multilineHTTPAttr(control, "title") != hint {
				t.Fatalf("hint lost: title=%q", multilineHTTPAttr(control, "title"))
			}
			if managed && (!multilineHTTPHasAttr(control, "required") || multilineHTTPAttr(control, "accesskey") != "t" || multilineHTTPAttr(control, "data-ob-fire-change") != "ПолеТекст") {
				t.Fatalf("managed input attributes lost: %v", control.Attr)
			}
			if multilineHTTPControl(t, page, "Код").Data != "input" {
				t.Fatal("ordinary string field changed editor")
			}
			rec := httptest.NewRecorder()
			s.submit(rec, reqWithChi(http.MethodPost, "/ui/catalog/Заметки/new", url.Values{"Текст": {value}, "Код": {"A"}}, map[string]string{"kind": "catalog", "entity": ent.Name}))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("POST=%d: %s", rec.Code, rec.Body.String())
			}
			rows, err := s.store.List(ctx, ent.Name, ent, storage.ListParams{})
			if err != nil || len(rows) != 1 || rows[0]["Текст"] != value {
				t.Fatalf("persisted rows=%v err=%v", rows, err)
			}
			page = get(fmt.Sprint(rows[0]["id"]))
			control = multilineHTTPControl(t, page, "Текст")
			if strings.Contains(page, "<script>alert('x')</script>") {
				t.Fatal("plain text became executable HTML")
			}
			got := multilineHTTPAttr(control, "value")
			if control.Data == "textarea" && control.FirstChild != nil {
				got = control.FirstChild.Data
			}
			// HTML parsing normalizes CRLF, storage must retain the original bytes.
			if got != strings.ReplaceAll(value, "\r\n", "\n") {
				t.Fatalf("reloaded control=%q, want %q", got, value)
			}
		})
	}
}

func TestMultilineHTTPInfoRegister(t *testing.T) {
	ir := &metadata.InfoRegister{Name: "Памятки", Dimensions: []metadata.Field{
		{Name: "Условие", Type: metadata.FieldTypeString, Multiline: true},
		{Name: "Код", Type: metadata.FieldTypeString},
	}, Resources: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString, Multiline: true}}}
	s, ctx := newSubmitTestServer(t, nil)
	if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	params := map[string]string{"name": ir.Name}
	rec := httptest.NewRecorder()
	s.infoRegForm(rec, reqWithChi(http.MethodGet, "/ui/inforeg/Памятки/new", nil, params))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET=%d: %s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"Условие", "Текст"} {
		control := multilineHTTPControl(t, rec.Body.String(), name)
		if control.Data != "textarea" || multilineHTTPAttr(control, "rows") != "5" {
			t.Fatalf("%s: expected textarea rows=5", name)
		}
	}
	if multilineHTTPControl(t, rec.Body.String(), "Код").Data != "input" {
		t.Fatal("ordinary dimension changed editor")
	}
	const value = "Строка 1\nСтрока 2 <b> & текст"
	rec = httptest.NewRecorder()
	s.infoRegSubmit(rec, reqWithChi(http.MethodPost, "/ui/inforeg/Памятки/new", url.Values{"Условие": {value}, "Код": {"A"}, "Текст": {value}}, params))
	if rec.Code != http.StatusFound {
		t.Fatalf("POST=%d: %s", rec.Code, rec.Body.String())
	}
	rows, err := s.store.InfoRegList(ctx, ir, storage.RegFilter{})
	if err != nil || len(rows) != 1 || rows[0]["Условие"] != value || rows[0]["Текст"] != value {
		t.Fatalf("saved register rows=%v, err=%v", rows, err)
	}
	rec = httptest.NewRecorder()
	s.infoRegList(rec, reqWithChi(http.MethodGet, "/ui/inforeg/Памятки", nil, params))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Строка 2 &lt;b&gt; &amp; текст") {
		t.Fatalf("saved register text not shown escaped: status=%d", rec.Code)
	}
}

func TestMultilineHTTPTransientAttributeAndReadonly(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Объект.Канал = Памятка;
	Объект.Записать();
КонецПроцедуры
`, []*metadata.FormAttribute{{Name: "Памятка", TypeRef: "string", Save: false}})
	enabled := true
	f.entity.Fields[0].Multiline = true
	form := f.entity.Forms[0]
	form.Elements = append(form.Elements,
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеПамятка", DataPath: "Форма.Памятка", Multiline: &enabled, Height: 12, Hint: "Нехранимый текст"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеИмя", DataPath: "Объект.Наименование", ReadOnly: true, Hint: "Только чтение"},
	)
	rec := httptest.NewRecorder()
	f.srv.formEdit(rec, reqWithChi(http.MethodGet, "/ui/catalog/Обращение/"+f.docID.String()+"/edit", nil,
		map[string]string{"kind": "catalog", "entity": f.entity.Name, "id": f.docID.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET=%d: %s", rec.Code, rec.Body.String())
	}
	local := multilineHTTPControl(t, rec.Body.String(), "Памятка")
	if local.Data != "textarea" || multilineHTTPAttr(local, "rows") != "12" || multilineHTTPAttr(local, "title") != "Нехранимый текст" {
		t.Fatalf("transient textarea=%v", local)
	}
	readonly := multilineHTTPControl(t, rec.Body.String(), "Наименование")
	if readonly.Data != "textarea" || !multilineHTTPHasAttr(readonly, "readonly") || multilineHTTPAttr(readonly, "title") != "Только чтение" {
		t.Fatalf("readonly textarea attrs=%v", readonly.Attr)
	}
	const text = "Нехранимый\nтекст <>&"
	response := f.fire(t, url.Values{"_id": {f.docID.String()}, "Памятка": {text}})
	if !response.OK || response.Values["Канал"] != text || response.Values["Памятка"] != text {
		t.Fatalf("event response=%+v", response)
	}
	row, err := f.srv.store.GetByID(context.Background(), f.entity.Name, f.docID, f.entity)
	if err != nil || row["Канал"] != text {
		t.Fatalf("saved event result=%v, err=%v", row, err)
	}
	if _, exists := row["Памятка"]; exists {
		t.Fatal("save:false attribute persisted as an object field")
	}
}
