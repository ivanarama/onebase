package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/webhook"
)

func hygieneObjects(warnings []Issue) map[string]string {
	out := map[string]string{}
	for _, w := range warnings {
		out[strings.TrimSuffix(w.Message, ": секрет записан открытым текстом")] = w.Code
	}
	return out
}

func TestCheckSecretHygieneFindsPlaintext(t *testing.T) {
	cfg := &project.AppConfig{
		Email: &project.EmailConfig{SMTPPass: "пароль-открытым-текстом"},
		LLM: &llm.Config{Endpoints: []llm.Endpoint{
			{Name: "облако", APIKey: "sk-открытым-текстом"},
			{Name: "локально", APIKey: "env:OB_LOCAL"},
		}},
		Backup: &project.BackupConfig{S3: &project.S3Config{
			AccessKey: "${env:OB_S3_KEY}",
			SecretKey: "секрет-открытым-текстом",
		}},
		Webhooks: []webhook.Config{{
			Name: "tg",
			Headers: map[string]string{
				"Content-Type":  "application/json", // не секрет — не должен попасть в отчёт
				"Authorization": "Bearer открытый-токен",
			},
		}},
	}

	got := hygieneObjects(CheckSecretHygiene(cfg, nil))
	for _, want := range []string{
		"email.smtp_password",
		"llm.облако.api_key",
		"backup.s3.secret_key",
		"webhook.tg.headers.Authorization",
	} {
		if got[want] != "secret.plaintext" {
			t.Errorf("не найдено предупреждение для %s (получено: %v)", want, got)
		}
	}
	for _, unwanted := range []string{
		"llm.локально.api_key",            // ссылка на окружение
		"backup.s3.access_key",            // ${env:...}
		"webhook.tg.headers.Content-Type", // служебный заголовок
	} {
		if _, bad := got[unwanted]; bad {
			t.Errorf("ложное предупреждение для %s", unwanted)
		}
	}
}

// Подсказка должна называть все три способа вынести секрет — иначе она
// бесполезна тому, кто видит это сообщение впервые.
func TestCheckSecretHygieneSuggestsFix(t *testing.T) {
	cfg := &project.AppConfig{Email: &project.EmailConfig{SMTPPass: "открытым-текстом"}}
	w := CheckSecretHygiene(cfg, nil)
	if len(w) != 1 {
		t.Fatalf("ожидалось одно предупреждение, получено %d", len(w))
	}
	for _, want := range []string{"env:", "file:", "enc:"} {
		if !strings.Contains(w[0].SuggestedFix, want) {
			t.Errorf("в подсказке нет %q: %s", want, w[0].SuggestedFix)
		}
	}
	// Само значение в отчёт не попадает.
	if strings.Contains(w[0].Message+w[0].SuggestedFix, "открытым-текстом") {
		t.Fatal("значение секрета попало в текст предупреждения")
	}
}

func TestCheckSecretHygieneCleanConfig(t *testing.T) {
	cfg := &project.AppConfig{
		Email: &project.EmailConfig{SMTPPass: "file:/run/secrets/smtp"},
		LLM:   &llm.Config{Endpoints: []llm.Endpoint{{Name: "облако", APIKey: "${env:OB_KEY}"}}},
	}
	if w := CheckSecretHygiene(cfg, nil); len(w) != 0 {
		t.Fatalf("чистая конфигурация не должна давать предупреждений: %+v", w)
	}
	if w := CheckSecretHygiene(nil, nil); len(w) != 0 {
		t.Fatalf("без конфигурации предупреждений быть не должно: %+v", w)
	}
}
