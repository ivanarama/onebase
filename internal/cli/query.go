package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/project"
	querylang "github.com/ivantit66/onebase/internal/query"
	"github.com/spf13/cobra"
)

var queryCmd = &cobra.Command{
	Use:   "query <текст запроса>",
	Short: "Выполнить read-only запрос OneBase headless",
	Long: `Компилирует запрос OneBase в SQL и выполняет его на выбранной базе.
Команда предназначена для быстрой проверки гипотез ИИ и разработчиком.
Разрешены только запросы, начинающиеся с ВЫБРАТЬ или SELECT.`,
	Args:          cobra.MaximumNArgs(1),
	RunE:          runQuery,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(queryCmd)
	queryCmd.Flags().String("file", "", "прочитать текст запроса из файла")
	queryCmd.Flags().String("params", "", "JSON-объект параметров &Имя")
	queryCmd.Flags().Int("limit", 100, "максимум строк результата; 0 = без усечения")
	queryCmd.Flags().Bool("json", false, "вывести результат в JSON")
	queryCmd.Flags().Bool("sql", false, "показать скомпилированный SQL")
	rootCmd.AddCommand(queryCmd)
}

type queryOutput struct {
	SQL     string              `json:"sql,omitempty"`
	Args    []any               `json:"args,omitempty"`
	Columns []string            `json:"columns"`
	Rows    []map[string]any    `json:"rows"`
	Count   int                 `json:"count"`
	Elapsed string              `json:"elapsed"`
	Sources []querySourceOutput `json:"sources,omitempty"`
}

type querySourceOutput struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func runQuery(cmd *cobra.Command, args []string) error {
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	text, err := queryTextFromFlags(cmd, args)
	if err != nil {
		return err
	}
	text = stripOuterQuotes(strings.TrimSpace(text))
	if text == "" {
		return fmt.Errorf("пустой запрос")
	}
	if !isSelectQuery(text) {
		return fmt.Errorf("onebase query выполняет только read-only запросы ВЫБРАТЬ/SELECT")
	}
	params, err := paramsFromJSONFlag(cmd)
	if err != nil {
		return err
	}

	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	proj, err := project.Load(bc.Dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	compiled, err := querylang.Compile(text, querylang.CompileOpts{
		Params:      params,
		Entities:    proj.Entities,
		Registers:   proj.Registers,
		InfoRegs:    proj.InfoRegisters,
		AccountRegs: proj.AccountRegisters,
		Dialect:     db.Dialect(),
	})
	if err != nil {
		return fmt.Errorf("compile query: %w", err)
	}

	limit, _ := cmd.Flags().GetInt("limit")
	sqlText := compiled.SQL
	if limit > 0 {
		sqlText = "SELECT * FROM (" + sqlText + ") _onebase_q LIMIT " + fmt.Sprint(limit)
	}
	start := time.Now()
	rows, cols, err := db.RunQuery(ctx, sqlText, compiled.Args)
	if err != nil {
		return err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	if cols == nil {
		cols = []string{}
	}
	elapsed := time.Since(start).Round(time.Millisecond)

	jsonOut, _ := cmd.Flags().GetBool("json")
	showSQL, _ := cmd.Flags().GetBool("sql")
	out := queryOutput{
		Columns: cols,
		Rows:    rows,
		Count:   len(rows),
		Elapsed: elapsed.String(),
		Sources: toQuerySourceOutput(compiled.Sources),
	}
	if showSQL || jsonOut {
		out.SQL = sqlText
		out.Args = compiled.Args
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if showSQL {
		outf("SQL:\n%s\nARGS: %v\n\n", sqlText, compiled.Args)
	}
	printRowsText(cols, rows)
	outf("\n%d строк, %s\n", len(rows), elapsed)
	return nil
}

func toQuerySourceOutput(in []querylang.SourceRef) []querySourceOutput {
	if len(in) == 0 {
		return nil
	}
	out := make([]querySourceOutput, len(in))
	for i, src := range in {
		out[i] = querySourceOutput{Kind: src.Kind, Name: src.Name}
	}
	return out
}

func queryTextFromFlags(cmd *cobra.Command, args []string) (string, error) {
	path, _ := cmd.Flags().GetString("file")
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if len(args) > 0 {
		return args[0], nil
	}
	if st, err := os.Stdin.Stat(); err == nil && (st.Mode()&os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("укажите текст запроса аргументом или через --file")
}

func paramsFromJSONFlag(cmd *cobra.Command) (map[string]any, error) {
	raw, _ := cmd.Flags().GetString("params")
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("--params должен быть JSON-объектом: %w", err)
	}
	return params, nil
}

func isSelectQuery(text string) bool {
	up := strings.ToUpper(strings.TrimLeftFunc(text, func(r rune) bool {
		return r == '\ufeff' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	}))
	return strings.HasPrefix(up, "ВЫБРАТЬ") || strings.HasPrefix(up, "SELECT")
}

func stripOuterQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func printRowsText(cols []string, rows []map[string]any) {
	if len(cols) == 0 && len(rows) > 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}
	if len(cols) == 0 {
		return
	}
	outln(strings.Join(cols, "\t"))
	for _, row := range rows {
		vals := make([]string, len(cols))
		for i, col := range cols {
			vals[i] = formatCLIValue(row[col])
		}
		outln(strings.Join(vals, "\t"))
	}
}

func formatCLIValue(v any) string {
	switch t := v.(type) {
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
