package pipelinecontract

import (
	"encoding/json"
	"sort"
	"testing"
)

// Список обязательных проверок ветки `main` живёт в репозитории пятью копиями
// (issue #1346). Авторитет — `.github/branch-protection.json`: этот же JSON
// лежит в живой защите ветки. Копия, которую читает не человек, а инструмент
// конвейера, ровно одна — `required_checks` в `pipelinectl.json`, и её
// расхождение с авторитетом означает не «неточную памятку», а неверное
// поведение: `pipelinectl` ждёт другой набор проверок, чем требует GitHub.
//
// Цена расхождения известна: после того как `launcher-webview-build` стал
// обязательным 22.08.2026, три текста продолжали называть его необязательным
// (#1192) — агент по инструкции считал красный джоб неблокирующим и упирался в
// отказ GitHub, которого в его картине мира не бывает. #1194 синхронизировал
// тексты, но сторожа не поставил.
//
// Здесь сторожится именно машиночитаемая пара. Текстовые копии (CLAUDE.md,
// docs/maintenance-pipeline.md, скил пастуха) намеренно оставлены вне теста:
// проверка «имя проверки упомянуто в тексте» не поймала бы #1192 — имя там
// присутствовало, неверным было утверждение вокруг него, — зато сделала бы
// красной сборкой любую переформулировку абзаца. Ощущение гарантии без самой
// гарантии дороже честного отсутствия проверки.
//
// Вариант 1 из разбора триажа (#1346).

func branchProtectionContexts(t *testing.T) []string {
	t.Helper()
	var protection struct {
		RequiredStatusChecks struct {
			Contexts []string `json:"contexts"`
		} `json:"required_status_checks"`
	}
	raw := repositoryFile(t, ".github", "branch-protection.json")
	if err := json.Unmarshal([]byte(raw), &protection); err != nil {
		t.Fatalf(".github/branch-protection.json: %v", err)
	}
	return protection.RequiredStatusChecks.Contexts
}

func pipelinectlRequiredChecks(t *testing.T) []string {
	t.Helper()
	var config struct {
		RequiredChecks []string `json:"required_checks"`
	}
	raw := repositoryFile(t, "pipelinectl.json")
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("pipelinectl.json: %v", err)
	}
	return config.RequiredChecks
}

func TestPipelinectlRequiredChecksMatchBranchProtection(t *testing.T) {
	authority := branchProtectionContexts(t)
	tool := pipelinectlRequiredChecks(t)

	// Пустой список означал бы, что ключ переименовали или структура файла
	// изменилась: тогда сравнение двух пустых множеств прошло бы молча и
	// сторож перестал бы сторожить.
	if len(authority) == 0 {
		t.Fatal(".github/branch-protection.json: required_status_checks.contexts пуст — сторож сравнивал бы пустоту")
	}
	if len(tool) == 0 {
		t.Fatal("pipelinectl.json: required_checks пуст — сторож сравнивал бы пустоту")
	}

	missing := difference(authority, tool)
	extra := difference(tool, authority)
	if len(missing) > 0 {
		t.Errorf("pipelinectl.json не знает обязательные проверки %v: конвейер вольёт PR, который GitHub не пропустит", missing)
	}
	if len(extra) > 0 {
		t.Errorf("pipelinectl.json ждёт проверки %v, которых нет в защите ветки: конвейер будет ждать вечно", extra)
	}
}

// difference возвращает элементы want, которых нет в have, — отсортированными,
// чтобы сообщение об ошибке не зависело от порядка ключей в файлах.
func difference(want, have []string) []string {
	present := make(map[string]bool, len(have))
	for _, name := range have {
		present[name] = true
	}
	var out []string
	for _, name := range want {
		if !present[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
