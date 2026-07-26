package cli

import "fmt"

// checkDemoEnv реализует защиту демо-режима из замечания #11: если в
// app.yaml включён блок demo (demo.enabled=true), но переменная окружения
// ONEBASE_ENV не "demo" — отказываем в старте. Это блокирует сценарий
// «конфигурацию подняли на проде с включённым demo, ночью пришёл
// reset_schedule и стёр данные».
//
// Возвращает nil если env="demo", иначе — ошибку с подсказкой.
func checkDemoEnv(env string) error {
	if env == "demo" {
		return nil
	}
	return fmt.Errorf(
		"demo.enabled=true в app.yaml, но ONEBASE_ENV=%q. "+
			"Чтобы запустить демо-режим, установите ONEBASE_ENV=demo. "+
			"Для прода — отключите блок demo: в app.yaml",
		env,
	)
}
