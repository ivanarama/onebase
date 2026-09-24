package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestWidgetExplainSampleWithConstant(t *testing.T) {
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: constant-explain\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "constants/settings.yaml", "constants:\n  - name: УчетСНДС\n    type: bool\n    default: 'true'\n")
	writeProcrunFixture(t, projectDir, "widgets/probe.yaml", `name: Probe
type: list
params:
  СНДС: "{{constant:УчетСНДС}}"
query: "ВЫБРАТЬ &СНДС КАК СНДС"
`)
	dbPath := filepath.Join(t.TempDir(), "explain.db")
	migrate := &cobra.Command{Use: "migrate", RunE: migrateCmd.RunE}
	addBaseFlags(migrate)
	migrate.SetArgs([]string{"--project", projectDir, "--sqlite", dbPath})
	if err := migrate.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []string{"5", "0"} {
		t.Run("sample="+sample, func(t *testing.T) {
			cmd := &cobra.Command{Use: widgetExplainCmd.Use, Args: widgetExplainCmd.Args, RunE: widgetExplainCmd.RunE,
				SilenceErrors: true, SilenceUsage: true}
			addBaseFlags(cmd)
			cmd.Flags().Int("sample", 0, "")
			cmd.Flags().Bool("json", false, "")
			cmd.SetArgs([]string{"Probe", "--project", projectDir, "--sqlite", dbPath, "--sample", sample, "--json"})
			output, err := captureStdout(t, cmd.Execute)
			if sample == "0" {
				if err == nil || !strings.Contains(err.Error(), "константы недоступны") {
					t.Fatalf("без --sample ожидался отказ источника констант: %v; %s", err, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("widget explain --sample: %v", err)
			}
			var result explainOutput
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatal(err)
			}
			if result.Error != "" || result.SQL == "" || len(result.Rows) != 1 || result.Params["СНДС"] != true {
				t.Fatalf("explain не выполнил виджет с булевой константой: %s", output)
			}
			if len(result.Args) != 1 || result.Args[0] != true {
				t.Fatalf("SQL получил нетипизированный параметр: %s", output)
			}
		})
	}
}

// TestReportExplainSampleSQLiteWithParams — регрессия на issue #473:
// `report explain --sample N` с переданными значениями параметров на SQLite
// падал «missing named argument "1::text"», потому что запрос компилировался
// без диалекта открытой БД (генерились Postgres-плейсхолдеры $N::text).
func TestReportExplainSampleSQLiteWithParams(t *testing.T) {
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: explain-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "catalogs/Товар.yaml", "name: Товар\nfields:\n  - name: Наименование\n    type: string\n")
	writeProcrunFixture(t, projectDir, "reports/ПоТовару.yaml", `name: ПоТовару
title: По товару
params:
  - name: Имя
    type: string
    label: "Имя"
query: |
  ВЫБРАТЬ Наименование
  ИЗ Справочник.Товар
  ГДЕ (&Имя ЕСТЬ ПУСТО ИЛИ Наименование = &Имя)
`)

	dbPath := filepath.Join(t.TempDir(), "explain.db")

	// Схема БД.
	migrate := &cobra.Command{}
	addBaseFlags(migrate)
	if err := migrate.Flags().Set("project", projectDir); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Flags().Set("sqlite", dbPath); err != nil {
		t.Fatal(err)
	}
	if err := runMigrate(migrate, nil); err != nil {
		t.Fatalf("runMigrate: %v", err)
	}

	// report explain --sample с переданным значением параметра.
	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().Int("sample", 0, "")
	cmd.Flags().String("params", "", "")
	cmd.Flags().Bool("json", false, "")
	for k, v := range map[string]string{
		"project": projectDir,
		"sqlite":  dbPath,
		"sample":  "5",
		"params":  `{"Имя":"Молоко"}`,
	} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureStdout(t, func() error {
		return runReportExplain(cmd, []string{"ПоТовару"})
	})
	if err != nil {
		t.Fatalf("runReportExplain: %v", err)
	}

	if strings.Contains(out, "missing named argument") {
		t.Fatalf("исполнение --sample на SQLite снова упало:\n%s", out)
	}
	if strings.Contains(out, "$1::text") {
		t.Fatalf("SQL содержит Postgres-плейсхолдеры вместо SQLite-диалекта:\n%s", out)
	}
	if strings.Contains(out, "Ошибка:") {
		t.Fatalf("explain вернул ошибку:\n%s", out)
	}
}
