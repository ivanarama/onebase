package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/storage"
)

// Предохранители переключаются из командной строки (issue #709).
//
// До этого включить сеть или запуск команд можно было только в UI либо прямой
// правкой _settings. В headless-окружении — CI, автопрогон на свежей базе — это
// значило лезть в базу мимо движка, причём имена ключей нигде не были описаны.

func settingsCLIFixture(t *testing.T) (*cobra.Command, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guards.db")
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "")
	cmd.Flags().String("sqlite", "", "")
	if err := cmd.Flags().Set("sqlite", path); err != nil {
		t.Fatal(err)
	}
	return cmd, path
}

func guardStateOnDisk(t *testing.T, path string) (net, exec bool) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return db.GetNetworkEnabled(ctx), db.GetExecEnabled(ctx)
}

func TestSettingsSet_ВключаетПредохранительВБазе(t *testing.T) {
	cmd, path := settingsCLIFixture(t)

	if net, _ := guardStateOnDisk(t, path); net {
		t.Fatal("сеть включена на свежей базе — проба некорректна")
	}
	if err := runSettingsSet(cmd, []string{"net.enabled", "вкл"}); err != nil {
		t.Fatalf("settings set: %v", err)
	}

	// Читаем ЗАНОВО, отдельным подключением: иначе тест доказывал бы только то,
	// что команда что-то подержала в памяти.
	net, exec := guardStateOnDisk(t, path)
	if !net {
		t.Error("net.enabled не записан в базу")
	}
	if exec {
		t.Error("exec.enabled включился заодно — предохранители должны быть раздельными")
	}
}

func TestSettingsSet_ВыключаетОбратно(t *testing.T) {
	cmd, path := settingsCLIFixture(t)
	if err := runSettingsSet(cmd, []string{"exec.enabled", "on"}); err != nil {
		t.Fatal(err)
	}
	if _, exec := guardStateOnDisk(t, path); !exec {
		t.Fatal("exec.enabled не включился — проба некорректна")
	}
	if err := runSettingsSet(cmd, []string{"exec.enabled", "выкл"}); err != nil {
		t.Fatal(err)
	}
	if _, exec := guardStateOnDisk(t, path); exec {
		t.Error("exec.enabled не выключился")
	}
}

// Команда переключает только предохранители: в _settings лежат также политика
// аутентификации, ссылки на секреты и режим хранения файлов, и запись туда
// мимо кода, знающего их инварианты, — это редактор базы, а не настройка.
func TestSettingsSet_ЧужойКлючОтклоняется(t *testing.T) {
	cmd, path := settingsCLIFixture(t)

	err := runSettingsSet(cmd, []string{"auth.policy", "вкл"})
	if err == nil {
		t.Fatal("посторонний ключ принят")
	}
	if !strings.Contains(err.Error(), "net.enabled") {
		t.Errorf("в ошибке нет списка допустимых ключей: %v", err)
	}

	// И ничего не записалось.
	if net, exec := guardStateOnDisk(t, path); net || exec {
		t.Error("отклонённая команда всё-таки что-то записала")
	}
}

func TestSettingsSet_НепонятноеЗначениеОтклоняется(t *testing.T) {
	cmd, _ := settingsCLIFixture(t)
	if err := runSettingsSet(cmd, []string{"net.enabled", "ага"}); err == nil {
		t.Fatal("непонятное значение принято")
	}
}

// Тексты отказов предохранителей обязаны называть ключ и команду: сообщение про
// «Система → Настройки» в headless-окружении не отвечает на вопрос «что делать».
func TestТекстыОтказов_НазываютКлючИКоманду(t *testing.T) {
	for _, g := range guards {
		if !strings.Contains(settingsCmd.Long, g.key) {
			t.Errorf("ключ %s не упомянут в справке команды", g.key)
		}
		if g.doc == "" {
			t.Errorf("у предохранителя %s нет описания", g.key)
		}
	}
}
