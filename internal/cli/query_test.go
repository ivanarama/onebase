package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
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

// Exercise the public command, including its own database connection, so the
// displayed SQL, executed limit and normalized column types cannot diverge.
func TestQueryLimitMatrix(t *testing.T) {
	oldLocation := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = oldLocation })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		projectDir, _ := queryCommandFixture(t)
		writeProcrunFixture(t, projectDir, "catalogs/QueryItems.yaml", `name: QueryItems
fields:
  - name: Значение
    type: string
  - name: Активен
    type: bool
  - name: ДатаСобытия
    type: date
`)
		proj, err := project.Load(projectDir)
		if err != nil {
			t.Fatal(err)
		}
		defer proj.Close()
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			t.Fatal(err)
		}
		date := time.Date(2026, 9, 26, 12, 34, 56, 0, time.UTC)
		for i := 0; i < 105; i++ {
			if err := db.Upsert(ctx, "QueryItems", uuid.New(), map[string]any{
				"Значение": fmt.Sprintf("%03d", i),
				"Активен":  i%2 == 0, "ДатаСобытия": date,
			}, proj.Entities[0]); err != nil {
				t.Fatal(err)
			}
		}
		baseArgs := []string{"query", "--project", projectDir}
		if db.IsSQLite() {
			baseArgs = append(baseArgs, "--sqlite", db.SQLitePath())
		} else {
			var schema string
			if err := db.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			// Preserve dbtest's ephemeral schema for the command's new connection.
			dsn := os.Getenv("TEST_DATABASE_URL")
			if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
				u, err := url.Parse(dsn)
				if err != nil {
					t.Fatal(err)
				}
				params := u.Query()
				params.Set("search_path", schema+",public")
				u.RawQuery = params.Encode()
				dsn = u.String()
			} else {
				dsn += " search_path=" + schema + ",public"
			}
			baseArgs = append(baseArgs, "--db", dsn)
		}
		query := "ВЫБРАТЬ Значение КАК value, Активен КАК active, ДатаСобытия КАК event_date " +
			"ИЗ Справочник.QueryItems ГДЕ Значение >= &Минимум УПОРЯДОЧИТЬ ПО Значение"
		for _, tc := range []struct {
			name  string
			flags []string
			count int
			limit int
		}{
			{"positive", []string{"--limit", "2"}, 2, 2},
			{"unlimited", []string{"--limit", "0"}, 104, 0},
			{"default", nil, 100, 100},
		} {
			for _, jsonOut := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonOut), func(t *testing.T) {
					args := append([]string{}, baseArgs...)
					args = append(args, "--sql", "--params", `{"Минимум":"001"}`)
					args = append(args, tc.flags...)
					if jsonOut {
						args = append(args, "--json")
					}
					out, err := executeQueryCommand(t, append(args, query)...)
					if err != nil {
						t.Fatal(err)
					}
					if tc.limit > 0 && !strings.Contains(out, fmt.Sprintf(") _onebase_q LIMIT %d", tc.limit)) {
						t.Fatalf("missing SQL limit: %s", out)
					}
					if tc.limit == 0 && strings.Contains(out, "_onebase_q LIMIT") {
						t.Fatalf("--limit 0 added SQL limit: %s", out)
					}
					if jsonOut {
						var got queryOutput
						if err := json.Unmarshal([]byte(out), &got); err != nil {
							t.Fatal(err)
						}
						if got.Count != tc.count || len(got.Rows) != tc.count {
							t.Fatalf("returned %d rows (count %d), want %d", len(got.Rows), got.Count, tc.count)
						}
						if !reflect.DeepEqual(got.Args, []any{"001"}) || len(got.Sources) != 1 || got.Sources[0].Name != "QueryItems" {
							t.Fatalf("lost parameters or sources: %+v", got)
						}
						if !reflect.DeepEqual(got.Columns, []string{"value", "active", "event_date"}) {
							t.Fatalf("columns = %v", got.Columns)
						}
						for i, row := range got.Rows {
							if row["value"] != fmt.Sprintf("%03d", i+1) || row["active"] != ((i+1)%2 == 0) {
								t.Fatalf("wrong values or bool normalization: %+v", row)
							}
							if row["event_date"] != date.Format(time.RFC3339) {
								t.Fatalf("date normalization: %v, want %s", row["event_date"], date.Format(time.RFC3339))
							}
						}
					} else {
						parts := strings.SplitN(out, "\n\n", 2)
						if len(parts) != 2 || !strings.Contains(parts[0], "ARGS: [001]") {
							t.Fatalf("lost SQL or parameter diagnostics: %s", out)
						}
						lines := strings.Split(strings.TrimSpace(parts[1]), "\n")
						if len(lines) != tc.count+3 || lines[0] != "value\tactive\tevent_date" ||
							!strings.HasPrefix(lines[len(lines)-1], fmt.Sprintf("%d строк,", tc.count)) {
							t.Fatalf("wrong text row count or columns: %s", out)
						}
						for i := 0; i < tc.count; i++ {
							want := fmt.Sprintf("%03d\t%t\t%s", i+1, (i+1)%2 == 0, date.Format("2006-01-02 15:04:05"))
							if lines[i+1] != want {
								t.Fatalf("row %d = %q, want %q", i, lines[i+1], want)
							}
						}
					}
				})
			}
		}
	})
}
