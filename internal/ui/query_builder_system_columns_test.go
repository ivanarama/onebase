package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Читаем ту схему, которую получает конструктор через публичный HTTP-вход.
func TestQueryBuilderDocumentSystemColumns(t *testing.T) {
	entities := []*metadata.Entity{
		{Name: "ЗаказыК", Kind: metadata.KindDocument},
		{Name: "КлиентыК", Kind: metadata.KindCatalog},
		{Name: "ОсобыеК", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "пРоВеДеН", Type: metadata.FieldTypeString},
			{Name: "ПометкаУдаления", Type: metadata.FieldTypeNumber},
		}},
	}
	s, _ := newSubmitTestServer(t, entities)
	s.reg.Load(runtime.LoadOptions{Entities: entities})
	r := chi.NewRouter()
	s.Mount(r)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ui/query-builder", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET query-builder: %d: %s", rr.Code, rr.Body.String())
	}
	match := regexp.MustCompile(`(?s)<script[^>]*id="ob-query-builder-schema"[^>]*>(.*?)</script>`).FindStringSubmatch(rr.Body.String())
	if len(match) != 2 {
		t.Fatal("нет схемы конструктора в HTML")
	}
	var sources []qbSource
	if err := json.Unmarshal([]byte(match[1]), &sources); err != nil {
		t.Fatal(err)
	}
	expected := map[string]map[string]string{
		"document:ЗаказыК": {"Проведен": "bool", "ПометкаУдаления": "bool"},
		"catalog:КлиентыК": {"ПометкаУдаления": "bool"},
		"document:ОсобыеК": {"пРоВеДеН": "string", "ПометкаУдаления": "number"},
	}
	for _, src := range sources {
		want, ok := expected[src.ID]
		if !ok {
			continue
		}
		got := map[string]string{}
		for _, f := range src.Fields {
			if _, duplicate := got[f.Name]; duplicate {
				t.Fatalf("%s: повтор поля %s", src.ID, f.Name)
			}
			got[f.Name] = f.Type
		}
		if len(got) != len(want) {
			t.Errorf("%s: поля = %v, want %v", src.ID, got, want)
		}
		for name, typ := range want {
			if got[name] != typ {
				t.Errorf("%s: %s = %q, want %q", src.ID, name, got[name], typ)
			}
		}
		delete(expected, src.ID)
	}
	if len(expected) != 0 {
		t.Fatalf("не найдены источники: %v", expected)
	}
}
