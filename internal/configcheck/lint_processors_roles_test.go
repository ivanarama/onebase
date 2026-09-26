package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Приёмка срез A плана 162 через настоящий путь `onebase check --lint`:
// RunFullWithOptions — тот же вход, что у пользовательской команды.

func TestLintRoles_ProcessorsImplicitAllowWarnsPerRole(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), "name: Клиент\nfields:\n  - name: Номер\n    type: string\n")
mkFile(t, filepath.Join(dir, "processors", "импорт.yaml"), "name: Импорт\nparams: []\n")
	mkFile(t, filepath.Join(dir, "src", "импорт.proc.os"), "Процедура Выполнить() Экспорт\nКонецПроцедуры\n")
	mkFile(t, filepath.Join(dir, "processors", "тестовая.yaml"), "name: Тестовая\nkind: test\nparams: []\n")
	mkFile(t, filepath.Join(dir, "roles", "менеджер.yaml"), "name: Менеджер\npermissions:\n  catalogs:\n    Клиент: [read]\n")
	mkFile(t, filepath.Join(dir, "roles", "гость.yaml"), "name: Гость\npermissions:\n  processors: {}\n")
	mkFile(t, filepath.Join(dir, "roles", "оператор.yaml"), "name: Оператор\npermissions:\n  processors:\n    Импорт: [run]\n")

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("переходный линт не должен ронять check: %+v", res.Issues)
	}

	implicit := 0
	for _, w := range res.Warnings {
		if w.Code != "rbac.processors-implicit-allow" {
			continue
		}
		implicit++
		if w.Object != "Менеджер" {
			t.Fatalf("implicit-warning не той роли: %+v", w)
		}
		// Тест-обработка не считается: доступных нетестовых обработок ровно одна.
		if !strings.Contains(w.Message, "(1)") {
			t.Fatalf("сообщение должно называть число доступных нетестовых обработок: %+v", w)
		}
		if !strings.Contains(w.Message, "processors_default: allow") {
			t.Fatalf("сообщение должно называть все три выхода: %+v", w)
		}
	}
	if implicit != 1 {
		t.Fatalf("implicit-warning ровно один за прогон, получено %d: warnings=%+v", implicit, res.Warnings)
	}
	for _, w := range res.Warnings {
		if w.Code == "rbac.processors-explicit-allow-all" {
			t.Fatalf("compat-warning не должен появляться без processors_default: %+v", w)
		}
	}
	// Неявное allow-all продолжает считать процессорное покрытие: обработка с
	// ролью не выдаёт rbac.object-without-role.
	for _, w := range res.Warnings {
		if w.Code == "rbac.object-without-role" && strings.Contains(w.Message, "Импорт") {
			t.Fatalf("неявное allow-all потеряло статус покрытия: %+v", w)
		}
	}
}

func TestLintRoles_ProcessorsExplicitAllowAllWarnsSoftly(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "импорт.yaml"), "name: Импорт\nparams: []\n")
	mkFile(t, filepath.Join(dir, "roles", "совместимая.yaml"), "name: Совместимая\npermissions:\n  processors_default: allow\n")

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("compatibility-роль не должна ронять check: %+v", res.Issues)
	}
	explicit, implicit := 0, 0
	for _, w := range res.Warnings {
		switch w.Code {
		case "rbac.processors-explicit-allow-all":
			explicit++
			if w.Object != "Совместимая" {
				t.Fatalf("explicit-warning не той роли: %+v", w)
			}
		case "rbac.processors-implicit-allow":
			implicit++
		}
	}
	if explicit != 1 || implicit != 0 {
		t.Fatalf("explicit=%d implicit=%d, ждали 1 и 0: warnings=%+v", explicit, implicit, res.Warnings)
	}
}

func TestLintRoles_ProcessorsDefaultConflictsWithMap(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "roles", "плохая.yaml"), "name: Плохая\npermissions:\n  processors_default: allow\n  processors:\n    Импорт: [run]\n")

	res := RunFullWithOptions(dir, Options{Lint: true})
	if res.OK {
		t.Fatalf("неоднозначное сочетание обязано ронять check: %+v", res)
	}
	found := false
	for _, issue := range res.Issues {
		if strings.Contains(issue.Message, "processors_default") {
			found = true
		}
	}
	if !found {
		t.Fatalf("ошибка не называет processors_default: %+v", res.Issues)
	}
}
