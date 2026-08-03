package configcheck

// Гигиена секретов (план 83): ищем значения, записанные в конфигурации открытым
// текстом там, где ожидается ссылка.
//
// Проверка предупреждающая, а не блокирующая: конфигурация с открытым ключом
// работоспособна, и объявлять её ошибкой значило бы сломать существующие
// проекты. Но знать об этом администратор должен — файлы конфигурации едут в
// git, в экспорт .obz и в поставку клиенту.

import (
	"strings"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/secrets"
)

// CheckSecretHygiene возвращает предупреждения о секретах, записанных значением.
// proj может быть nil — тогда проверяются только настройки app.yaml.
func CheckSecretHygiene(appCfg *project.AppConfig, proj *project.Project) []Issue {
	var warnings []Issue
	warn := func(file, object, field, value string) {
		if secrets.Classify(value) != secrets.KindPlain || secrets.ContainsRef(value) {
			return
		}
		warnings = append(warnings, Issue{
			File:   file,
			Object: object,
			Kind:   "Секрет",
			Code:   "secret.plaintext",
			// Путь в сообщении полный: текстовый вывод `onebase check` печатает
			// Message, но не Object, а «api_key: открытым текстом» без имени
			// endpoint'а не подсказывает, куда идти править.
			Message: object + "." + field + ": секрет записан открытым текстом",
			SuggestedFix: "Вынесите значение из конфигурации: env:ИМЯ (переменная окружения), " +
				"file:/путь (docker/k8s secret) или enc:… (onebase secret encrypt). " +
				"Открытый секрет уезжает в git, в экспорт конфигурации и в дамп бэкапа.",
		})
	}

	if appCfg != nil {
		const appFile = "config/app.yaml"
		if appCfg.Email != nil {
			warn(appFile, "email", "smtp_password", appCfg.Email.SMTPPass)
		}
		if appCfg.LLM != nil {
			for _, ep := range appCfg.LLM.Endpoints {
				warn(appFile, "llm."+ep.Name, "api_key", ep.APIKey)
				for name, v := range ep.Headers {
					if looksSecretHeader(name) {
						warn(appFile, "llm."+ep.Name, "headers."+name, v)
					}
				}
			}
		}
		if appCfg.Backup != nil && appCfg.Backup.S3 != nil {
			warn(appFile, "backup.s3", "secret_key", appCfg.Backup.S3.SecretKey)
			warn(appFile, "backup.s3", "access_key", appCfg.Backup.S3.AccessKey)
		}
		if appCfg.FileStorage != nil && appCfg.FileStorage.S3 != nil {
			warn(appFile, "file_storage.s3", "secret_key", appCfg.FileStorage.S3.SecretKey)
			warn(appFile, "file_storage.s3", "access_key", appCfg.FileStorage.S3.AccessKey)
		}
		for _, h := range appCfg.Webhooks {
			for name, v := range h.Headers {
				if looksSecretHeader(name) {
					warn(appFile, "webhook."+h.Name, "headers."+name, v)
				}
			}
		}
	}

	if proj != nil {
		for _, s := range proj.HTTPServices {
			// SecretRaw хранит значение до раскрытия ссылок; для сервисов,
			// собранных программно, его нет — тогда смотрим на Secret.
			value := s.SecretRaw
			if value == "" {
				value = s.Secret
			}
			warn("services", s.Name, "secret", value)
		}
		for _, in := range proj.Intakes {
			warn("intake", in.Name, "secret", in.Secret)
		}
	}
	return warnings
}

// looksSecretHeader отсеивает служебные заголовки (Content-Type, Accept) от
// тех, что переносят секрет. Точного списка не существует — ориентируемся на
// имя: заголовок с «auth», «token», «key», «secret», «signature» или
// «password» почти всегда несёт учётные данные.
func looksSecretHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, marker := range []string{"auth", "token", "key", "secret", "signature", "password"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}
