package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	querylang "github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
	"github.com/spf13/cobra"
)

var widgetCmd = &cobra.Command{
	Use:   "widget",
	Short: "Инструменты для виджетов",
}

var widgetExplainCmd = &cobra.Command{
	Use:           "explain <name>",
	Short:         "Объяснить виджет: запрос, SQL, колонки, sample rows",
	Args:          cobra.ExactArgs(1),
	RunE:          runWidgetExplain,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Инструменты для отчётов",
}

var reportExplainCmd = &cobra.Command{
	Use:           "explain <name>",
	Short:         "Объяснить отчёт: запрос, SQL, компоновка, sample rows",
	Args:          cobra.ExactArgs(1),
	RunE:          runReportExplain,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(widgetExplainCmd)
	widgetExplainCmd.Flags().Int("sample", 0, "выполнить и вернуть первые N строк/результат виджета")
	widgetExplainCmd.Flags().Bool("json", false, "вывести JSON")
	widgetCmd.AddCommand(widgetExplainCmd)
	rootCmd.AddCommand(widgetCmd)

	addBaseFlags(reportExplainCmd)
	reportExplainCmd.Flags().Int("sample", 0, "выполнить и вернуть первые N строк")
	reportExplainCmd.Flags().String("params", "", "JSON-объект параметров отчёта")
	reportExplainCmd.Flags().Bool("json", false, "вывести JSON")
	reportCmd.AddCommand(reportExplainCmd)
	rootCmd.AddCommand(reportCmd)
}

type explainOutput struct {
	Kind        string              `json:"kind"`
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Type        string              `json:"type,omitempty"`
	Query       string              `json:"query,omitempty"`
	Params      map[string]any      `json:"params,omitempty"`
	SQL         string              `json:"sql,omitempty"`
	Args        []any               `json:"args,omitempty"`
	Sources     []querySourceOutput `json:"sources,omitempty"`
	Columns     []string            `json:"columns,omitempty"`
	Rows        []map[string]any    `json:"rows,omitempty"`
	ChartOption map[string]any      `json:"chartOption,omitempty"`
	Composition *report.Composition `json:"composition,omitempty"`
	Variants    []string            `json:"variants,omitempty"`
	Error       string              `json:"error,omitempty"`
}

func runWidgetExplain(cmd *cobra.Command, args []string) error {
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	proj, err := project.Load(bc.Dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()
	w := findWidget(proj, args[0])
	if w == nil {
		return fmt.Errorf("виджет %q не найден", args[0])
	}
	out := explainOutput{
		Kind:  "widget",
		Name:  w.Name,
		Title: w.Title,
		Type:  string(w.Type),
		Query: w.Query,
	}
	params := map[string]any{}
	for k, v := range scheduler.ResolveParamTemplates(copyStringMap(w.Params)) {
		params[k] = v
	}
	if len(params) > 0 {
		out.Params = params
	}
	// Открываем БД заранее, если запрошен --sample: её диалект нужен и для
	// исполнения виджета, и чтобы показанный SQL совпадал с фактическим (иначе
	// на SQLite генерятся Postgres-плейсхолдеры $N::text). См. issue #473.
	sample, _ := cmd.Flags().GetInt("sample")
	var db *storage.DB
	if sample > 0 {
		db, err = bc.OpenDB(context.Background())
		if err != nil {
			return err
		}
		defer db.Close()
	}
	if strings.TrimSpace(w.Query) != "" {
		opts := querylang.CompileOpts{
			Params:      params,
			Entities:    proj.Entities,
			Registers:   proj.Registers,
			InfoRegs:    proj.InfoRegisters,
			AccountRegs: proj.AccountRegisters,
		}
		if db != nil {
			opts.Dialect = db.Dialect()
		}
		compiled, err := querylang.Compile(w.Query, opts)
		if err != nil {
			out.Error = "compile: " + err.Error()
		} else {
			out.SQL, out.Args, out.Sources = compiled.SQL, compiled.Args, toQuerySourceOutput(compiled.Sources)
		}
	}
	if sample > 0 {
		reg := buildRuntimeRegistry(proj)
		res := widget.New(reg, db).Run(context.Background(), w)
		if res.Error != "" {
			out.Error = res.Error
		}
		switch {
		case res.KPI != nil:
			out.Rows = []map[string]any{{"value": res.KPI.Value, "display": res.KPI.Display}}
			out.Columns = []string{"value", "display"}
		case len(res.Rows) > 0:
			out.Rows = truncateRows(res.Rows, sample)
			for _, c := range res.Columns {
				out.Columns = append(out.Columns, c.Field)
			}
		case res.Chart != nil:
			out.ChartOption = widget.EChartsOption(res.Chart)
		}
	}
	return printExplain(cmd, out)
}

func runReportExplain(cmd *cobra.Command, args []string) error {
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	proj, err := project.Load(bc.Dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()
	rep := findReport(proj, args[0])
	if rep == nil {
		return fmt.Errorf("отчёт %q не найден", args[0])
	}
	params, err := paramsFromJSONFlag(cmd)
	if err != nil {
		return err
	}
	out := explainOutput{
		Kind:        "report",
		Name:        rep.Name,
		Title:       rep.Title,
		Query:       rep.Query,
		Params:      params,
		Composition: rep.Composition,
	}
	for _, v := range rep.Variants {
		out.Variants = append(out.Variants, v.Name)
	}
	if strings.TrimSpace(rep.Query) != "" {
		// При --sample открываем БД до компиляции: без её диалекта компилятор
		// генерит Postgres-плейсхолдеры $N::text, и исполнение на SQLite падает
		// с «missing named argument». См. issue #473.
		sample, _ := cmd.Flags().GetInt("sample")
		var db *storage.DB
		if sample > 0 {
			db, err = bc.OpenDB(context.Background())
			if err != nil {
				return err
			}
			defer db.Close()
		}
		opts := querylang.CompileOpts{
			Params:      params,
			Entities:    proj.Entities,
			Registers:   proj.Registers,
			InfoRegs:    proj.InfoRegisters,
			AccountRegs: proj.AccountRegisters,
		}
		if db != nil {
			opts.Dialect = db.Dialect()
		}
		compiled, err := querylang.Compile(rep.Query, opts)
		if err != nil {
			out.Error = "compile: " + err.Error()
		} else {
			out.SQL, out.Args, out.Sources = compiled.SQL, compiled.Args, toQuerySourceOutput(compiled.Sources)
			if sample > 0 {
				rows, cols, err := db.RunQuery(context.Background(), "SELECT * FROM ("+compiled.SQL+") _onebase_r LIMIT "+fmt.Sprint(sample), compiled.Args)
				if err != nil {
					out.Error = "execute: " + err.Error()
				} else {
					// SQL обёрнут в подзапрос, поэтому query.Run тут не подходит;
					// приведение зовём явно — иначе выборка показывала бы 1/0
					// на SQLite и true/false на PostgreSQL.
					querylang.NormalizeColumns(&compiled, rows)
					out.Columns, out.Rows = cols, rows
				}
			}
		}
	}
	return printExplain(cmd, out)
}

func printExplain(cmd *cobra.Command, out explainOutput) error {
	jsonOut, _ := cmd.Flags().GetBool("json")
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	outf("%s %s", out.Kind, out.Name)
	if out.Title != "" {
		outf(" — %s", out.Title)
	}
	outln()
	if out.Type != "" {
		outf("type: %s\n", out.Type)
	}
	if out.Query != "" {
		outf("\nЗапрос:\n%s\n", out.Query)
	}
	if out.SQL != "" {
		outf("\nSQL:\n%s\nARGS: %v\n", out.SQL, out.Args)
	}
	if len(out.Sources) > 0 {
		outf("sources: %v\n", out.Sources)
	}
	if out.Error != "" {
		outf("\nОшибка: %s\n", out.Error)
	}
	if len(out.Rows) > 0 {
		outln("\nSample:")
		printRowsText(out.Columns, out.Rows)
	}
	return nil
}

func findWidget(proj *project.Project, name string) *metadataWidget {
	for _, w := range proj.Widgets {
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

type metadataWidget = metadata.Widget

func copyStringMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func truncateRows(rows []map[string]any, limit int) []map[string]any {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}
