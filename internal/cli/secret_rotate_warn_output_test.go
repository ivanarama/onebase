package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Что именно печатает предупреждение ротации.
//
// Отбор проверяется в secret_rotate_config_test.go, здесь — сам вывод: печать
// идёт через secrets.Describe, и главное свойство («секрет не показывается
// никогда») должно держаться тестом, а не только чтением кода. Перехватывать
// есть чем — captureStdout в этом же пакете подменяет os.Stdout.

func TestRotateWarningNeverPrintsSecret(t *testing.T) {
	oldKey, newKey := testKey(t), testKey(t)
	dir := t.TempDir()
	const plain = "пароль-smtp-секретный"
	enc, err := oldKey.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	writeAppYAML(t, dir, enc, "sk-ключ-ии-открытым-текстом")

	out, err := captureStdout(t, func() error {
		warnConfigSecretsUnderOldKey(dir, false, newKey)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "email.smtp_password") {
		t.Fatalf("путь секрета не назван — предупреждение бесполезно:\n%s", out)
	}
	if strings.Contains(out, plain) {
		t.Errorf("расшифрованный секрет в выводе:\n%s", out)
	}
	if strings.Contains(out, enc) {
		t.Errorf("enc:-значение целиком в выводе:\n%s", out)
	}
	// Открытый текст — не enc:, ротации он не касается: смена ключа его не
	// ломает, а печатать значение тем более незачем.
	if strings.Contains(out, "sk-ключ-ии-открытым-текстом") {
		t.Errorf("открытый ключ ИИ попал в вывод ротации:\n%s", out)
	}
}

// База с конфигурацией в БД: файлов нет, а каталог — временная выгрузка,
// которая исчезнет вместе с командой. Назвать его значило бы отправить
// администратора искать файл, которого не существует.
func TestRotateWarningForDBConfigPointsToConfigurator(t *testing.T) {
	oldKey, newKey := testKey(t), testKey(t)
	dir := t.TempDir()
	enc, err := oldKey.Encrypt("пароль-smtp")
	if err != nil {
		t.Fatal(err)
	}
	writeAppYAML(t, dir, enc, "")

	out, err := captureStdout(t, func() error {
		warnConfigSecretsUnderOldKey(dir, true, newKey)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, dir) {
		t.Errorf("временный каталог выгрузки назван администратору:\n%s", out)
	}
	if !strings.Contains(out, "конфигурат") {
		t.Errorf("не сказано, где править (ожидался конфигуратор):\n%s", out)
	}
	if !strings.Contains(out, "email.smtp_password") {
		t.Errorf("путь секрета не назван:\n%s", out)
	}
}

// Каталога конфигурации нет (`--sqlite` без проекта) — ложная тревога здесь
// хуже молчания: администратору нечего проверять.
func TestRotateWarningSilentWithoutConfig(t *testing.T) {
	for _, dir := range []string{"", filepath.Join(t.TempDir(), "нет-такого")} {
		out, err := captureStdout(t, func() error {
			warnConfigSecretsUnderOldKey(dir, false, testKey(t))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if out != "" {
			t.Errorf("лишний вывод при каталоге %q:\n%s", dir, out)
		}
	}
}
