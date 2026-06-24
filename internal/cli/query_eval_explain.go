package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

var queryCmd = &cobra.Command{
	Use:   "query [query text]",
	Short: "Выполнить или скомпилировать запрос OneBase headless",
	Args:  cobra.ArbitraryArgs,
	RunE:  runQueryCLI,
}

var evalCmd = &cobra.Command{
	Use:   "eval [expression]",
	Short: "Выполнить DSL-выражение или фрагмент в песочнице",
	Args:  cobra.ArbitraryArgs,
	RunE:  runEvalCLI,
}

var widgetCmd = &cobra.Command{Use: "widget", Short: "Инструменты для виджетов"}
var widgetExplainCmd = &cobra.Command{
	Use:   "explain <name>",
	Short: "Объяснить виджет: YAML, SQL и опциональные sample rows",
	Args:  cobra.ExactArgs(1),
	RunE:  runWidgetExplain,
}

var reportCmd = &cobra.Command{Use: "report", Short: "Инструменты для отчётов"}
var reportExplainCmd = &cobra.Command{
	Use:   "explain <name>",
	Short: "Объяснить отчёт: параметры, SQL, компоновку и sample rows",
	Args:  cobra.ExactArgs(1),
	RunE:  runReportExplain,
}

func init() {
	addBaseFlags(queryCmd)
	queryCmd.Flags().String("file", "", "прочитать запрос из файла")
	queryCmd.Flags().String("params", "{}", "JSON-объект параметров запроса")
	queryCmd.Flags().Int("limit", 100, "максимум строк в JSON-результате; 0 = без обрезки")
	queryCmd.Flags().Bool("sql", false, "только скомпилировать и вывести SQL/args, без выполнения")
	rootCmd.AddCommand(queryCmd)

	evalCmd.Flags().String("code", "", "DSL-фрагмент тела функции; если не задан, args считаются выражением")
	evalCmd.Flags().String("file", "", "прочитать DSL-фрагмент из файла")
	rootCmd.AddCommand(evalCmd)

	addBaseFlags(widgetExplainCmd)
	widgetExplainCmd.Flags().Int("sample", 0, "выполнить запрос и вернуть первые N строк")
	widgetCmd.AddCommand(widgetExplainCmd)
	rootCmd.AddCommand(widgetCmd)

	addBaseFlags(reportExplainCmd)
	reportExplainCmd.Flags().String("params", "{}", "JSON-объект параметров отчёта")
	reportExplainCmd.Flags().Int("sample", 0, "выполнить запрос и вернуть первые N строк")
	reportCmd.AddCommand(reportExplainCmd)
	rootCmd.AddCommand(reportCmd)
}

func runQueryCLI(cmd *cobra.Command, args []string) error {
	bc, proj, cleanup, err := loadCLIProject(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	text, err := queryTextFromFlags(cmd, args)
	if err != nil {
		return err
	}
	params, err := jsonParams(cmd)
	if err != nil {
		return err
	}
	compiled, err := compileProjectQuery(text, params, bc, proj)
	if err != nil {
		return err
	}
	out := map[string]any{
		"sql":     compiled.SQL,
		"args":    compiled.Args,
		"sources": compiled.Sources,
	}
	sqlOnly, _ := cmd.Flags().GetBool("sql")
	if !sqlOnly {
		db, err := bc.OpenDB(context.Background())
		if err != nil {
			return err
		}
		defer db.Close()
		limit, _ := cmd.Flags().GetInt("limit")
		rows, cols, truncated, err := runLimitedQuery(context.Background(), db, compiled.SQL, compiled.Args, limit)
		if err != nil {
			return err
		}
		out["columns"] = cols
		out["rows"] = rows
		out["rowCount"] = len(rows)
		out["truncated"] = truncated
	}
	return printJSON(out)
}

func runEvalCLI(cmd *cobra.Command, args []string) error {
	src, err := evalSourceFromFlags(cmd, args)
	if err != nil {
		return err
	}
	prog, err := parser.New(lexer.New(src, "eval.os")).ParseProgram()
	if err != nil {
		return err
	}
	if len(prog.Procedures) == 0 {
		return fmt.Errorf("eval: не найдено выполняемой функции")
	}
	var result any
	if err := interpreter.New().RunSandboxed(prog.Procedures[0], nil, interpreter.RestrictedProfile(), &result); err != nil {
		return err
	}
	return printJSON(map[string]any{"result": result})
}

func runWidgetExplain(cmd *cobra.Command, args []string) error {
	bc, proj, cleanup, err := loadCLIProject(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	w := findWidget(proj.Widgets, args[0])
	if w == nil {
		return fmt.Errorf("виджет %q не найден", args[0])
	}
	out := map[string]any{
		"name":      w.Name,
		"type":      w.Type,
		"title":     w.Title,
		"query":     w.Query,
		"chartKind": w.ChartKind,
		"xField":    w.XField,
		"yFields":   w.YFields,
		"columns":   w.Columns,
		"items":     w.Items,
		"entities":  w.Entities,
	}
	if strings.TrimSpace(w.Query) != "" {
		params := make(map[string]any, len(w.Params))
		for k, v := range w.Params {
			params[k] = v
		}
		params = scheduler.ResolveParamTemplates(params)
		compiled, err := compileProjectQuery(w.Query, params, bc, proj)
		if err != nil {
			out["compileError"] = err.Error()
		} else {
			out["sql"] = compiled.SQL
			out["args"] = compiled.Args
			out["sources"] = compiled.Sources
			if err := maybeAddSample(cmd, bc, compiled, out); err != nil {
				return err
			}
		}
	}
	return printJSON(out)
}

func runReportExplain(cmd *cobra.Command, args []string) error {
	bc, proj, cleanup, err := loadCLIProject(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	rep := findReport(proj, args[0])
	if rep == nil {
		return fmt.Errorf("отчёт %q не найден", args[0])
	}
	params, err := jsonParams(cmd)
	if err != nil {
		return err
	}
	compiled, err := compileProjectQuery(rep.Query, params, bc, proj)
	out := map[string]any{
		"name":        rep.Name,
		"title":       rep.Title,
		"params":      rep.Params,
		"query":       rep.Query,
		"chartProc":   rep.ChartProc,
		"composition": rep.Composition,
		"variants":    rep.Variants,
	}
	if err != nil {
		out["compileError"] = err.Error()
		return printJSON(out)
	}
	out["sql"] = compiled.SQL
	out["args"] = compiled.Args
	out["sources"] = compiled.Sources
	if err := maybeAddSample(cmd, bc, compiled, out); err != nil {
		return err
	}
	return printJSON(out)
}

func loadCLIProject(cmd *cobra.Command) (*baseConfig, *project.Project, func(), error) {
	bc, err := resolveBase(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	proj, err := project.Load(bc.Dir)
	if err != nil {
		bc.Cleanup()
		return nil, nil, nil, err
	}
	cleanup := func() {
		proj.Close()
		bc.Cleanup()
	}
	return bc, proj, cleanup, nil
}

func queryTextFromFlags(cmd *cobra.Command, args []string) (string, error) {
	if file, _ := cmd.Flags().GetString("file"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return stripQueryQuotesCLI(string(data)), nil
	}
	if len(args) > 0 {
		return stripQueryQuotesCLI(strings.Join(args, " ")), nil
	}
	info, statErr := os.Stdin.Stat()
	if statErr == nil && (info.Mode()&os.ModeCharDevice) != 0 {
		return "", fmt.Errorf("передайте текст запроса аргументом, через --file или stdin")
	}
	data, err := io.ReadAll(os.Stdin)
	if err == nil && len(data) > 0 {
		return stripQueryQuotesCLI(string(data)), nil
	}
	return "", fmt.Errorf("передайте текст запроса аргументом, через --file или stdin")
}

func jsonParams(cmd *cobra.Command) (map[string]any, error) {
	raw, _ := cmd.Flags().GetString("params")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("--params: %w", err)
	}
	coerceCLIParams(params)
	return params, nil
}

func compileProjectQuery(text string, params map[string]any, bc *baseConfig, proj *project.Project) (query.Result, error) {
	return query.Compile(text, query.CompileOpts{
		Params:      params,
		Entities:    proj.Entities,
		Registers:   proj.Registers,
		InfoRegs:    proj.InfoRegisters,
		AccountRegs: proj.AccountRegisters,
		Dialect:     dialectForBase(bc),
	})
}

func dialectForBase(bc *baseConfig) storage.Dialect {
	if bc != nil && bc.DBType == "sqlite" {
		return storage.SQLiteDialect{}
	}
	return storage.PgDialect{}
}

func maybeAddSample(cmd *cobra.Command, bc *baseConfig, compiled query.Result, out map[string]any) error {
	sample, _ := cmd.Flags().GetInt("sample")
	if sample <= 0 {
		return nil
	}
	db, err := bc.OpenDB(context.Background())
	if err != nil {
		return err
	}
	defer db.Close()
	rows, cols, truncated, err := runLimitedQuery(context.Background(), db, compiled.SQL, compiled.Args, sample)
	if err != nil {
		return err
	}
	out["columns"] = cols
	out["sampleRows"] = rows
	out["sampleTruncated"] = truncated
	return nil
}

func runLimitedQuery(ctx context.Context, db *storage.DB, sql string, args []any, limit int) ([]map[string]any, []string, bool, error) {
	if limit <= 0 {
		rows, cols, err := db.RunQuery(ctx, sql, args)
		return rows, cols, false, err
	}
	limitedSQL := strings.TrimSpace(sql)
	for strings.HasSuffix(limitedSQL, ";") {
		limitedSQL = strings.TrimSpace(strings.TrimSuffix(limitedSQL, ";"))
	}
	limitedSQL = fmt.Sprintf("SELECT * FROM (%s) AS _onebase_cli_limit LIMIT %d", limitedSQL, limit+1)
	rows, cols, err := db.RunQuery(ctx, limitedSQL, args)
	if err != nil {
		return nil, nil, false, err
	}
	if len(rows) <= limit {
		return rows, cols, false, nil
	}
	return rows[:limit], cols, true, nil
}

func evalSourceFromFlags(cmd *cobra.Command, args []string) (string, error) {
	if file, _ := cmd.Flags().GetString("file"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return wrapEvalBody(string(data)), nil
	}
	if code, _ := cmd.Flags().GetString("code"); strings.TrimSpace(code) != "" {
		return wrapEvalBody(code), nil
	}
	if len(args) == 0 {
		return "", fmt.Errorf("передайте выражение аргументом, --code или --file")
	}
	expr := strings.Join(args, " ")
	return "Функция __eval()\nВозврат (" + expr + ");\nКонецФункции\n", nil
}

func wrapEvalBody(body string) string {
	return "Функция __eval()\n" + body + "\nКонецФункции\n"
}

func findWidget(items []*metadata.Widget, name string) *metadata.Widget {
	for _, w := range items {
		if strings.EqualFold(w.Name, name) {
			return w
		}
	}
	return nil
}

func findReport(proj *project.Project, name string) *report.Report {
	for _, r := range proj.Reports {
		if strings.EqualFold(r.Name, name) {
			return r
		}
	}
	return nil
}

func stripQueryQuotesCLI(q string) string {
	q = strings.TrimSpace(q)
	if len(q) >= 2 {
		if (q[0] == '\'' && q[len(q)-1] == '\'') || (q[0] == '"' && q[len(q)-1] == '"') {
			return strings.TrimSpace(q[1 : len(q)-1])
		}
	}
	return q
}

func coerceCLIParams(params map[string]any) {
	for k, v := range params {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if _, err := uuid.Parse(strings.TrimSpace(s)); err == nil {
			continue
		}
		for _, layout := range []string{
			"02.01.2006 15:04",
			"02.01.2006 15:04:05",
			"02.01.2006",
			"2006-01-02",
		} {
			if t, err := time.Parse(layout, s); err == nil {
				params[k] = t
				break
			}
		}
		if _, ok := params[k].(time.Time); ok {
			continue
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			params[k] = f
		}
	}
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
