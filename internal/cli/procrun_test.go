package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

func TestRunProcrunEnsuresAuditSchema(t *testing.T) {
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: procrun-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "processors/Проверка.yaml", "name: Проверка\n")
	writeProcrunFixture(t, projectDir, "src/Проверка.proc.os", `Процедура Выполнить()
    Сообщить("ok");
КонецПроцедуры
`)

	dbPath := filepath.Join(t.TempDir(), "procrun.db")
	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().String("proc", "", "")
	cmd.Flags().StringArray("set", nil, "")
	cmd.Flags().StringArray("file", nil, "")
	if err := cmd.Flags().Set("project", projectDir); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("sqlite", dbPath); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("proc", "Проверка"); err != nil {
		t.Fatal(err)
	}

	if err := runProcrun(cmd, nil); err != nil {
		t.Fatalf("runProcrun: %v", err)
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM _audit").Scan(&count); err != nil {
		t.Fatalf("procrun did not initialize _audit: %v", err)
	}
}

// onebase check не выводит тип локальной переменной из результата НайтиПо… и
// потому не может доказать, что последующее чтение идёт у ссылки. Публичный
// runtime-путь обязан хотя бы завершиться явной ошибкой с рабочей подсказкой,
// а не продолжить выполнение со значением Неопределено.
func TestCheckAndProcrunRejectImplicitReferenceAttributeAtRuntime(t *testing.T) {
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: ref-member-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "catalogs/Клиент.yaml", `name: Клиент
fields:
  - name: Наименование
    type: string
  - name: Телефон
    type: string
`)
	writeProcrunFixture(t, projectDir, "processors/ПроверкаСсылки.yaml", "name: ПроверкаСсылки\n")
	writeProcrunFixture(t, projectDir, "src/ПроверкаСсылки.proc.os", `Процедура Выполнить()
    Клиент = Справочники.Клиент.Создать();
    Клиент.Наименование = "Иванов";
    Клиент.Телефон = "+79161234567";
    Клиент.Записать();
    СсылкаКлиента = Справочники.Клиент.НайтиПоНаименованию("Иванов");
    Сообщить(СсылкаКлиента.Телефон);
КонецПроцедуры
`)

	checkOut, err := runCheckCmd(t, runCheck, projectDir, nil)
	if err != nil {
		t.Fatalf("onebase check должен принять динамически типизированный модуль: %v\n%s", err, checkOut)
	}
	if !strings.Contains(checkOut, "OK: ошибок не найдено") {
		t.Fatalf("onebase check не подтвердил конфигурацию:\n%s", checkOut)
	}

	ctx := context.Background()
	proj, err := project.Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "procrun.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		proj.Close()
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		db.Close()
		proj.Close()
		t.Fatal(err)
	}
	db.Close()
	proj.Close()

	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().String("proc", "", "")
	cmd.Flags().StringArray("set", nil, "")
	cmd.Flags().StringArray("file", nil, "")
	for flag, value := range map[string]string{
		"project": projectDir,
		"sqlite":  dbPath,
		"proc":    "ПроверкаСсылки",
	} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}

	err = runProcrun(cmd, nil)
	if err == nil {
		t.Fatal("procrun молча принял чтение Ссылка.Телефон")
	}
	for _, want := range []string{
		"Реквизит ссылки «Телефон» недоступен через точку",
		`ЗначениеРеквизитаОбъекта(Ссылка, "Телефон")`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ошибка procrun не содержит %q: %v", want, err)
		}
	}
}

func writeProcrunFixture(t *testing.T, root, relativePath, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
