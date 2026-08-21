package cli

// Причина отказа в CLI (#1065).
//
// `onebase update`, `--from` и `--rollback` отвечали одной строкой «установка …
// не поддерживает безопасное самообновление» на два разных препятствия. Строка
// верна и бесполезна: она не говорит, что делать, а из двух случаев только один
// лечится правами.

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/selfupdate"
)

func TestExplainTargetBlock_PermissionsAndLocationDiffer(t *testing.T) {
	perm := explainTargetBlock(`C:\Program Files\onebase`,
		errors.Join(selfupdate.ErrTargetNotWritable, errors.New("деталь")), "обновить")
	if !strings.Contains(perm.Error(), "нет прав на запись") {
		t.Errorf("отказ по правам не назван правами: %v", perm)
	}
	if !strings.Contains(perm.Error(), `C:\Program Files\onebase`) {
		t.Errorf("в отказе нет каталога установки: %v", perm)
	}

	loc := explainTargetBlock(`C:\Projects\onebase`,
		errors.Join(selfupdate.ErrTargetNotPrivate, errors.New("деталь")), "обновить")
	text := loc.Error()
	if strings.Contains(text, "нет прав на запись") {
		t.Errorf("расположение установки выдано за отказ по правам: %v", loc)
	}
	if !strings.Contains(text, "вне личного каталога пользователя") {
		t.Errorf("настоящая причина не названа: %v", loc)
	}
	// Совет «запустите от администратора» здесь вреден: у администратора другой
	// личный каталог, и он упрётся в тот же отказ.
	if !strings.Contains(text, "администратора ничего не изменит") {
		t.Errorf("текст не снимает ложную догадку про администратора: %v", loc)
	}
	for _, want := range []string{"вручную", "переставить"} {
		if !strings.Contains(text, want) {
			t.Errorf("в отказе нет выхода %q: %v", want, loc)
		}
	}
}

// Неизвестная причина остаётся общей формулировкой — с деталью, а не молча.
func TestExplainTargetBlock_UnknownReasonKeepsDetail(t *testing.T) {
	err := explainTargetBlock("/opt/onebase", errors.New("каталог исчез"), "обновить")
	if !strings.Contains(err.Error(), "каталог исчез") {
		t.Errorf("техническая причина потеряна: %v", err)
	}
}

func TestWarnUnrecognizedVersion(t *testing.T) {
	out, err := captureStdout(t, func() error {
		if !warnUnrecognizedVersion("build-793fix") {
			t.Error("нераспознанная версия не вызвала предупреждения")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"build-793fix", "не сопоставима с выпусками канала", "никогда"} {
		if !strings.Contains(out, want) {
			t.Errorf("в предупреждении нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "актуальная версия") {
		t.Errorf("предупреждение всё равно называет версию актуальной:\n%s", out)
	}

	// Нормальная версия предупреждения не печатает — иначе оно обесценится.
	quiet, err := captureStdout(t, func() error {
		if warnUnrecognizedVersion("build-793") {
			t.Error("распознанная версия вызвала предупреждение")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(quiet) != "" {
		t.Errorf("на распознанной версии напечатано лишнее:\n%s", quiet)
	}
}
