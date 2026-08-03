package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testResolver(t *testing.T, env map[string]string, opts ...Option) *Resolver {
	t.Helper()
	base := []Option{WithEnv(func(k string) string { return env[k] })}
	return New(append(base, opts...)...)
}

func TestResolvePlainValue(t *testing.T) {
	r := testResolver(t, nil)
	got, err := r.Resolve("sk-открытый-ключ")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "sk-открытый-ключ" {
		t.Fatalf("значение без ссылки должно возвращаться как есть, получено %q", got)
	}
}

func TestResolveEnv(t *testing.T) {
	r := testResolver(t, map[string]string{"OB_KEY": "значение-из-окружения"})

	cases := map[string]string{
		"env:OB_KEY":                 "значение-из-окружения",
		"  env:OB_KEY  ":             "значение-из-окружения",
		"${env:OB_KEY}":              "значение-из-окружения",
		"${env: OB_KEY }":            "значение-из-окружения",
		"Bearer ${env:OB_KEY}":       "Bearer значение-из-окружения",
		"https://h/${env:OB_KEY}/go": "https://h/значение-из-окружения/go",
	}
	for in, want := range cases {
		got, err := r.Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("Resolve(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

// Отсутствующая переменная разыменовывается в пустую строку — так вело себя
// разыменование ${env:...} до плана 83, и конфигурации на это опираются.
func TestResolveEnvMissingIsEmptyNotError(t *testing.T) {
	r := testResolver(t, nil)
	for _, in := range []string{"env:НЕТ_ТАКОЙ", "${env:НЕТ_ТАКОЙ}"} {
		got, err := r.Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q): неожиданная ошибка %v", in, err)
		}
		if got != "" {
			t.Errorf("Resolve(%q) = %q, ожидалась пустая строка", in, got)
		}
	}
}

func TestResolveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "smtp.pass")
	if err := os.WriteFile(path, []byte("пароль из файла\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := testResolver(t, nil)

	got, err := r.Resolve("file:" + path)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "пароль из файла" {
		t.Fatalf("завершающий перевод строки должен срезаться, получено %q", got)
	}

	if _, err := r.Resolve("file:" + filepath.Join(dir, "нет.txt")); err == nil {
		t.Fatal("нечитаемый file: обязан возвращать ошибку, а не пустое значение")
	}
	if _, err := r.Resolve("file:"); err == nil {
		t.Fatal("пустой путь в file: обязан возвращать ошибку")
	}
}

func TestResolveEncRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt("sk-секрет-провайдера")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "enc:") {
		t.Fatalf("Encrypt должен возвращать enc:-ссылку, получено %q", ref)
	}
	if strings.Contains(ref, "sk-секрет") {
		t.Fatal("шифротекст не должен содержать исходное значение")
	}

	r := testResolver(t, nil, WithKey(key))
	got, err := r.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "sk-секрет-провайдера" {
		t.Fatalf("round-trip дал %q", got)
	}

	// Встроенная форма — секрет как часть URL.
	inline := "https://api.example/${" + ref + "}"
	got, err = r.Resolve(inline)
	if err != nil {
		t.Fatalf("Resolve(inline): %v", err)
	}
	if got != "https://api.example/sk-секрет-провайдера" {
		t.Fatalf("встроенная enc:-ссылка дала %q", got)
	}
}

// Нет мастер-ключа → enc: не разыменовывается с внятной ошибкой. Это и есть
// fail-closed: подсистема выключится, но сервер не упадёт.
func TestResolveEncWithoutMasterKey(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}

	r := testResolver(t, nil) // окружение пустое → ключа нет
	if r.HasKey() {
		t.Fatal("без переменных окружения ключа быть не должно")
	}
	_, err = r.Resolve(ref)
	if !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("ожидалась ErrNoMasterKey, получено %v", err)
	}
}

func TestResolveEncWrongKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := k1.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k2.Decrypt(ref); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("ожидалась ErrWrongKey, получено %v", err)
	}
}

func TestDecryptCorrupted(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}
	// Портим последний знак base64, сохраняя длину.
	body := strings.TrimPrefix(ref, "enc:")
	swapped := "A"
	if strings.HasSuffix(body, "A") {
		swapped = "B"
	}
	broken := "enc:" + body[:len(body)-1] + swapped
	if _, err := key.Decrypt(broken); err == nil {
		t.Fatal("испорченное значение обязано не расшифровываться")
	}

	if _, err := key.Decrypt("enc:не-base64!!"); err == nil {
		t.Fatal("неразбираемый base64 обязан давать ошибку")
	}
	if _, err := key.Decrypt("env:VAR"); !errors.Is(err, ErrNotEncrypted) {
		t.Fatalf("ожидалась ErrNotEncrypted, получено %v", err)
	}
}

func TestParseKeyForms(t *testing.T) {
	gen, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := gen.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}

	// Ключ, записанный hex — то, что печатает keygen.
	fromHex, err := ParseKey(gen.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if fromHex.ID() != gen.ID() {
		t.Fatalf("hex-запись дала другой ключ: %s vs %s", fromHex.ID(), gen.ID())
	}
	got, err := fromHex.Decrypt(ref)
	if err != nil || got != "значение" {
		t.Fatalf("расшифровка ключом из hex: %q, %v", got, err)
	}

	if _, err := ParseKey("   "); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("пустой ключ: ожидалась ErrNoMasterKey, получено %v", err)
	}
}

// Парольная фраза выводится в ключ детерминированно — иначе база, снятая с
// одного сервера, не открылась бы на другом.
func TestParseKeyPassphraseIsDeterministic(t *testing.T) {
	a, err := ParseKey("очень секретная фраза")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseKey("очень секретная фраза")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() {
		t.Fatalf("одна фраза дала разные ключи: %s vs %s", a.ID(), b.ID())
	}
	c, err := ParseKey("другая фраза")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID() == a.ID() {
		t.Fatal("разные фразы дали один ключ")
	}
}

func TestLoadKeySources(t *testing.T) {
	gen, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	if err := os.WriteFile(keyPath, []byte(gen.Hex()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Файл имеет приоритет над переменной.
	other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{EnvMasterKeyFile: keyPath, EnvMasterKey: other.Hex()}
	k, err := LoadKey(func(s string) string { return env[s] }, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if k.ID() != gen.ID() {
		t.Fatalf("должен был выиграть ключ из файла, получен %s", k.ID())
	}

	// Ключ не задан вовсе — это не ошибка.
	k, err = LoadKey(func(string) string { return "" }, os.ReadFile)
	if err != nil || k != nil {
		t.Fatalf("без ключа ожидалось (nil, nil), получено (%v, %v)", k, err)
	}

	// Нечитаемый файл ключа — ошибка.
	env = map[string]string{EnvMasterKeyFile: filepath.Join(dir, "нет.key")}
	if _, err := LoadKey(func(s string) string { return env[s] }, os.ReadFile); err == nil {
		t.Fatal("нечитаемый файл ключа обязан давать ошибку")
	}
}

func TestClassifyAndDescribe(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in   string
		want Kind
	}{
		{"", KindEmpty},
		{"   ", KindEmpty},
		{"sk-открытый", KindPlain},
		{"env:VAR", KindEnv},
		{"${env:VAR}", KindEnv},
		{"file:/run/secrets/x", KindFile},
		{ref, KindEnc},
		{"https://h/${env:TOKEN}", KindPlain}, // ссылка внутри — но целиком не ссылка
	}
	for _, c := range cases {
		if got := Classify(c.in); got != c.want {
			t.Errorf("Classify(%q) = %v, ожидалось %v", c.in, got, c.want)
		}
	}

	if !IsRef("env:VAR") || IsRef("sk-открытый") {
		t.Fatal("IsRef различает ссылку и открытое значение")
	}
	if !ContainsRef("https://h/${env:TOKEN}") || ContainsRef("https://h/token") {
		t.Fatal("ContainsRef различает встроенную ссылку")
	}
	if got := Describe("sk-открытый"); got != "ОТКРЫТЫЙ ТЕКСТ" {
		t.Fatalf("Describe открытого значения = %q", got)
	}
	if got := Describe(ref); !strings.Contains(got, key.ID()) {
		t.Fatalf("Describe enc: должен называть отпечаток ключа, получено %q", got)
	}
	if got := Describe("https://h/${env:TOKEN}"); got != "ссылка в составе значения" {
		t.Fatalf("Describe встроенной ссылки = %q", got)
	}
}

func TestMask(t *testing.T) {
	if got := Mask("sk-1234567890"); got != "****7890" {
		t.Errorf("Mask = %q", got)
	}
	if got := Mask("abc"); got != "****" {
		t.Errorf("Mask короткого = %q", got)
	}
	if got := Mask("env:VAR"); got != "env:VAR" {
		t.Errorf("ссылка не маскируется, получено %q", got)
	}
	if got := Mask(""); got != "" {
		t.Errorf("пустое значение = %q", got)
	}
}

func TestRefKeyID(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := key.Encrypt("значение")
	if err != nil {
		t.Fatal(err)
	}
	id, ok := RefKeyID(ref)
	if !ok || id != key.ID() {
		t.Fatalf("RefKeyID = %q, %v; ожидалось %q", id, ok, key.ID())
	}
	if _, ok := RefKeyID("env:VAR"); ok {
		t.Fatal("RefKeyID на не-enc значении обязан вернуть false")
	}
}
