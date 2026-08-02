package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/project"
	querylang "github.com/ivantit66/onebase/internal/query"
	"github.com/spf13/cobra"
)

var impactCmd = &cobra.Command{
	Use:   "impact",
	Short: "Показать статический impact изменения объекта/поля/процедуры",
	Long: `Ищет ссылки на объект, поле или процедуру в YAML и DSL-файлах проекта.
Это быстрый read-only анализ для ИИ-правок: перед удалением/переименованием
можно увидеть затронутые формы, запросы, виджеты, отчёты и модули.`,
	RunE:          runImpact,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(impactCmd)
	impactCmd.Flags().String("object", "", "имя объекта метаданных")
	impactCmd.Flags().String("field", "", "имя поля внутри объекта")
	impactCmd.Flags().String("procedure", "", "имя процедуры/функции")
	impactCmd.Flags().Bool("json", false, "вывести JSON")
	rootCmd.AddCommand(impactCmd)
}

type impactReport struct {
	Object         string         `json:"object,omitempty"`
	Field          string         `json:"field,omitempty"`
	Procedure      string         `json:"procedure,omitempty"`
	Matches        []impactMatch  `json:"matches"`
	Summary        map[string]int `json:"summary,omitempty"`
	MigrationNotes []string       `json:"migrationNotes,omitempty"`
}

type impactMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Kind    string `json:"kind"`
	Snippet string `json:"snippet"`
}

type impactNeedle struct {
	Text string
	Kind string
}

func runImpact(cmd *cobra.Command, _ []string) error {
	object, _ := cmd.Flags().GetString("object")
	field, _ := cmd.Flags().GetString("field")
	procedure, _ := cmd.Flags().GetString("procedure")
	object = strings.TrimSpace(object)
	field = strings.TrimSpace(field)
	procedure = strings.TrimSpace(procedure)
	if object == "" && field == "" && procedure == "" {
		return fmt.Errorf("укажите --object, --field или --procedure")
	}
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	rep, err := scanImpact(bc.Dir, object, field, procedure)
	if err != nil {
		return err
	}
	jsonOut, _ := cmd.Flags().GetBool("json")
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if len(rep.Matches) == 0 {
		outln("Совпадений не найдено")
		return nil
	}
	for _, m := range rep.Matches {
		outf("%s:%d [%s] %s\n", m.File, m.Line, m.Kind, m.Snippet)
	}
	outf("\nВсего совпадений: %d\n", len(rep.Matches))
	return nil
}

func scanImpact(root, object, field, procedure string) (impactReport, error) {
	rep := impactReport{Object: object, Field: field, Procedure: procedure}
	needles := impactNeedles(object, field, procedure)
	if len(needles) == 0 {
		return rep, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".hg", ".svn", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		low := strings.ToLower(path)
		if !strings.HasSuffix(low, ".yaml") && !strings.HasSuffix(low, ".yml") && !strings.HasSuffix(low, ".os") {
			return nil
		}
		matches, err := scanImpactFile(root, path, needles)
		if err != nil {
			return err
		}
		rep.Matches = append(rep.Matches, matches...)
		return nil
	})
	if err == nil {
		rep.Matches = append(rep.Matches, scanProjectImpact(root, object, field)...)
	}
	sort.Slice(rep.Matches, func(i, j int) bool {
		if rep.Matches[i].File == rep.Matches[j].File {
			return rep.Matches[i].Line < rep.Matches[j].Line
		}
		return rep.Matches[i].File < rep.Matches[j].File
	})
	rep.Matches = dedupeImpactMatches(rep.Matches)
	rep.Summary = impactSummary(rep.Matches)
	rep.MigrationNotes = impactMigrationNotes(object, field, procedure)
	return rep, err
}

func impactNeedles(object, field, procedure string) []impactNeedle {
	var out []impactNeedle
	add := func(kind, s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, impactNeedle{Text: strings.ToLower(s), Kind: kind})
		}
	}
	if object != "" && field != "" {
		add("qualified-field", object+"."+field)
	}
	if object != "" {
		add("query-source", "Справочник."+object)
		add("query-source", "Документ."+object)
		add("query-source", "РегистрНакопления."+object)
		add("query-source", "РегистрСведений."+object)
		add("query-source", "РегистрБухгалтерии."+object)
		add("reference", "reference:"+object)
		add("yaml-ref", "entity: "+object)
	}
	add("object", object)
	add("field", field)
	add("procedure", procedure)
	return out
}

func scanImpactFile(root, path string, needles []impactNeedle) ([]impactMatch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rel, _ := filepath.Rel(root, path)
	var out []impactMatch
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		low := strings.ToLower(text)
		for _, needle := range needles {
			if strings.Contains(low, needle.Text) {
				out = append(out, impactMatch{
					File:    filepath.ToSlash(rel),
					Line:    line,
					Kind:    needle.Kind,
					Snippet: strings.TrimSpace(text),
				})
				break
			}
		}
	}
	return out, sc.Err()
}

func scanProjectImpact(root, object, field string) []impactMatch {
	proj, err := project.Load(root)
	if err != nil {
		return nil
	}
	defer proj.Close()
	var out []impactMatch
	eq := func(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }
	for _, e := range proj.Entities {
		file := entityImpactPath(e.Kind, e.Name)
		for _, f := range e.Fields {
			if object != "" && eq(f.RefEntity, object) {
				out = append(out, impactMatch{File: file, Kind: "metadata-reference", Snippet: e.Name + "." + f.Name + " -> " + object})
			}
			if object != "" && field != "" && eq(e.Name, object) && eq(f.Name, field) {
				out = append(out, impactMatch{File: file, Kind: "field-definition", Snippet: e.Name + "." + f.Name})
			}
		}
		for _, tp := range e.TableParts {
			for _, f := range tp.Fields {
				if object != "" && eq(f.RefEntity, object) {
					out = append(out, impactMatch{File: file, Kind: "metadata-reference", Snippet: e.Name + "." + tp.Name + "." + f.Name + " -> " + object})
				}
				if object != "" && field != "" && eq(e.Name, object) && eq(f.Name, field) {
					out = append(out, impactMatch{File: file, Kind: "field-definition", Snippet: e.Name + "." + tp.Name + "." + f.Name})
				}
			}
		}
		for _, src := range e.BasedOn {
			if object != "" && eq(src, object) {
				out = append(out, impactMatch{File: file, Kind: "based-on", Snippet: e.Name + " based_on " + object})
			}
		}
	}
	opts := querylang.CompileOpts{
		Entities:    proj.Entities,
		Registers:   proj.Registers,
		InfoRegs:    proj.InfoRegisters,
		AccountRegs: proj.AccountRegisters,
	}
	for _, w := range proj.Widgets {
		if object == "" || strings.TrimSpace(w.Query) == "" {
			continue
		}
		params := map[string]any{}
		for k := range w.Params {
			params[k] = nil
		}
		o := opts
		o.Params = params
		if r, err := querylang.Compile(w.Query, o); err == nil && querySourcesObject(r.Sources, object) {
			out = append(out, impactMatch{File: "widgets/" + w.Name + ".yaml", Kind: "query-source-compiled", Snippet: "widget query reads " + object})
		}
	}
	for _, rep := range proj.Reports {
		if object == "" || strings.TrimSpace(rep.Query) == "" {
			continue
		}
		params := map[string]any{}
		for _, p := range rep.Params {
			params[p.Name] = nil
		}
		o := opts
		o.Params = params
		if r, err := querylang.Compile(rep.Query, o); err == nil && querySourcesObject(r.Sources, object) {
			out = append(out, impactMatch{File: "reports/" + rep.Name + ".yaml", Kind: "query-source-compiled", Snippet: "report query reads " + object})
		}
	}
	return out
}

func querySourcesObject(srcs []querylang.SourceRef, object string) bool {
	for _, src := range srcs {
		if strings.EqualFold(src.Name, object) {
			return true
		}
	}
	return false
}

func entityImpactPath(kind any, name string) string {
	subdir := "catalogs"
	if fmt.Sprint(kind) == "document" {
		subdir = "documents"
	}
	return subdir + "/" + strings.ToLower(name) + ".yaml"
}

func dedupeImpactMatches(in []impactMatch) []impactMatch {
	seen := map[string]bool{}
	out := make([]impactMatch, 0, len(in))
	for _, m := range in {
		key := fmt.Sprintf("%s:%d:%s:%s", m.File, m.Line, m.Kind, m.Snippet)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}

func impactSummary(matches []impactMatch) map[string]int {
	if len(matches) == 0 {
		return nil
	}
	out := map[string]int{}
	for _, m := range matches {
		out[m.Kind]++
	}
	return out
}

func impactMigrationNotes(object, field, procedure string) []string {
	var notes []string
	if object != "" {
		notes = append(notes, "Переименование или удаление объекта меняет физическую таблицу/права/формы; данные не переносятся автоматически без отдельной миграции.")
	}
	if object != "" && field != "" {
		notes = append(notes, "Переименование поля меняет колонку таблицы объекта; проверьте миграцию данных, формы, запросы, отчёты, виджеты и роли.")
	}
	if procedure != "" {
		notes = append(notes, "Переименование процедуры безопасно только после проверки всех обработчиков событий и прямых вызовов в .os модулях.")
	}
	return notes
}
