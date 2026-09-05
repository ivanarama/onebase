package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestRunProcrunDynamicObjectFields(t *testing.T) {
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: dynamic-fields-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "catalogs/Клиент.yaml", `name: Клиент
fields:
  - name: Наименование
    type: string
  - name: Организация
    type: string
`)
	writeProcrunFixture(t, projectDir, "documents/Заказ.yaml", `name: Заказ
fields:
  - name: Комментарий
    type: string
`)
	writeProcrunFixture(t, projectDir, "processors/ДинамическиеПоля.yaml", "name: ДинамическиеПоля\n")
	writeProcrunFixture(t, projectDir, "src/ДинамическиеПоля.proc.os", `Процедура Выполнить()
    Кл = Справочники.Клиент.Создать();
    Кл.Наименование = "Проба DSL";
    Поле = "ОрГаНиЗаЦиЯ";
    Кл[Поле] = "ООО Проба";
    Сообщить("dynamic=" + Кл[Поле]);
    Кл.Записать();

    Док = Документы.Заказ.Создать();
    ПолеДокумента = "кОмМеНтАрИй";
    Док[ПолеДокумента] = "Динамический документ";
    Сообщить("document=" + Док[ПолеДокумента]);
    Док.Записать();
КонецПроцедуры
`)

	dbPath := filepath.Join(t.TempDir(), "dynamic-fields.db")
	if _, err := captureStdout(t, func() error {
		return runMigrate(migrateCmdFor(t, projectDir, dbPath, nil), nil)
	}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runProcrun(procrunCommandFor(t, projectDir, dbPath, "ДинамическиеПоля"), nil)
	})
	if err != nil {
		t.Fatalf("runProcrun: %v", err)
	}
	if !strings.Contains(out, "dynamic=ООО Проба") {
		t.Fatalf("индексное чтение не вернуло записанное значение, stdout: %q", out)
	}
	if !strings.Contains(out, "document=Динамический документ") {
		t.Fatalf("индексное чтение документа не вернуло записанное значение, stdout: %q", out)
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var organization string
	if err := db.QueryRow(ctx, `SELECT организация FROM клиент LIMIT 1`).Scan(&organization); err != nil {
		t.Fatalf("чтение сохранённого реквизита: %v", err)
	}
	if organization != "ООО Проба" {
		t.Fatalf("Организация = %q, ожидалось %q", organization, "ООО Проба")
	}
	var comment string
	if err := db.QueryRow(ctx, `SELECT комментарий FROM заказ LIMIT 1`).Scan(&comment); err != nil {
		t.Fatalf("чтение сохранённого реквизита документа: %v", err)
	}
	if comment != "Динамический документ" {
		t.Fatalf("Комментарий = %q, ожидалось %q", comment, "Динамический документ")
	}
}

func TestRunProcrunDynamicObjectFieldsInDocumentHooks(t *testing.T) {
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: dynamic-hook-fields-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "documents/Заказ.yaml", `name: Заказ
posting: true
fields:
  - name: Комментарий
    type: string
`)
	writeProcrunFixture(t, projectDir, "processors/ДинамическиеПоляХуков.yaml", "name: ДинамическиеПоляХуков\n")
	writeProcrunFixture(t, projectDir, "src/Заказ.os", `Процедура ПриЗаписи()
    Поле = "кОмМеНтАрИй";
    this[Поле] = this[Поле] + "|ПриЗаписи";
    Сообщить("hook-write=" + this[Поле]);
КонецПроцедуры
`)
	writeProcrunFixture(t, projectDir, "src/Заказ.posting.os", `Процедура ОбработкаПроведения()
    Поле = "КОММЕНТАРИЙ";
    ЭтотОбъект[Поле] = ЭтотОбъект[Поле] + "|Проведение";
    Сообщить("hook-post=" + ЭтотОбъект[Поле]);
КонецПроцедуры
`)
	writeProcrunFixture(t, projectDir, "src/ДинамическиеПоляХуков.proc.os", `Процедура Выполнить()
    Док = Документы.Заказ.Создать();
    Док.Комментарий = "Старт";
    Док.Провести();
КонецПроцедуры
`)

	dbPath := filepath.Join(t.TempDir(), "dynamic-hook-fields.db")
	if _, err := captureStdout(t, func() error {
		return runMigrate(migrateCmdFor(t, projectDir, dbPath, nil), nil)
	}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runProcrun(procrunCommandFor(t, projectDir, dbPath, "ДинамическиеПоляХуков"), nil)
	})
	if err != nil {
		t.Fatalf("runProcrun: %v", err)
	}
	for _, want := range []string{
		"hook-write=Старт|ПриЗаписи",
		"hook-post=Старт|ПриЗаписи|Проведение",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout не содержит %q: %q", want, out)
		}
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var comment string
	if err := db.QueryRow(ctx, `SELECT комментарий FROM заказ LIMIT 1`).Scan(&comment); err != nil {
		t.Fatalf("чтение сохранённого реквизита: %v", err)
	}
	if comment != "Старт|ПриЗаписи|Проведение" {
		t.Fatalf("Комментарий = %q, ожидалось %q", comment, "Старт|ПриЗаписи|Проведение")
	}
}

func TestRunProcrunDynamicFieldsRejectInvalidAccess(t *testing.T) {
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: dynamic-fields-errors\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "catalogs/Клиент.yaml", `name: Клиент
fields:
  - name: Наименование
    type: string
`)

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"неизвестная запись", `Кл = Справочники.Клиент.Создать(); Кл["НетТакого"] = 1;`, "Неизвестный реквизит «НетТакого»"},
		{"неизвестное чтение", `Кл = Справочники.Клиент.Создать(); Сообщить(Кл["НетТакого"]);`, "Неизвестный реквизит «НетТакого»"},
		{"нестроковое имя", `Кл = Справочники.Клиент.Создать(); Кл[1] = 1;`, "Имя реквизита в индексной записи должно быть строкой"},
		{"неподдержанная запись", `Значения = Новый Структура("Поле", 1); Значения["Поле"] = 2;`, "не поддерживает индексную запись"},
		{"неподдержанное чтение", `Значения = Новый Структура("Поле", 1); Сообщить(Значения["Поле"]);`, "не поддерживает индексное чтение"},
	}
	for _, tc := range tests {
		processor := "Ошибка" + strings.ReplaceAll(tc.name, " ", "")
		writeProcrunFixture(t, projectDir, "processors/"+processor+".yaml", "name: "+processor+"\n")
		writeProcrunFixture(t, projectDir, "src/"+processor+".proc.os", "Процедура Выполнить()\n    "+tc.body+"\nКонецПроцедуры\n")
	}

	dbPath := filepath.Join(t.TempDir(), "dynamic-fields-errors.db")
	if _, err := captureStdout(t, func() error {
		return runMigrate(migrateCmdFor(t, projectDir, dbPath, nil), nil)
	}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			processor := "Ошибка" + strings.ReplaceAll(tc.name, " ", "")
			_, err := captureStdout(t, func() error {
				return runProcrun(procrunCommandFor(t, projectDir, dbPath, processor), nil)
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("runProcrun error = %v, ожидалась подстрока %q", err, tc.wantErr)
			}
		})
	}
}

func procrunCommandFor(t *testing.T, projectDir, dbPath, processor string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().String("proc", "", "")
	cmd.Flags().StringArray("set", nil, "")
	cmd.Flags().StringArray("file", nil, "")
	for key, value := range map[string]string{"project": projectDir, "sqlite": dbPath, "proc": processor} {
		if err := cmd.Flags().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
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
