package ui

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// TestManagedRefValueBehaviorFromFormEvent соединяет серверную публичную точку
// входа с настоящим applyValues из managed.js. Текущий сериализатор обычно
// сворачивает *interpreter.Ref в UUID, но клиент обязан принимать и его
// документированную JSON-форму {UUID,Name,Type,Kind}: именно она раньше
// превращалась в "[object Object]".
func TestManagedRefValueBehaviorFromFormEvent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed reference value regression test")
	}

	srv, entity, target := setupRefOptionEventServer(t)
	body := url.Values{
		"_element":     {"ПолеНаименование"},
		"_event":       {string(metadata.FormEventOnChange)},
		"_kind":        {"object"},
		"Наименование": {"Заказ 1"},
	}
	rec := executeFormEvent(t, srv, entity, body)

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode public form-event response: %v; body=%s", err, rec.Body.String())
	}
	values, ok := response["values"].(map[string]any)
	if !ok {
		t.Fatalf("form-event response has no values object: %#v", response)
	}
	if got := refValueString(values["Склад"]); got != target.String() {
		t.Fatalf("form-event did not return assigned reference: got %q, want %s", got, target)
	}
	refOptions, ok := response["refOptions"].(map[string]any)
	if !ok || refOptions["Склад"] == nil {
		t.Fatalf("form-event did not return safe reference label: %#v", response["refOptions"])
	}

	// Воспроизводим точную проблемную wire-форму поверх настоящего ответа: ID и
	// безопасная подпись по-прежнему пришли из публичного form-event.
	values["Склад"] = map[string]any{
		"UUID": target.String(),
		"Name": refOptionsTargetName,
		"Type": "Склад",
		"Kind": "catalog",
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, "--test", "static/managed_ref_value_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_FORM_EVENT_RESPONSE_B64="+base64.StdEncoding.EncodeToString(payload))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node managed reference value test: %v\n%s", err, output)
	}
}
