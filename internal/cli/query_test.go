package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func queryCommandFixture(t *testing.T) (string, string) {
	t.Helper()
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "app.yaml"),
		[]byte("name: query-test\nversion: \"1.0\"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return projectDir, filepath.Join(t.TempDir(), "query.db")
}

func executeQueryCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetQueryCommandFlags(t)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		resetQueryCommandFlags(t)
	})
	return captureStdout(t, rootCmd.Execute)
}

func resetQueryCommandFlags(t *testing.T) {
	t.Helper()
	queryCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatalf("сбросить флаг query --%s: %v", flag.Name, err)
		}
		flag.Changed = false
	})
}

func TestQuerySQLIsPrintedBeforeExecutionError(t *testing.T) {
	projectDir, dbPath := queryCommandFixture(t)
	out, err := executeQueryCommand(t,
		"query", "--project", projectDir, "--sqlite", dbPath, "--sql",
		"SELECT * FROM missing_query_table",
	)
	if err == nil {
		t.Fatal("запрос к отсутствующей таблице обязан завершиться ошибкой")
	}
	if !strings.Contains(out, "SQL:\n") || !strings.Contains(out, "missing_query_table") {
		t.Fatalf("--sql не показал скомпилированный SQL до ошибки:\n%s", out)
	}
	if !strings.Contains(out, "ARGS: []") {
		t.Fatalf("--sql не показал аргументы до ошибки:\n%s", out)
	}
}

func TestQueryExecutionErrorWithoutSQLFlagHasNoDiagnostic(t *testing.T) {
	projectDir, dbPath := queryCommandFixture(t)
	out, err := executeQueryCommand(t,
		"query", "--project", projectDir, "--sqlite", dbPath,
		"SELECT * FROM missing_query_table",
	)
	if err == nil {
		t.Fatal("запрос к отсутствующей таблице обязан завершиться ошибкой")
	}
	if strings.Contains(out, "SQL:") || strings.Contains(out, "ARGS:") {
		t.Fatalf("диагностика SQL появилась без --sql:\n%s", out)
	}
}

func TestQuerySQLFlagExplainsCompileError(t *testing.T) {
	projectDir, dbPath := queryCommandFixture(t)
	out, err := executeQueryCommand(t,
		"query", "--project", projectDir, "--sqlite", dbPath, "--sql",
		"ВЫБРАТЬ Ном ИЗ РегистрНакопления.Неизвестный.Остатки()",
	)
	if err == nil {
		t.Fatal("незавершённый запрос обязан завершиться ошибкой компиляции")
	}
	if !strings.Contains(err.Error(), "SQL ещё не построен") {
		t.Fatalf("ошибка не объясняет отсутствие SQL: %v", err)
	}
	if strings.Contains(out, "SQL:\n") || strings.Contains(out, "ARGS:") {
		t.Fatalf("при ошибке компиляции напечатан несуществующий SQL:\n%s", out)
	}
}

func TestQueryJSONWithSQLRemainsSingleJSONDocument(t *testing.T) {
	projectDir, dbPath := queryCommandFixture(t)
	out, err := executeQueryCommand(t,
		"query", "--project", projectDir, "--sqlite", dbPath, "--json", "--sql",
		"SELECT 1 AS value",
	)
	if err != nil {
		t.Fatalf("успешный JSON-запрос вернул ошибку: %v", err)
	}
	var got queryOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json --sql должен вернуть один JSON-документ: %v\n%s", err, out)
	}
	if got.SQL == "" || len(got.Rows) != 1 {
		t.Fatalf("JSON-ответ потерял SQL или результат: %+v", got)
	}
}

func TestQueryJSONExecutionErrorKeepsDiagnosticOutOfStdout(t *testing.T) {
	projectDir, dbPath := queryCommandFixture(t)
	out, err := executeQueryCommand(t,
		"query", "--project", projectDir, "--sqlite", dbPath, "--json", "--sql",
		"SELECT * FROM missing_query_table",
	)
	if err == nil {
		t.Fatal("запрос к отсутствующей таблице обязан завершиться ошибкой")
	}
	if out != "" {
		t.Fatalf("ошибка не должна загрязнять JSON-канал stdout:\n%s", out)
	}
	if !strings.Contains(err.Error(), "SQL:\n") || !strings.Contains(err.Error(), "ARGS: []") {
		t.Fatalf("ошибка --json --sql потеряла SQL-диагностику: %v", err)
	}
}
