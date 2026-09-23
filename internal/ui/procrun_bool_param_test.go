package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Логические параметры обработки в пути procrun (issue #1437).
//
// `onebase procrun --set Флаг=Истина` молча передавал Ложь: набор истинных
// значений состоял из `true`, `on`, `1` и `да`, а самое естественное для
// русскоязычного DSL слово в него не входило. Обработка при этом не падала —
// она просто не делала того, что человек включил.
//
// Тест идёт публичной точкой входа `RunProcessorOffline` — тем же вызовом, что
// и `internal/cli/procrun.go:83`, а не разбором значения напрямую: проверка
// приватного разборщика осталась бы зелёной и в том случае, если бы значение
// до обработки не доезжало.

// writeBoolParamProject собирает минимальный проект с обработкой, у которой
// один параметр `bool`; Выполнить() сообщает «да» либо «нет» по его значению.
func writeBoolParamProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"processors", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	yaml := "name: ЗагрузкаЭДО\ntitle: Загрузка ЭДО\nparams:\n  - name: СоздаватьНедостающую\n    type: bool\n    label: Создавать недостающую номенклатуру\n"
	if err := os.WriteFile(filepath.Join(dir, "processors", "загрузкаэдо.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	procOS := "Процедура Выполнить()\n" +
		"  Если Параметры.СоздаватьНедостающую Тогда\n" +
		"    Сообщить(\"да\");\n" +
		"  Иначе\n" +
		"    Сообщить(\"нет\");\n" +
		"  КонецЕсли;\n" +
		"КонецПроцедуры\n"
	if err := os.WriteFile(filepath.Join(dir, "src", "загрузкаэдо.proc.os"), []byte(procOS), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runBoolParam(t *testing.T, dir, raw string) string {
	t.Helper()
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "procrun.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	messages, runErr, err := RunProcessorOffline(ctx, proj, db, "ЗагрузкаЭДО",
		map[string]string{"СоздаватьНедостающую": raw}, nil)
	if err != nil {
		t.Fatalf("RunProcessorOffline(%q): %v", raw, err)
	}
	if runErr != nil {
		t.Fatalf("выполнение обработки при %q: %v", raw, runErr)
	}
	if len(messages) != 1 {
		t.Fatalf("при %q ждали одно сообщение, получено %v", raw, messages)
	}
	return messages[0]
}

func TestProcrunBoolParamRussianLiterals(t *testing.T) {
	dir := writeBoolParamProject(t)

	t.Run("истина", func(t *testing.T) {
		// «Истина» в любом регистре — то самое значение из заявки.
		for _, raw := range []string{"Истина", "истина", "ИСТИНА", "Да", "да", "ДА", "true", "on", "1"} {
			if got := runBoolParam(t, dir, raw); got != "да" {
				t.Errorf("--set X=%s дал %q, ждали «да»", raw, got)
			}
		}
	})

	t.Run("ложь", func(t *testing.T) {
		// Совместимость: ложные и нераспознанные значения читаются как раньше.
		// Строгий отказ на произвольный текст — отдельное продуктовое решение,
		// поэтому «мусор» здесь обязан остаться ложью, а не ошибкой.
		for _, raw := range []string{"Ложь", "ложь", "Нет", "нет", "false", "off", "0", "", "мусор"} {
			if got := runBoolParam(t, dir, raw); got != "нет" {
				t.Errorf("--set X=%s дал %q, ждали «нет»", raw, got)
			}
		}
	})
}
