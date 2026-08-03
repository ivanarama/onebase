package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/secrets"
	"github.com/ivantit66/onebase/internal/storage"
)

func testKey(t *testing.T) *secrets.Key {
	t.Helper()
	k, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "secret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestStoreSecretPaths(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	key := testKey(t)
	ref, err := key.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}

	// Произвольный ключ служебной таблицы.
	if err := storeSecret(ctx, db, "_settings:моя.настройка", ref); err != nil {
		t.Fatalf("_settings: %v", err)
	}
	got, ok, err := db.GetSetting(ctx, "моя.настройка")
	if err != nil || !ok || got != ref {
		t.Fatalf("_settings: got=%q ok=%v err=%v", got, ok, err)
	}

	// Токен плана обмена: в базе лежит ссылка…
	if err := storeSecret(ctx, db, "exchange.token.обмен", ref); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	stored, ok, err := db.GetSetting(ctx, "exchange.token.обмен")
	if err != nil || !ok || stored != ref {
		t.Fatalf("exchange: в базе %q ok=%v err=%v", stored, ok, err)
	}
	// …а читатель получает значение — при заданном мастер-ключе.
	t.Setenv(secrets.EnvMasterKey, key.Hex())
	tok, err := db.GetExchangeToken(ctx, "обмен")
	if err != nil || tok != "значение" {
		t.Fatalf("exchange: token=%q err=%v", tok, err)
	}

	// Ключ ИИ — только для существующего endpoint.
	if err := storeSecret(ctx, db, "llm.нетути.api_key", ref); err == nil {
		t.Fatal("несуществующий endpoint должен давать ошибку")
	}
	if err := db.SaveLLMConfig(ctx, llm.Config{
		Enabled:   true,
		Endpoints: []llm.Endpoint{{Name: "z_ai", Kind: llm.KindAnthropic, APIKey: "sk-открытый"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storeSecret(ctx, db, "llm.z_ai.api_key", ref); err != nil {
		t.Fatalf("llm: %v", err)
	}
	cfg, err := db.GetLLMConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoints[0].APIKey != ref {
		t.Fatalf("ключ не заменён ссылкой: %q", cfg.Endpoints[0].APIKey)
	}

	if err := storeSecret(ctx, db, "чего.то.не.то", ref); err == nil {
		t.Fatal("неизвестный путь должен давать ошибку")
	}
}

// Сквозная проверка из плана 83: положили ключ ИИ зашифрованным — в базе лежит
// enc:, ИИ работает при заданном мастер-ключе и отключается с понятным
// сообщением, когда ключ убрали.
func TestSecretSetLLMKeyEndToEnd(t *testing.T) {
	const apiKey = "sk-настоящий-ключ-провайдера"
	ctx := context.Background()
	db := testDB(t)
	key := testKey(t)

	if err := db.SaveLLMConfig(ctx, llm.Config{
		Enabled:   true,
		Endpoints: []llm.Endpoint{{Name: "z_ai", Kind: llm.KindAnthropic}},
		Models:    []llm.Model{{Name: "glm-4.6", Endpoint: "z_ai"}},
		Profiles:  []llm.Profile{{Task: "чат", Models: []string{"glm-4.6"}}},
	}); err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeSecret(ctx, db, "llm.z_ai.api_key", ref); err != nil {
		t.Fatal(err)
	}

	// В базе — шифротекст. Именно эту таблицу целиком уносит обычный бэкап.
	raw, ok, err := db.GetSetting(ctx, "llm.config")
	if err != nil || !ok {
		t.Fatalf("llm.config: ok=%v err=%v", ok, err)
	}
	if strings.Contains(raw, apiKey) {
		t.Fatalf("ключ лежит в базе открытым текстом: %s", raw)
	}
	if !strings.Contains(raw, "enc:") {
		t.Fatalf("ожидалась enc:-ссылка, получено: %s", raw)
	}

	cfg, err := db.GetLLMConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// С мастер-ключом ИИ работает.
	t.Setenv(secrets.EnvMasterKey, key.Hex())
	resolved, err := cfg.Resolve("чат")
	if err != nil {
		t.Fatalf("Resolve при заданном ключе: %v", err)
	}
	if got := resolved[0].Endpoint.APIKey; got != apiKey {
		t.Fatalf("ключ не разыменован: %q", got)
	}

	// Без него — отключается, и сообщение объясняет, чего не хватает.
	t.Setenv(secrets.EnvMasterKey, "")
	_, err = cfg.Resolve("чат")
	if err == nil {
		t.Fatal("без мастер-ключа ИИ обязан отключиться")
	}
	if !strings.Contains(err.Error(), "мастер-ключ") {
		t.Fatalf("сообщение не объясняет причину: %v", err)
	}
}

// Повторная ротация не должна перешифровывать уже перешифрованное — иначе
// прерванный прогон нельзя было бы просто повторить.
func TestReencryptIsIdempotent(t *testing.T) {
	oldKey, newKey := testKey(t), testKey(t)
	ref, err := oldKey.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}

	got, err := reencrypt(ref, oldKey, newKey)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("значение под старым ключом должно перешифровываться")
	}
	plain, err := newKey.Decrypt(got)
	if err != nil || plain != "значение" {
		t.Fatalf("после ротации: %q, %v", plain, err)
	}

	again, err := reencrypt(got, oldKey, newKey)
	if err != nil {
		t.Fatal(err)
	}
	if again != "" {
		t.Fatal("значение уже под новым ключом — повторно шифровать не нужно")
	}

	for _, v := range []string{"", "sk-открытый", "env:VAR", "file:/run/secrets/x"} {
		if got, err := reencrypt(v, oldKey, newKey); err != nil || got != "" {
			t.Errorf("не-enc значение %q: got=%q err=%v", v, got, err)
		}
	}
}

func TestRotateLLMConfig(t *testing.T) {
	oldKey, newKey := testKey(t), testKey(t)
	encKey, err := oldKey.Encrypt("sk-секрет")
	if err != nil {
		t.Fatal(err)
	}
	encHdr, err := oldKey.Encrypt("Bearer токен")
	if err != nil {
		t.Fatal(err)
	}
	cfg := llm.Config{
		Enabled: true,
		Endpoints: []llm.Endpoint{
			{Name: "облако", APIKey: encKey, Headers: map[string]string{"X-Auth": encHdr}},
			{Name: "локально", APIKey: "env:OB_LOCAL"}, // ссылку на окружение трогать нельзя
		},
	}
	raw, err := cfg.JSON()
	if err != nil {
		t.Fatal(err)
	}

	n, out, err := rotateLLMConfig(raw, oldKey, newKey)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("ожидалось 2 перешифрованных значения, получено %d", n)
	}
	got, err := llm.ParseConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := newKey.Decrypt(got.Endpoints[0].APIKey); err != nil || v != "sk-секрет" {
		t.Fatalf("ключ после ротации: %q, %v", v, err)
	}
	if v, err := newKey.Decrypt(got.Endpoints[0].Headers["X-Auth"]); err != nil || v != "Bearer токен" {
		t.Fatalf("заголовок после ротации: %q, %v", v, err)
	}
	if got.Endpoints[1].APIKey != "env:OB_LOCAL" {
		t.Fatalf("ссылка на окружение не должна меняться: %q", got.Endpoints[1].APIKey)
	}

	// Второй прогон — уже нечего делать.
	n, _, err = rotateLLMConfig(out, oldKey, newKey)
	if err != nil || n != 0 {
		t.Fatalf("повторная ротация: n=%d err=%v", n, err)
	}
}

func TestBaseSecretsInventory(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	key := testKey(t)
	ref, err := key.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveLLMConfig(ctx, llm.Config{Endpoints: []llm.Endpoint{
		{Name: "z_ai", APIKey: "sk-открытым-текстом"},
		{Name: "локально", APIKey: "env:OB_LOCAL"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveExchangeToken(ctx, "обмен", ref); err != nil {
		t.Fatal(err)
	}

	rows, err := baseSecrets(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, r := range rows {
		found[r.Path] = r.Kind
	}
	if got := found["llm.z_ai.api_key"]; got != "ОТКРЫТЫЙ ТЕКСТ" {
		t.Errorf("открытый ключ должен быть виден как проблема: %q", got)
	}
	if got := found["llm.локально.api_key"]; got != "переменная окружения" {
		t.Errorf("ссылка на окружение: %q", got)
	}
	if got := found["exchange.token.обмен"]; !strings.Contains(got, key.ID()) {
		t.Errorf("зашифрованный токен должен называть отпечаток ключа: %q", got)
	}
	// Значения секретов в инвентаризацию не попадают никогда.
	for _, r := range rows {
		if strings.Contains(r.Kind, "sk-открытым-текстом") || strings.Contains(r.Kind, "значение") {
			t.Fatalf("значение секрета утекло в отчёт: %+v", r)
		}
	}
}

func TestConfigSecretsInventory(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	appYAML := `name: Demo
email:
  smtp_host: smtp.example.org
  smtp_user: robot
  smtp_password: "env:OB_SMTP_PASS"
llm:
  endpoints:
    - name: z_ai
      kind: anthropic
      api_key: "sk-открытым-текстом"
backup:
  enabled: true
  s3:
    endpoint: s3.example.org
    bucket: b
    access_key: "${env:OB_S3_KEY}"
    secret_key: "секрет-открытым-текстом"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte(appYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	rows, err := configSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, r := range rows {
		found[r.Path] = r.Kind
	}
	want := map[string]string{
		"email.smtp_password":  "переменная окружения",
		"llm.z_ai.api_key":     "ОТКРЫТЫЙ ТЕКСТ",
		"backup.s3.access_key": "переменная окружения",
		"backup.s3.secret_key": "ОТКРЫТЫЙ ТЕКСТ",
	}
	for path, kind := range want {
		if got := found[path]; got != kind {
			t.Errorf("%s: вид %q, ожидался %q", path, got, kind)
		}
	}
}
