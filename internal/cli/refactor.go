package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/spf13/cobra"
)

var refactorCmd = &cobra.Command{
	Use:   "refactor",
	Short: "Безопасные preview/write helpers для переименований",
	Long: `Строит file-level preview для опасных правок конфигурации. По умолчанию
ничего не пишет. Запись включается только через --write; после записи запускается
onebase check, а при красном результате изменения откатываются.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var refactorRenameObjectCmd = &cobra.Command{
	Use:           "rename-object",
	Short:         "Переименовать объект метаданных с patch preview",
	RunE:          runRefactorRenameObject,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var refactorRenameFieldCmd = &cobra.Command{
	Use:           "rename-field",
	Short:         "Переименовать поле объекта с patch preview",
	RunE:          runRefactorRenameField,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(refactorRenameObjectCmd)
	refactorRenameObjectCmd.Flags().String("from", "", "старое имя объекта")
	refactorRenameObjectCmd.Flags().String("to", "", "новое имя объекта")
	refactorRenameObjectCmd.Flags().Bool("write", false, "применить изменения к файлам")
	refactorRenameObjectCmd.Flags().Bool("json", false, "вывести JSON")

	addBaseFlags(refactorRenameFieldCmd)
	refactorRenameFieldCmd.Flags().String("object", "", "имя объекта-владельца поля")
	refactorRenameFieldCmd.Flags().String("from", "", "старое имя поля")
	refactorRenameFieldCmd.Flags().String("to", "", "новое имя поля")
	refactorRenameFieldCmd.Flags().Bool("write", false, "применить изменения к файлам")
	refactorRenameFieldCmd.Flags().Bool("json", false, "вывести JSON")

	refactorCmd.AddCommand(refactorRenameObjectCmd, refactorRenameFieldCmd)
	rootCmd.AddCommand(refactorCmd)
}

type refactorResult struct {
	Type           string                 `json:"type"`
	Object         string                 `json:"object,omitempty"`
	From           string                 `json:"from"`
	To             string                 `json:"to"`
	Write          bool                   `json:"write"`
	RolledBack     bool                   `json:"rolledBack,omitempty"`
	Changes        []refactorChange       `json:"changes"`
	MigrationNotes []string               `json:"migrationNotes"`
	Impact         *impactReport          `json:"impact,omitempty"`
	Check          *configcheck.Result    `json:"check,omitempty"`
	Stats          map[string]interface{} `json:"stats,omitempty"`
}

type refactorChange struct {
	File         string            `json:"file"`
	RenameTo     string            `json:"renameTo,omitempty"`
	Replacements int               `json:"replacements"`
	Preview      []refactorPreview `json:"preview,omitempty"`
}

type refactorPreview struct {
	Line   int    `json:"line"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type refactorOp struct {
	rel        string
	renameTo   string
	oldContent []byte
	newContent []byte
	change     refactorChange
}

func runRefactorRenameObject(cmd *cobra.Command, _ []string) error {
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")
	write, _ := cmd.Flags().GetBool("write")
	jsonOut, _ := cmd.Flags().GetBool("json")
	return runRefactor(cmd, refactorRequest{typ: "rename-object", from: from, to: to, write: write, jsonOut: jsonOut})
}

func runRefactorRenameField(cmd *cobra.Command, _ []string) error {
	object, _ := cmd.Flags().GetString("object")
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")
	write, _ := cmd.Flags().GetBool("write")
	jsonOut, _ := cmd.Flags().GetBool("json")
	return runRefactor(cmd, refactorRequest{typ: "rename-field", object: object, from: from, to: to, write: write, jsonOut: jsonOut})
}

type refactorRequest struct {
	typ     string
	object  string
	from    string
	to      string
	write   bool
	jsonOut bool
}

func runRefactor(cmd *cobra.Command, req refactorRequest) error {
	req.from = strings.TrimSpace(req.from)
	req.to = strings.TrimSpace(req.to)
	req.object = strings.TrimSpace(req.object)
	if req.from == "" || req.to == "" {
		return fmt.Errorf("укажите --from и --to")
	}
	if req.from == req.to {
		return fmt.Errorf("--from и --to совпадают")
	}
	if req.typ == "rename-field" && req.object == "" {
		return fmt.Errorf("для rename-field укажите --object")
	}
	if !validIdentifier(req.to) {
		return fmt.Errorf("новое имя %q не похоже на идентификатор OneBase", req.to)
	}

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	if req.write && bc.cleanup != nil {
		return fmt.Errorf("--write для database-backed --id пока запрещён: refactor работает с реальными файлами проекта, а не временным экспортом")
	}

	ops, impact, err := buildRefactorOps(bc.Dir, req)
	if err != nil {
		return err
	}
	res := refactorResult{
		Type:           req.typ,
		Object:         req.object,
		From:           req.from,
		To:             req.to,
		Write:          req.write,
		Impact:         impact,
		MigrationNotes: refactorMigrationNotes(req),
		Stats:          map[string]interface{}{"files": len(ops), "replacements": refactorReplacementCount(ops)},
	}
	for _, op := range ops {
		res.Changes = append(res.Changes, op.change)
	}
	if req.write && len(ops) > 0 {
		check, rolledBack, err := applyRefactorOps(bc.Dir, ops)
		res.Check = &check
		res.RolledBack = rolledBack
		if err != nil {
			if req.jsonOut {
				_ = json.NewEncoder(os.Stdout).Encode(res)
			}
			return err
		}
	}
	if req.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	printRefactorResult(res)
	return nil
}

func buildRefactorOps(root string, req refactorRequest) ([]refactorOp, *impactReport, error) {
	var impact *impactReport
	var allowed map[string]bool
	if req.typ == "rename-field" {
		rep, err := scanImpact(root, req.object, req.from, "")
		if err != nil {
			return nil, nil, err
		}
		impact = &rep
		if p := findEntityFile(root, req.object); p != "" {
			allowed = refactorFieldAllowedFiles(rep, p)
		} else {
			allowed = refactorFieldAllowedFiles(rep, "")
		}
	} else {
		rep, err := scanImpact(root, req.from, "", "")
		if err != nil {
			return nil, nil, err
		}
		impact = &rep
	}

	files, err := collectRefactorFiles(root)
	if err != nil {
		return nil, nil, err
	}
	var ops []refactorOp
	for _, rel := range files {
		if allowed != nil && !allowed[rel] {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		raw, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, err
		}
		next, previews, count := replaceIdentifierWithPreview(string(raw), req.from, req.to)
		renameTo := ""
		if req.typ == "rename-object" {
			renameTo = renamedObjectPath(rel, req.from, req.to)
		}
		if count == 0 && renameTo == "" {
			continue
		}
		ops = append(ops, refactorOp{
			rel:        rel,
			renameTo:   renameTo,
			oldContent: raw,
			newContent: []byte(next),
			change: refactorChange{
				File:         rel,
				RenameTo:     renameTo,
				Replacements: count,
				Preview:      previews,
			},
		})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].rel < ops[j].rel })
	return ops, impact, nil
}

func refactorFieldAllowedFiles(rep impactReport, objectFile string) map[string]bool {
	type flags struct {
		object bool
		field  bool
		direct bool
	}
	byFile := map[string]*flags{}
	for _, m := range rep.Matches {
		f := byFile[m.File]
		if f == nil {
			f = &flags{}
			byFile[m.File] = f
		}
		switch m.Kind {
		case "field":
			f.field = true
		case "qualified-field", "field-definition":
			f.direct = true
		default:
			f.object = true
		}
	}
	out := map[string]bool{}
	if objectFile != "" {
		out[objectFile] = true
	}
	for file, f := range byFile {
		if f.direct || (f.object && f.field) {
			out[file] = true
		}
	}
	return out
}

func collectRefactorFiles(root string) ([]string, error) {
	var files []string
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
		rel, _ := filepath.Rel(root, path)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, err
}

func replaceIdentifierWithPreview(src, from, to string) (string, []refactorPreview, int) {
	lines := strings.SplitAfter(src, "\n")
	var b strings.Builder
	var previews []refactorPreview
	total := 0
	for i, line := range lines {
		body := strings.TrimSuffix(line, "\n")
		replaced, n := replaceIdentifier(body, from, to)
		if n > 0 {
			total += n
			if len(previews) < 20 {
				previews = append(previews, refactorPreview{Line: i + 1, Before: strings.TrimSpace(body), After: strings.TrimSpace(replaced)})
			}
		}
		b.WriteString(replaced)
		if strings.HasSuffix(line, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), previews, total
}

func replaceIdentifier(s, from, to string) (string, int) {
	if from == "" {
		return s, 0
	}
	var b strings.Builder
	pos := 0
	count := 0
	for {
		idx := strings.Index(s[pos:], from)
		if idx < 0 {
			b.WriteString(s[pos:])
			break
		}
		idx += pos
		end := idx + len(from)
		if identifierBoundaryBefore(s, idx) && identifierBoundaryAfter(s, end) {
			b.WriteString(s[pos:idx])
			b.WriteString(to)
			pos = end
			count++
			continue
		}
		b.WriteString(s[pos:end])
		pos = end
	}
	if count == 0 {
		return s, 0
	}
	return b.String(), count
}

func identifierBoundaryBefore(s string, idx int) bool {
	if idx <= 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:idx])
	return !isIdentRune(r)
}

func identifierBoundaryAfter(s string, idx int) bool {
	if idx >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[idx:])
	return !isIdentRune(r)
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func renamedObjectPath(rel, from, to string) string {
	dir, file := filepath.ToSlash(filepath.Dir(rel)), filepath.Base(rel)
	if dir == "." {
		return ""
	}
	ext := filepath.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	if !strings.EqualFold(stem, strings.ToLower(from)) && !strings.EqualFold(stem, from) {
		return ""
	}
	switch dir {
	case "catalogs", "documents", "registers", "inforegs", "enums", "accounts", "accountregs", "reports", "widgets", "processors", "pages", "subsystems", "roles", "services", "scheduled":
		return dir + "/" + strings.ToLower(to) + ext
	default:
		return ""
	}
}

func findEntityFile(root, object string) string {
	for _, sub := range []string{"catalogs", "documents"} {
		rel := sub + "/" + strings.ToLower(object) + ".yaml"
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return rel
		}
	}
	proj, err := projectAIContextLoad(root)
	if err != nil {
		return ""
	}
	for _, e := range proj {
		if strings.EqualFold(e.name, object) {
			if e.kind == metadata.KindDocument {
				return "documents/" + strings.ToLower(e.name) + ".yaml"
			}
			return "catalogs/" + strings.ToLower(e.name) + ".yaml"
		}
	}
	return ""
}

type refactorEntityRef struct {
	name string
	kind metadata.Kind
}

func projectAIContextLoad(root string) ([]refactorEntityRef, error) {
	proj, err := loadProjectForRefactor(root)
	if err != nil {
		return nil, err
	}
	defer proj.Close()
	out := make([]refactorEntityRef, 0, len(proj.Entities))
	for _, e := range proj.Entities {
		out = append(out, refactorEntityRef{name: e.Name, kind: e.Kind})
	}
	return out, nil
}

func applyRefactorOps(root string, ops []refactorOp) (configcheck.Result, bool, error) {
	backups := map[string][]byte{}
	existed := map[string]bool{}
	for _, op := range ops {
		for _, rel := range []string{op.rel, op.renameTo} {
			if rel == "" {
				continue
			}
			if _, ok := backups[rel]; ok {
				continue
			}
			full := filepath.Join(root, filepath.FromSlash(rel))
			if data, err := os.ReadFile(full); err == nil {
				backups[rel] = data
				existed[rel] = true
			} else {
				backups[rel] = nil
				existed[rel] = false
			}
		}
	}
	for _, op := range ops {
		target := op.rel
		if op.renameTo != "" {
			target = op.renameTo
			if existed[target] {
				rollbackRefactor(root, backups, existed)
				return configcheck.Result{}, true, fmt.Errorf("целевой файл уже существует: %s", target)
			}
		}
		full := filepath.Join(root, filepath.FromSlash(target))
		if err := os.MkdirAll(filepath.Dir(full), fsmode.Dir); err != nil {
			rollbackRefactor(root, backups, existed)
			return configcheck.Result{}, true, err
		}
		if err := os.WriteFile(full, op.newContent, fsmode.File); err != nil {
			rollbackRefactor(root, backups, existed)
			return configcheck.Result{}, true, err
		}
		if op.renameTo != "" {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(op.rel))); err != nil && !os.IsNotExist(err) {
				rollbackRefactor(root, backups, existed)
				return configcheck.Result{}, true, err
			}
		}
	}
	check := configcheck.RunFull(root)
	if !check.OK {
		rollbackRefactor(root, backups, existed)
		return check, true, fmt.Errorf("refactor применён в staging, но onebase check стал красным; изменения откатаны")
	}
	return check, false, nil
}

func rollbackRefactor(root string, backups map[string][]byte, existed map[string]bool) {
	keys := make([]string, 0, len(backups))
	for rel := range backups {
		keys = append(keys, rel)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	for _, rel := range keys {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if !existed[rel] {
			_ = os.Remove(full)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(full), fsmode.Dir)
		_ = os.WriteFile(full, backups[rel], fsmode.File)
	}
}

func loadProjectForRefactor(root string) (*project.Project, error) {
	return project.Load(root)
}

func validIdentifier(s string) bool {
	if s == "" || strings.ContainsAny(s, `/\:*?"<>|`) || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		if !isIdentRune(r) {
			return false
		}
	}
	return true
}

func refactorReplacementCount(ops []refactorOp) int {
	n := 0
	for _, op := range ops {
		n += op.change.Replacements
	}
	return n
}

func refactorMigrationNotes(req refactorRequest) []string {
	switch req.typ {
	case "rename-object":
		return []string{
			"Проверьте миграцию данных: физическая таблица/права/формы объекта могут требовать отдельного переноса.",
			"После применения выполните `onebase check`, миграцию базы и smoke-запуск ключевых сценариев.",
		}
	case "rename-field":
		return []string{
			"Проверьте миграцию данных: колонка поля не переименовывается автоматически на уже существующей базе.",
			"Проверьте формы, отчёты, виджеты, роли и .os модули, где поле использовалось без квалификатора объекта.",
		}
	default:
		return nil
	}
}

func printRefactorResult(res refactorResult) {
	mode := "preview"
	if res.Write {
		mode = "write"
	}
	outf("Refactor %s (%s): %s -> %s\n", res.Type, mode, res.From, res.To)
	if res.Object != "" {
		outf("Объект: %s\n", res.Object)
	}
	if len(res.Changes) == 0 {
		outln("Изменений не найдено")
		return
	}
	for _, ch := range res.Changes {
		target := ""
		if ch.RenameTo != "" {
			target = " -> " + ch.RenameTo
		}
		outf("\n%s%s (%d замен)\n", ch.File, target, ch.Replacements)
		for _, p := range ch.Preview {
			outf("  L%d: %s\n       %s\n", p.Line, p.Before, p.After)
		}
	}
	if len(res.MigrationNotes) > 0 {
		outln("\nMigration notes:")
		for _, n := range res.MigrationNotes {
			outln("- " + n)
		}
	}
	if res.Check != nil {
		if res.Check.OK {
			outln("\ncheck: OK")
		} else {
			outf("\ncheck: FAIL (%d issues), rolledBack=%v\n", res.Check.Total, res.RolledBack)
		}
	}
}
