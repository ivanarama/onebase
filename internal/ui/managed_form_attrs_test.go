package ui

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedFormAttrsBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed form attribute regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_form_attrs_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("managed form attributes: %v\n%s", err, out)
	}
}

// The initial carrier configuration comes from the production GET page, and
// each next POST is built by managed.js from the preceding HTTP response.
func TestFormEvent_UnplacedAttributesSurviveOtherEvent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the HTTP/client form attribute regression test")
	}
	s, ent, skladID := attrEventServer(t, `
Процедура ПолеАПриИзменении()
	Объект.СкрытьПоле = Истина;
	Объект.Пустое = "";
	Объект.Склад = Справочники.Склад.НайтиПоНаименованию("Основной");
КонецПроцедуры
Процедура ПолеБПриИзменении()
	Если Объект.СкрытьПоле Тогда
		Сообщить("ФЛАГ:ДА");
	КонецЕсли;
	Если Объект.Пустое = "" Тогда
		Сообщить("ПУСТО:ДА");
	КонецЕсли;
	Сообщить("СКЛАД:" + Объект.Склад.Наименование);
КонецПроцедуры
`)
	form := ent.Forms[0]
	form.Attributes = []*metadata.FormAttribute{
		{Name: "СкрытьПоле", TypeRef: "bool"},
		{Name: "Пустое", TypeRef: "string"},
		{Name: "Склад", TypeRef: "CatalogRef.Склад"},
		// These must not become hidden form attribute carriers.
		{Name: "Наименование", TypeRef: "string"},
		{Name: "_version", TypeRef: "number"},
		{Name: "Объект", MainAttribute: true},
		{Name: "Строки", TypeRef: "ValueTable"},
	}
	form.Elements = []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "ПолеА", DataPath: "Объект.Наименование",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ПолеАПриИзменении"}},
		{Kind: metadata.FormElementField, Name: "ПолеБ", DataPath: "Объект.Наименование",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ПолеБПриИзменении"}},
		{Kind: metadata.FormElementField, Name: "Скрываемое", DataPath: "Объект.Наименование", HiddenWhen: "СкрытьПоле"},
		{Kind: metadata.FormElementField, Name: "Запираемое", DataPath: "Объект.Наименование", ReadOnlyWhen: "СкрытьПоле"},
	}

	page := httptest.NewRecorder()
	s.form(page, reqWithChi("GET", "/ui/catalog/Контрагент/new", nil, map[string]string{"entity": ent.Name, "kind": "catalog"}))
	if page.Code != 200 {
		t.Fatalf("GET form: %d: %s", page.Code, page.Body.String())
	}
	const marker = `<script type="application/json" id="ob-managed-config">`
	_, rest, ok := strings.Cut(page.Body.String(), marker)
	if !ok {
		t.Fatal("GET form has no client configuration")
	}
	configJSON, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("unterminated client configuration")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatal(err)
	}
	attrs, ok := config["formAttrs"].([]any)
	if !ok || len(attrs) != 3 {
		t.Fatalf("carrier list includes non-scalar/entity/service fields: %v", config["formAttrs"])
	}
	if _, ok := config["formAttrValues"]; !ok {
		t.Fatal("GET did not supply initial attribute values")
	}

	first := decodeFormEventResponse(t, executeFormEvent(t, s, ent, url.Values{
		"_element": {"ПолеА"}, "_event": {"ПриИзменении"}, "Наименование": {"Тест"},
	}).Body.Bytes())
	if !first.OK {
		t.Fatalf("first event: %s", first.Error)
	}
	fixture, err := json.Marshal(map[string]any{"config": config, "response": first})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "static/managed_form_attrs_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_FORM_ATTR_FIXTURE_B64="+base64.StdEncoding.EncodeToString(fixture))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("carry event response: %v\n%s", err, out)
	}
	var carried struct {
		Initial map[string]string `json:"initial"`
		Next    map[string]string `json:"next"`
	}
	if err := json.Unmarshal(out, &carried); err != nil {
		t.Fatalf("client payload: %v: %s", err, out)
	}
	for _, name := range []string{"СкрытьПоле", "Пустое", "Склад"} {
		if value, ok := carried.Initial[name]; !ok || value != "" {
			t.Fatalf("initial carrier %s = %q, present=%v", name, value, ok)
		}
	}
	if carried.Next["СкрытьПоле"] != "true" || carried.Next["Склад"] != skladID.String() {
		t.Fatalf("client lost flag or reference UUID: %v", carried.Next)
	}
	if value, ok := carried.Next["Пустое"]; !ok || value != "" {
		t.Fatalf("client lost explicit empty attribute: %v", carried.Next)
	}
	body := url.Values{"Наименование": {"Тест"}}
	for name, value := range carried.Next {
		body.Set(name, value)
	}
	second := decodeFormEventResponse(t, executeFormEvent(t, s, ent, body).Body.Bytes())
	if !second.OK || strings.Join(second.Messages, "|") != "ФЛАГ:ДА|ПУСТО:ДА|СКЛАД:Основной" {
		t.Fatalf("next event lost attributes: %+v", second)
	}
	if second.ElementStates == nil || !second.ElementStates.Hidden["Скрываемое"] || !second.ElementStates.ReadOnly["Запираемое"] {
		t.Fatalf("next event reset conditions: %+v", second.ElementStates)
	}

	// Negative control: omitting the carrier reproduces the original rollback.
	body.Del("СкрытьПоле")
	missing := decodeFormEventResponse(t, executeFormEvent(t, s, ent, body).Body.Bytes())
	if !missing.OK || missing.ElementStates == nil || missing.ElementStates.Hidden["Скрываемое"] || missing.ElementStates.ReadOnly["Запираемое"] {
		t.Fatalf("test did not reproduce rollback without carrier: %+v", missing)
	}
}

func TestManagedFormAttrsInitialValues(t *testing.T) {
	ent := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog}
	form := &metadata.FormModule{
		Name: "Форма", Kind: "object", EntityName: ent.Name, LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{
			{Name: "Флаг", TypeRef: "bool"},
			{Name: "Текст", TypeRef: "string"},
			{Name: "СсылкаНаСклад", TypeRef: "CatalogRef.Склад"},
		},
	}
	values := map[string]string{"Флаг": "false", "Текст": " пробелы и <текст> ", "СсылкаНаСклад": "ref-id"}
	page := отрисоватьСУсловиями(t, ent, form, values)
	_, rest, ok := strings.Cut(page, `<script type="application/json" id="ob-managed-config">`)
	if !ok {
		t.Fatal("no managed config")
	}
	configJSON, _, _ := strings.Cut(rest, "</script>")
	var config struct {
		Values map[string]string `json:"formAttrValues"`
	}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatal(err)
	}
	for name, want := range values {
		if got, ok := config.Values[name]; !ok || got != want {
			t.Errorf("initial %s = %q, present=%v; want %q", name, got, ok, want)
		}
	}
}
