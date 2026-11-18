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
)

// Галка «Ввод по строке» в панели свойств элемента. Проверяется публичный
// маршрут конфигуратора: ключ пишется булевым скаляром, доезжает до модели
// холста, а снятая галка удаляет ключ (умолчание и есть «выключено»).
func TestConfiguratorInputByStringRoundTrip(t *testing.T) {
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
  - kind: ПолеВвода
    name: ПолеСклад
    data_path: Объект.Склад
`
	post := func(values url.Values) editOpResponse {
		t.Helper()
		values.Set("yaml", src)
		request := httptest.NewRequest(http.MethodPost, "/bases/"+b.ID+"/configurator/forms/edit-op", strings.NewReader(values.Encode()))
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

	set := post(url.Values{"op": {"setProp"}, "node": {"elements.0"}, "key": {"input_by_string"}, "value": {"true"}})
	if !set.OK {
		t.Fatalf("ввод по строке отклонён: %v", set.Errors)
	}
	if !strings.Contains(set.YAML, "input_by_string: true") {
		t.Fatalf("ключ не записан булевым скаляром:\n%s", set.YAML)
	}
	if !set.Model["elements.0"].InputByString {
		t.Fatalf("панель свойств не увидит включённый ввод по строке: %+v", set.Model["elements.0"])
	}

	cleared := post(url.Values{"op": {"delProp"}, "node": {"elements.0"}, "key": {"input_by_string"}})
	if !cleared.OK {
		t.Fatalf("снятие галки отклонено: %v", cleared.Errors)
	}
	if strings.Contains(cleared.YAML, "input_by_string") || cleared.Model["elements.0"].InputByString {
		t.Fatalf("снятая галка оставила ключ:\n%s", cleared.YAML)
	}
}
