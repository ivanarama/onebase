package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/secrets"
)

// Ротация мастер-ключа и enc:-значения в файлах конфигурации.
//
// `secret rotate` перешифровывает только базу — и правильно делает: YAML лежит
// в git и в поставке клиенту, переписывать его за администратора нельзя. Но
// `secret encrypt` прямо предлагает класть enc: в YAML, а ротация заканчивается
// советом сменить мастер-ключ процесса. Без предупреждения такие значения после
// смены ключа просто перестают разворачиваться, и узнаётся это по отвалившейся
// почте, а не по выводу команды.

// writeAppYAML кладёт config/app.yaml с заданным паролем SMTP и ключом ИИ.
func writeAppYAML(t *testing.T, dir, smtpPass, llmKey string) {
	t.Helper()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name: Demo\n" +
		"email:\n  smtp_host: smtp.example.org\n  smtp_user: robot\n" +
		"  smtp_password: \"" + smtpPass + "\"\n" +
		"llm:\n  endpoints:\n    - name: z_ai\n      kind: anthropic\n" +
		"      api_key: \"" + llmKey + "\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stalePaths(t *testing.T, dir string, key *secrets.Key) map[string]string {
	t.Helper()
	rows, err := configSecretsNotUnderKey(dir, key)
	if err != nil {
		t.Fatalf("configSecretsNotUnderKey: %v", err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Path] = secrets.Describe(r.Value)
	}
	return out
}

// Значение под старым ключом обязано быть названо: это и есть та часть работы,
// которую ротация не делает, а администратор считает сделанной.
func TestRotateFindsConfigSecretsUnderOldKey(t *testing.T) {
	oldKey, newKey := testKey(t), testKey(t)
	dir := t.TempDir()
	encPass, err := oldKey.Encrypt("пароль-smtp")
	if err != nil {
		t.Fatal(err)
	}
	writeAppYAML(t, dir, encPass, "env:OB_LLM_KEY")

	stale := stalePaths(t, dir, newKey)
	if _, ok := stale["email.smtp_password"]; !ok {
		t.Fatalf("значение под старым ключом не найдено: %+v", stale)
	}
	if _, ok := stale["llm.z_ai.api_key"]; ok {
		t.Fatalf("ссылка на окружение смены ключа не боится, предупреждать о ней незачем: %+v", stale)
	}
	// Наружу идёт только вид значения — сам секрет не раскрывается никогда.
	if d := stale["email.smtp_password"]; strings.Contains(d, "пароль-smtp") {
		t.Fatalf("секрет утёк в предупреждение: %q", d)
	}
}

// Всё уже перешифровано вручную — предупреждать не о чем. Иначе повторный
// прогон ротации пугал бы администратора тем, что он только что починил.
func TestRotateSilentWhenConfigAlreadyUnderNewKey(t *testing.T) {
	newKey := testKey(t)
	dir := t.TempDir()
	encPass, err := newKey.Encrypt("пароль-smtp")
	if err != nil {
		t.Fatal(err)
	}
	writeAppYAML(t, dir, encPass, "sk-открытым-текстом")

	if stale := stalePaths(t, dir, newKey); len(stale) != 0 {
		t.Fatalf("лишнее предупреждение: %+v", stale)
	}
}

// Отпечаток третьего ключа — тоже повод предупредить: после смены мастер-ключа
// такое значение не откроется ничем, и «оно не под новым ключом» ровно то, что
// администратору надо знать. Отпечаток при этом называется — по нему и понятно,
// каким ключом значение шифровали.
func TestRotateFindsConfigSecretsUnderThirdKey(t *testing.T) {
	third, newKey := testKey(t), testKey(t)
	dir := t.TempDir()
	encPass, err := third.Encrypt("пароль-smtp")
	if err != nil {
		t.Fatal(err)
	}
	writeAppYAML(t, dir, encPass, "")

	stale := stalePaths(t, dir, newKey)
	if d := stale["email.smtp_password"]; !strings.Contains(d, third.ID()) {
		t.Fatalf("значение под третьим ключом не названо с отпечатком: %q", d)
	}
}

// Нечитаемая конфигурация — ошибка, а не «предупреждать не о чем»: молчание
// здесь неотличимо от «всё в порядке», и администратор сменил бы ключ спокойно.
func TestRotateReportsUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte("name: [не YAML\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := configSecretsNotUnderKey(dir, testKey(t)); err == nil {
		t.Fatal("нечитаемая конфигурация обязана давать ошибку, а не пустой список")
	}
}
