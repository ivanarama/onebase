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

	"github.com/ivantit66/onebase/internal/dsl/loader"
)

// Кнопка «Открыть карточку» в конструкторе форм (#1876): флажок панели
// «Форма» пишет form.ref_card_button_admin_only булевым скаляром, посторонняя
// правка не теряет ни его, ни ref_card_button поля, а сохранённый файл
// загрузчик читает обратно. Путь — HTTP-эндпоинты edit-op и save, которыми
// пользуется конфигуратор.
func TestConfiguratorFormRefCardButton_HTTPRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	s := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	b := &Base{ID: "ref-card-roundtrip", Path: configDir, ConfigSource: "file"}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add base: %v", err)
	}
	h := &handler{store: s}
	post := func(path string, values url.Values, call func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/bases/"+b.ID+"/configurator/forms/"+path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", b.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		call(rec, req)
		return rec
	}
	editOp := func(values url.Values) editOpResponse {
		t.Helper()
		rec := post("edit-op", values, h.configuratorFormsEditOp)
		if rec.Code != http.StatusOK {
			t.Fatalf("edit-op %v: статус %d: %s", values, rec.Code, rec.Body.String())
		}
		var resp editOpResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("разбор edit-op JSON: %v", err)
		}
		if !resp.OK || resp.Form == nil {
			t.Fatalf("edit-op %v: ok=%v form=%v errors=%v", values, resp.OK, resp.Form, resp.Errors)
		}
		return resp
	}

	source := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеКлиент
    data_path: Объект.Клиент
    ref_card_button: false
`
	// Без ключей формы панель показывает умолчание: кнопка есть и видна всем.
	initial := editOp(url.Values{"op": {"render"}, "node": {"form"}, "yaml": {source}})
	if !initial.Form.RefCardButton || initial.Form.RefCardAdminOnly {
		t.Fatalf("панель формы без ключей: %+v", *initial.Form)
	}

	// Флажок «Только администратору».
	set := editOp(url.Values{"op": {"setProp"}, "node": {"form"}, "key": {"ref_card_button_admin_only"}, "value": {"true"}, "yaml": {source}})
	if !strings.Contains(set.YAML, "ref_card_button_admin_only: true") || !set.Form.RefCardAdminOnly {
		t.Fatalf("ключ формы не записан булевым:\n%s\nform=%+v", set.YAML, *set.Form)
	}

	// Посторонняя правка формы не теряет ни ключ формы, ни ключ поля.
	retitled := editOp(url.Values{"op": {"setProp"}, "node": {"form"}, "key": {"title.ru"}, "value": {"Заказ клиента"}, "yaml": {set.YAML}})
	for _, want := range []string{"ref_card_button_admin_only: true", "ref_card_button: false"} {
		if !strings.Contains(retitled.YAML, want) {
			t.Fatalf("после правки заголовка потерян %q:\n%s", want, retitled.YAML)
		}
	}

	save := post("save", url.Values{"entity": {"Заказ"}, "name": {"ФормаОбъекта"}, "yaml": {retitled.YAML}, "os": {""}}, h.configuratorFormsSave)
	if save.Code != http.StatusSeeOther {
		t.Fatalf("save: статус %d: %s", save.Code, save.Body.String())
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(configDir, "forms", "заказ", "формаобъекта.form.yaml"), "Заказ")
	if err != nil {
		t.Fatalf("повторная загрузка YAML: %v", err)
	}
	if !fm.RefCardButtonAdminOnly {
		t.Fatal("ref_card_button_admin_only потерян после save/load")
	}
	if len(fm.Elements) != 1 || fm.Elements[0].RefCardButton == nil || *fm.Elements[0].RefCardButton {
		t.Fatalf("ref_card_button поля потерян после save/load: %+v", fm.Elements)
	}

	// Снятие флажка удаляет ключ, а не пишет false.
	cleared := editOp(url.Values{"op": {"delProp"}, "node": {"form"}, "key": {"ref_card_button_admin_only"}, "yaml": {retitled.YAML}})
	if strings.Contains(cleared.YAML, "ref_card_button_admin_only") || cleared.Form.RefCardAdminOnly {
		t.Fatalf("ключ формы не удалён:\n%s", cleared.YAML)
	}
	// «Не показывать у ссылочных полей» — ref_card_button: false в блоке form.
	hidden := editOp(url.Values{"op": {"setProp"}, "node": {"form"}, "key": {"ref_card_button"}, "value": {"false"}, "yaml": {cleared.YAML}})
	if hidden.Form.RefCardButton {
		t.Fatalf("панель не видит ref_card_button: false формы:\n%s", hidden.YAML)
	}
}
