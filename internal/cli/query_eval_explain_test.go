package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

func TestRunEvalCLIExpression(t *testing.T) {
	if err := evalCmd.Flags().Set("code", ""); err != nil {
		t.Fatal(err)
	}
	if err := evalCmd.Flags().Set("file", ""); err != nil {
		t.Fatal(err)
	}
	out := captureCLIStdout(t, func() error {
		return runEvalCLI(evalCmd, []string{"1 + 2"})
	})

	var got struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("eval output is not JSON: %v\n%s", err, out)
	}
	if got.Result != "3" {
		t.Fatalf("result=%q, want 3", got.Result)
	}
}

func TestRunQueryCLISQLOnly(t *testing.T) {
	dir := t.TempDir()
	setBaseFlagValues(t, queryCmd, dir)
	if err := queryCmd.Flags().Set("file", ""); err != nil {
		t.Fatal(err)
	}
	if err := queryCmd.Flags().Set("params", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := queryCmd.Flags().Set("limit", "100"); err != nil {
		t.Fatal(err)
	}
	if err := queryCmd.Flags().Set("sql", "true"); err != nil {
		t.Fatal(err)
	}
	out := captureCLIStdout(t, func() error {
		return runQueryCLI(queryCmd, []string{"ВЫБРАТЬ 1 КАК X"})
	})

	var got struct {
		SQL string `json:"sql"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("query output is not JSON: %v\n%s", err, out)
	}
	if !strings.Contains(got.SQL, "SELECT 1 AS x") {
		t.Fatalf("compiled SQL=%q", got.SQL)
	}
}

func TestRunWidgetExplainCompilesQuery(t *testing.T) {
	dir := t.TempDir()
	writeCLITestFile(t, dir, "widgets/sales.yaml", `name: Продажи
type: chart
query: "ВЫБРАТЬ 1 КАК Период, 2 КАК Сумма"
chart_kind: line
x_field: Период
y_fields: [Сумма]
`)
	setBaseFlagValues(t, widgetExplainCmd, dir)
	if err := widgetExplainCmd.Flags().Set("sample", "0"); err != nil {
		t.Fatal(err)
	}
	out := captureCLIStdout(t, func() error {
		return runWidgetExplain(widgetExplainCmd, []string{"Продажи"})
	})

	var got struct {
		Name      string `json:"name"`
		ChartKind string `json:"chartKind"`
		SQL       string `json:"sql"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("widget explain output is not JSON: %v\n%s", err, out)
	}
	if got.Name != "Продажи" || got.ChartKind != "line" || !strings.Contains(got.SQL, "SELECT 1 AS") {
		t.Fatalf("widget explain output looks wrong: %+v", got)
	}
}

func TestRunReportExplainCompilesQuery(t *testing.T) {
	dir := t.TempDir()
	writeCLITestFile(t, dir, "reports/sales.yaml", `name: Продажи
title: Продажи
query: "ВЫБРАТЬ 1 КАК Сумма"
params:
  - name: Период
    type: date
`)
	setBaseFlagValues(t, reportExplainCmd, dir)
	if err := reportExplainCmd.Flags().Set("params", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := reportExplainCmd.Flags().Set("sample", "0"); err != nil {
		t.Fatal(err)
	}
	out := captureCLIStdout(t, func() error {
		return runReportExplain(reportExplainCmd, []string{"Продажи"})
	})

	var got struct {
		Name   string `json:"name"`
		Title  string `json:"title"`
		SQL    string `json:"sql"`
		Params []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"params"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("report explain output is not JSON: %v\n%s", err, out)
	}
	if got.Name != "Продажи" || got.Title != "Продажи" || len(got.Params) != 1 || !strings.Contains(got.SQL, "SELECT 1 AS") {
		t.Fatalf("report explain output looks wrong: %+v", got)
	}
}

func TestRunLimitedQueryTruncates(t *testing.T) {
	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, cols, truncated, err := runLimitedQuery(
		context.Background(),
		db,
		"SELECT 1 AS x UNION ALL SELECT 2 AS x",
		nil,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 || cols[0] != "x" {
		t.Fatalf("columns=%v", cols)
	}
	if len(rows) != 1 || rows[0]["x"] == nil || !truncated {
		t.Fatalf("rows=%v truncated=%v", rows, truncated)
	}
}

func setBaseFlagValues(t *testing.T, cmd *cobra.Command, dir string) {
	t.Helper()
	for name, value := range map[string]string{
		"project": dir,
		"id":      "",
		"sqlite":  "",
		"db":      "",
	} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
}

func writeCLITestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureCLIStdout(t *testing.T, run func() error) []byte {
	t.Helper()
	var out bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan error, 1)
	go func() {
		_, err := out.ReadFrom(r)
		done <- err
	}()
	if err := run(); err != nil {
		w.Close()
		t.Fatalf("run command: %v", err)
	}
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
