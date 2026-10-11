package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestConfiguratorChoiceFilterIsRootHTTP(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	b := &Base{Path: t.TempDir(), ConfigSource: "file"}
	if err := s.Add(b); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: s}
	const src = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
elements:
  - id: city-picker
    kind: ПолеВвода
    name: ПолеГород
    data_path: Объект.Город
`
	post := func(condition string) editOpResponse {
		t.Helper()
		form := url.Values{"op": {"setChoiceFilter"}, "node": {"elements.0"}, "choice_filter": {condition}, "yaml": {src}}
		request := httptest.NewRequest(http.MethodPost, "/bases/"+b.ID+"/configurator/forms/edit-op", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", b.ID)
		request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, rctx))
		recorder := httptest.NewRecorder()
		h.configuratorFormsEditOp(recorder, request)
		var response editOpResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("response: %v; body=%s", err, recorder.Body.String())
		}
		return response
	}
	for _, value := range []string{"true", "false"} {
		response := post(`[{"field":"is_root","op":"eq","value":` + value + `}]`)
		if !response.OK {
			t.Fatalf("is_root=%s rejected: %v", value, response.Errors)
		}
		conditions := response.Model["elements.0"].ChoiceFilter
		if len(conditions) != 1 || conditions[0].Field != metadata.FormChoiceRootField || conditions[0].Value == nil || *conditions[0].Value != (value == "true") {
			t.Fatalf("is_root=%s lost on HTTP round trip: %+v", value, conditions)
		}
		if !strings.Contains(response.YAML, "value: "+value) {
			t.Fatalf("typed value missing from YAML: %s", response.YAML)
		}
	}
	combined := post(`[{"field":"is_root","op":"eq","value":false},{"field":"parent_id","op":"not_in_hierarchy","ref":"d4ba641c-70ae-4632-bf99-035f4caaa0af"}]`)
	if !combined.OK {
		t.Fatalf("combined filter rejected: %v", combined.Errors)
	}
	conditions := combined.Model["elements.0"].ChoiceFilter
	if len(conditions) != 2 || conditions[0].Value == nil || *conditions[0].Value || conditions[1].Op != string(metadata.FormChoiceOpNotInHierarchy) || conditions[1].Ref != "d4ba641c-70ae-4632-bf99-035f4caaa0af" {
		t.Fatalf("combined filter lost on HTTP round trip: %+v", conditions)
	}

	// The YAML editor has no target catalog metadata. A real reference
	// attribute named is_root must retain its sources; check validates whether
	// that attribute exists (and rejects these sources for the pseudo-field).
	for _, condition := range []string{
		`[{"field":"is_root","op":"eq","from":"Объект.Город"}]`,
		`[{"field":"is_root","op":"eq","ref":"d4ba641c-70ae-4632-bf99-035f4caaa0af"}]`,
		`[{"field":"is_root","op":"not_in_hierarchy","ref":"d4ba641c-70ae-4632-bf99-035f4caaa0af"}]`,
	} {
		response := post(condition)
		if !response.OK {
			t.Fatalf("attribute source rejected: %s: %v", condition, response.Errors)
		}
		var want []canvasChoiceCondition
		if err := json.Unmarshal([]byte(condition), &want); err != nil {
			t.Fatal(err)
		}
		got := response.Model["elements.0"].ChoiceFilter
		if len(got) != 1 || got[0].Field != want[0].Field || got[0].Op != want[0].Op || got[0].From != want[0].From || got[0].Ref != want[0].Ref {
			t.Fatalf("attribute source lost: %+v, want %+v", got, want)
		}
	}
	for _, condition := range []string{
		`[{"field":"is_root","op":"in_hierarchy","value":true}]`,
		`[{"field":"is_root","op":"not_in_hierarchy","value":true}]`,
	} {
		if response := post(condition); response.OK {
			t.Fatalf("invalid is_root saved: %s", condition)
		}
	}
}
