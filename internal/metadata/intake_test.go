package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIntakeNormalizeDefaults(t *testing.T) {
	in := &Intake{Name: "  SiteLead ", DLQ: IntakeDLQ{On: []string{"Handler_Error"}}}
	in.Normalize()
	if in.Name != "SiteLead" {
		t.Errorf("name не тримнут: %q", in.Name)
	}
	if in.Transport != IntakeTransportHTTP {
		t.Errorf("transport по умолчанию должен быть http, получено %q", in.Transport)
	}
	if in.Idempotency.Key != "event_id" {
		t.Errorf("ключ по умолчанию должен быть event_id, получено %q", in.Idempotency.Key)
	}
	if in.DLQ.On[0] != DLQHandlerError {
		t.Errorf("dlq.on не приведён к нижнему регистру: %q", in.DLQ.On[0])
	}
}

func TestIntakeTTLSeconds(t *testing.T) {
	cases := map[string]int64{"": 0, "30d": 2592000, "12h": 43200, "45m": 2700, "10s": 10}
	for ttl, want := range cases {
		in := &Intake{Idempotency: IntakeIdempotency{TTL: ttl}}
		got, err := in.TTLSeconds()
		if err != nil {
			t.Fatalf("TTLSeconds(%q): %v", ttl, err)
		}
		if got != want {
			t.Errorf("TTLSeconds(%q)=%d, ожидалось %d", ttl, got, want)
		}
	}
	if _, err := (&Intake{Idempotency: IntakeIdempotency{TTL: "30x"}}).TTLSeconds(); err == nil {
		t.Error("ожидалась ошибка на неизвестной единице")
	}
}

func TestIntakeValidate(t *testing.T) {
	good := &Intake{Name: "SiteLead", Transport: "http", Endpoint: "/hs/site/lead", Handler: "SiteLead",
		Idempotency: IntakeIdempotency{Key: "event_id"}}
	good.Normalize()
	if err := good.Validate(); err != nil {
		t.Fatalf("валидный шлюз не прошёл: %v", err)
	}

	bad := []*Intake{
		{Transport: "http", Endpoint: "/x", Handler: "H"},                                                                                 // нет имени
		{Name: "A", Transport: "http", Handler: "H"},                                                                                      // http без endpoint
		{Name: "A", Transport: "queue", Endpoint: "/x", Handler: "H"},                                                                     // неизвестный transport
		{Name: "A", Transport: "http", Endpoint: "/x"},                                                                                    // нет handler
		{Name: "A", Transport: "http", Endpoint: "/x", Handler: "H", DLQ: IntakeDLQ{On: []string{"nope"}}},                                // плохой dlq.on
		{Name: "A", Transport: "http", Endpoint: "/x", Handler: "H", Idempotency: IntakeIdempotency{Scope: []string{""}}},                 // пустой scope
		{Name: "A", Transport: "http", Endpoint: "/x", Handler: "H", Idempotency: IntakeIdempotency{Scope: []string{"source", "source"}}}, // дубль scope
	}
	for i, in := range bad {
		in.Normalize()
		if err := in.Validate(); err == nil {
			t.Errorf("случай %d: ожидалась ошибка валидации", i)
		}
	}
}

func TestIntakeQuarantineOn(t *testing.T) {
	empty := &Intake{}
	empty.Normalize()
	if !empty.QuarantineOn(DLQHandlerError) {
		t.Error("пустой dlq.on должен карантинить любую причину")
	}
	specific := &Intake{DLQ: IntakeDLQ{On: []string{DLQHandlerError}}}
	specific.Normalize()
	if !specific.QuarantineOn(DLQHandlerError) {
		t.Error("handler_error должен карантиниться")
	}
	if specific.QuarantineOn(DLQSchemaMismatch) {
		t.Error("schema_mismatch не в списке — не должен карантиниться")
	}
}

func TestLoadIntakeDir(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: SiteLead
transport: http
endpoint: /hs/site/lead
schema_version: "1"
handler: SiteLead
idempotency:
  key: event_id
  scope: [source, aggregate]
  ttl: 30d
dlq:
  on: [handler_error, schema_mismatch]
  max_retries: 3
`
	if err := os.WriteFile(filepath.Join(dir, "sitelead.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadIntakeDir(dir)
	if err != nil {
		t.Fatalf("LoadIntakeDir: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ожидался 1 шлюз, получено %d", len(got))
	}
	in := got[0]
	if in.Name != "SiteLead" || in.Endpoint != "/hs/site/lead" || len(in.Idempotency.Scope) != 2 {
		t.Fatalf("поля разобраны неверно: %+v", in)
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("загруженный шлюз не прошёл валидацию: %v", err)
	}

	// Отсутствующий каталог — не ошибка.
	none, err := LoadIntakeDir(filepath.Join(dir, "nope"))
	if err != nil || none != nil {
		t.Fatalf("отсутствующий каталог должен дать (nil,nil), получено (%v,%v)", none, err)
	}
}

func TestIntakeValidateWS(t *testing.T) {
	good := &Intake{Name: "Hub", Transport: "ws", URL: "wss://hub.example.com/stream", Handler: "Hub",
		Auth: "token", Secret: "${env:WS_TOKEN}"}
	good.Normalize()
	if err := good.Validate(); err != nil {
		t.Fatalf("валидный ws-шлюз не прошёл: %v", err)
	}
	if good.Reconnect.Initial != 1 || good.Reconnect.Max != 60 {
		t.Fatalf("дефолты reconnect не применены: %+v", good.Reconnect)
	}

	bad := []*Intake{
		{Name: "A", Transport: "ws", Handler: "H"},                                                        // нет url
		{Name: "A", Transport: "ws", URL: "https://hub", Handler: "H"},                                    // не ws-схема
		{Name: "A", Transport: "ws", URL: "wss://hub", Handler: "H", Endpoint: "/hs/x"},                   // endpoint не применим
		{Name: "A", Transport: "ws", URL: "wss://hub", Handler: "H", Auth: "hmac", Secret: "s"},           // hmac не применим
		{Name: "A", Transport: "ws", URL: "wss://hub", Handler: "H", Reconnect: IntakeReconnect{Max: -5}}, // max < initial (после дефолта initial=1)
	}
	for i, in := range bad {
		in.Normalize()
		if err := in.Validate(); err == nil {
			t.Errorf("ws-случай %d: ожидалась ошибка валидации", i)
		}
	}
}
