package llm

import (
	"strings"
	"testing"
)

const unresolvableRef = "enc:AQIDBAUGBwgJCgsMDQ4PEBESExQV" // валидный по форме, но мастер-ключа нет

// Нет мастер-ключа → ИИ отключается с внятным сообщением (fail-closed), а не
// уходит к провайдеру с пустым ключом.
func TestResolveFailsClosedWithoutMasterKey(t *testing.T) {
	c := Config{
		Enabled:   true,
		Endpoints: []Endpoint{{Name: "z_ai", Kind: KindAnthropic, APIKey: unresolvableRef}},
		Models:    []Model{{Name: "glm-4.6", Endpoint: "z_ai"}},
		Profiles:  []Profile{{Task: "чат", Models: []string{"glm-4.6"}}},
	}
	_, err := c.Resolve("чат")
	if err == nil {
		t.Fatal("ожидалась ошибка: секрет не разыменовывается")
	}
	msg := err.Error()
	for _, want := range []string{"секрет не разыменован", "z_ai", "мастер-ключ"} {
		if !strings.Contains(msg, want) {
			t.Errorf("в сообщении нет %q: %s", want, msg)
		}
	}
}

// Модель с неразыменованным секретом выбывает из цепочки, а следующая в профиле
// работает: смысл цепочки как раз в том, чтобы уйти на другого провайдера.
func TestResolveSkipsEndpointWithUnresolvableSecret(t *testing.T) {
	t.Setenv("OB_TEST_LLM_LOCAL_KEY", "локальный-ключ")
	c := Config{
		Enabled: true,
		Endpoints: []Endpoint{
			{Name: "облако", Kind: KindAnthropic, APIKey: unresolvableRef},
			{Name: "локально", Kind: KindCompatible, APIKey: "env:OB_TEST_LLM_LOCAL_KEY"},
		},
		Models: []Model{
			{Name: "облачная", Endpoint: "облако"},
			{Name: "локальная", Endpoint: "локально"},
		},
		Profiles: []Profile{{Task: "чат", Models: []string{"облачная", "локальная"}}},
	}
	got, err := c.Resolve("чат")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].Model.Name != "локальная" {
		t.Fatalf("ожидалась только локальная модель, получено %+v", got)
	}
	if got[0].Endpoint.APIKey != "локальный-ключ" {
		t.Fatalf("ключ не разыменован: %q", got[0].Endpoint.APIKey)
	}
}

// Ссылка на секрет — не секрет: Redacted показывает её админу как есть,
// маскируя только ключи, записанные значением.
func TestRedactedKeepsSecretRefsVisible(t *testing.T) {
	c := Config{Endpoints: []Endpoint{
		{Name: "a", APIKey: "sk-1234567890"},
		{Name: "b", APIKey: "${env:OB_KEY}"},
		{Name: "c", APIKey: "file:/run/secrets/llm"},
		{Name: "d", APIKey: unresolvableRef},
	}}
	got := c.Redacted()
	if got.Endpoints[0].APIKey != "****7890" {
		t.Errorf("открытый ключ должен маскироваться: %q", got.Endpoints[0].APIKey)
	}
	for i, want := range map[int]string{1: "${env:OB_KEY}", 2: "file:/run/secrets/llm", 3: unresolvableRef} {
		if got.Endpoints[i].APIKey != want {
			t.Errorf("ссылка должна остаться видимой: %q, ожидалось %q", got.Endpoints[i].APIKey, want)
		}
	}
}
