package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// `report explain --params` приводит значения к типам параметров тем же путём,
// что экран отчёта и REST v2. Раньше даты уходили в запрос строками: на
// PostgreSQL сравнение с колонкой даты падало («operator does not exist:
// timestamp with time zone >= text»), а умолчание параметра ({{today}}) не
// подставлялось вовсе — explain объяснял не тот запрос, что строит экран.

func explainParamsProject(t *testing.T) (string, string) {
	t.Helper()
	projectDir := t.TempDir()
	writeProcrunFixture(t, projectDir, "config/app.yaml", "name: explain-params\nversion: \"1.0\"\n")
	writeProcrunFixture(t, projectDir, "catalogs/Товар.yaml", `name: Товар
fields:
  - name: Наименование
    type: string
  - name: Поступил
    type: date
`)
	writeProcrunFixture(t, projectDir, "reports/ПоступилоДо.yaml", `name: ПоступилоДо
title: Поступило до
params:
  - name: До
    type: date
    default: "{{today}}"
query: |
  ВЫБРАТЬ Наименование
  ИЗ Справочник.Товар
  ГДЕ Поступил <= &До
`)
	dbPath := filepath.Join(t.TempDir(), "explain.db")
	migrate := &cobra.Command{}
	addBaseFlags(migrate)
	for k, v := range map[string]string{"project": projectDir, "sqlite": dbPath} {
		if err := migrate.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrate(migrate, nil); err != nil {
		t.Fatalf("runMigrate: %v", err)
	}
	return projectDir, dbPath
}

func runExplainJSON(t *testing.T, projectDir, dbPath, params string) (explainOutput, error) {
	t.Helper()
	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().Int("sample", 0, "")
	cmd.Flags().String("params", "", "")
	cmd.Flags().Bool("json", false, "")
	flags := map[string]string{"project": projectDir, "sqlite": dbPath, "sample": "5", "json": "true"}
	if params != "" {
		flags["params"] = params
	}
	for k, v := range flags {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureStdout(t, func() error {
		return runReportExplain(cmd, []string{"ПоступилоДо"})
	})
	if err != nil {
		return explainOutput{}, err
	}
	var res explainOutput
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("json explain: %v\n%s", jerr, out)
	}
	return res, nil
}

// argTime — аргумент запроса как момент времени; строка «2026-01-02» без
// времени и зоны — признак того, что значение ушло в SQL текстом.
func argTime(t *testing.T, res explainOutput) time.Time {
	t.Helper()
	if len(res.Args) != 1 {
		t.Fatalf("аргументов запроса %d, ожидался 1: %v (ошибка %q)", len(res.Args), res.Args, res.Error)
	}
	s, _ := res.Args[0].(string)
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("параметр-дата ушёл в запрос не датой, а %T %v", res.Args[0], res.Args[0])
	}
	return at
}

func TestReportExplainParamsAreTyped(t *testing.T) {
	projectDir, dbPath := explainParamsProject(t)

	res, err := runExplainJSON(t, projectDir, dbPath, `{"До":"2026-01-02"}`)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("explain: %s", res.Error)
	}
	if got := argTime(t, res).In(time.Local).Format("2006-01-02 15:04"); got != "2026-01-02 00:00" {
		t.Fatalf("дата параметра %s, ожидалась местная полночь 2026-01-02", got)
	}

	// Параметр не передан — умолчание отчёта ({{today}}), как на экране.
	res, err = runExplainJSON(t, projectDir, dbPath, "")
	if err != nil {
		t.Fatalf("explain без --params: %v", err)
	}
	if got, want := argTime(t, res).In(time.Local).Format("2006-01-02"), time.Now().Format("2006-01-02"); got != want {
		t.Fatalf("умолчание {{today}} дало %s, ожидалось %s", got, want)
	}

	// Негодная дата — ошибка с именем параметра, а не строка в запросе.
	if _, err = runExplainJSON(t, projectDir, dbPath, `{"До":"завтра"}`); err == nil || !strings.Contains(err.Error(), "До") {
		t.Fatalf("негодная дата: ошибка %v, ожидалось указание на параметр «До»", err)
	}
}
