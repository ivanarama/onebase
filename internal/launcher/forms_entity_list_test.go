package launcher

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/onec_forms"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntityListEditorPublicRoundTrip(t *testing.T) {
	config := t.TempDir()
	store := &Store{path: filepath.Join(t.TempDir(), "bases.yaml")}
	base := &Base{ID: "entity-list", Path: config, ConfigSource: "file"}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store}
	request := func(handler http.HandlerFunc, path string, values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", base.ID)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	yaml := `schema: onebase.form/v1
form: {name: ФормаОбъекта, kind: object, entity: Заявка}
elements:
 - kind: Список
   id: history
   name: History
`
	spec := `{"entity":"Заказ","filter":[{"field":"Клиент","op":"eq","source":"Объект.Клиент"},{"field":"Флаг","op":"eq","value":false},{"field":"Сумма","op":"eq","value":0}],"order_by":["Дата desc","Номер asc"],"limit":5,"columns":["Номер","Дата"],"on_row_click":"открыть"}`
	response := request(h.configuratorFormsEditOp, "/forms/edit-op", url.Values{"op": {"setEntityList"}, "node": {"elements.0"}, "yaml": {yaml}, "entity_list": {spec}})
	var edited editOpResponse
	if err := json.Unmarshal(response.Body.Bytes(), &edited); err != nil || !edited.OK {
		t.Fatalf("edit: %s %v", response.Body.String(), err)
	}
	response = request(h.configuratorFormsEditOp, "/forms/edit-op", url.Values{"op": {"setProp"}, "node": {"elements.0"}, "yaml": {edited.YAML}, "key": {"title.ru"}, "value": {"История"}})
	if err := json.Unmarshal(response.Body.Bytes(), &edited); err != nil || !edited.OK {
		t.Fatalf("unrelated edit: %s %v", response.Body.String(), err)
	}
	if got := edited.Model["elements.0"].EntityList; got == nil || got.Limit != 5 || got.Filter[1].Value.Value != "false" || got.Filter[2].Value.Value != "0" {
		t.Fatalf("model=%+v", got)
	}
	saved := request(h.configuratorFormsSave, "/forms/save", url.Values{"entity": {"Заявка"}, "name": {"ФормаОбъекта"}, "yaml": {edited.YAML}})
	if saved.Code != 303 {
		t.Fatalf("save=%d %s", saved.Code, saved.Body.String())
	}
	path := filepath.Join(config, "forms", "заявка", "формаобъекта.form.yaml")
	form, err := loader.NewManagedFormLoader().LoadFormFile(path, "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Elements) != 1 || form.Elements[0].EntityList == nil || form.Elements[0].EntityList.Filter[1].Value.Tag != "!!bool" || form.Elements[0].EntityList.Filter[2].Value.Tag != "!!int" {
		t.Fatalf("saved form=%+v", form.Elements)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened := request(h.configuratorFormsEditOp, "/forms/edit-op", url.Values{"op": {"render"}, "yaml": {string(bytes)}})
	if reopened.Code != 200 {
		t.Fatalf("reopen=%s", reopened.Body.String())
	}
	ir, err := onec_forms.ReadFormYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip := filepath.Join(t.TempDir(), "roundtrip.form.yaml")
	if err := onec_forms.WriteFormYAML(ir, roundtrip); err != nil {
		t.Fatal(err)
	}
	again, err := loader.NewManagedFormLoader().LoadFormFile(roundtrip, "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Elements[0]; got.ID != "history" || got.EntityList.Filter[1].Value.Tag != "!!bool" || got.EntityList.Filter[2].Value.Tag != "!!int" || got.EntityList.OnRowClick != "открыть" {
		t.Fatalf("IR lost specification: %+v", got)
	}
	onec_forms.NormalizeForExport(ir)
	if err := onec_forms.WriteFormXML(ir, filepath.Join(t.TempDir(), "Form.xml")); err == nil {
		t.Fatal("XML silently dropped unsupported list")
	}
}
