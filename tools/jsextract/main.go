// jsextract извлекает inline-<script> из Go-шаблонов и раскладывает их
// отдельными .js-файлами, чтобы CI мог прогнать по ним `node --check`.
//
// Зачем. Гейт синтаксиса JS (#686) отбирает файлы через `git ls-files '*.js'` и
// inline-скрипты внутри Go-шаблонов не видит физически. А они там есть и
// добавляются: #758 (Lucide-спрайт) добавил именно такой. Для компилятора Go
// это просто текст, поэтому синтаксическая ошибка проходит все гейты зелёной, а
// в браузере SyntaxError отменяет весь блок вместе со всеми обработчиками —
// тот же класс отказа, ради которого гейт и заводили (#684).
//
// Разбирается go/ast, а не текст файла. Наивный регексп по исходнику склеивает
// `<script>` из одного строкового литерала с `</script>` из другого и тащит в
// «скрипт» лежащий между ними Go-код вместе с комментариями — шесть блоков из
// сорока восьми оказывались ложно сломанными. Литерал — естественная граница.
//
// Что заменяется плейсхолдером (иначе node сообщил бы об ошибке на ровном
// месте, хотя JS корректен):
//   - действия шаблонизатора: сначала целиком {{if}}…{{end}} — в шаблонах они
//     стоят в позиции значения (`window.__cfg = {{if .B}}{{.B}}{{else}}{}{{end}};`),
//     затем одиночные {{.X}} / {{jsJSON .Y}};
//   - глаголы fmt (%s, %d, …) — Go-интерполяция того же рода.
//
// Проверяется именно СИНТАКСИС: плейсхолдер делает выражение синтаксически
// корректным, но смысла не сохраняет. Семантику этот гейт не ловит и не должен.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	// scriptRe — блок <script> с телом, целиком внутри одного литерала.
	scriptRe = regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`)
	// openRe/closeRe — для учёта блоков, разорванных конкатенацией Go.
	openRe  = regexp.MustCompile(`<script[^>]*>`)
	closeRe = regexp.MustCompile(`</script>`)
	// tmplIfRe — ветвление шаблона целиком. Вложенных if в inline-JS нет;
	// появятся — регулярка их не свернёт, и node честно сообщит об ошибке:
	// гейт останется fail-closed, а не пропустит блок молча.
	tmplIfRe     = regexp.MustCompile(`(?s)\{\{-?\s*(?:if|with|range)\b.*?\{\{-?\s*end\s*-?\}\}`)
	tmplActionRe = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	fmtVerbRe    = regexp.MustCompile(`%[-+# 0]*[\d.*]*[a-zA-Z]`)
	typeRe       = regexp.MustCompile(`type\s*=\s*"([^"]*)"`)
	// commentRe — HTML- и шаблонные комментарии. Вырезаются ДО поиска тегов:
	// в них встречается слово <script> как часть объяснения, и без этого
	// «телом скрипта» становится текст комментария вперемешку с разметкой.
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->|\{\{/\*.*?\*/\}\}`)
)

const placeholder = "0"

// block — извлечённый скрипт с координатами источника.
type block struct {
	file string
	line int
	body string
}

func main() {
	outDir := flag.String("out", "", "каталог для извлечённых .js (обязателен)")
	flag.Parse()
	if *outDir == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "использование: jsextract -out <каталог> <файл.go> [...]")
		os.Exit(2)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var all []block
	torn := 0
	for _, path := range flag.Args() {
		blocks, tornHere, err := extractFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		all = append(all, blocks...)
		torn += tornHere
	}

	for i, b := range all {
		name := fmt.Sprintf("%s.%d.js", strings.NewReplacer("/", "_", "\\", "_").Replace(b.file), i)
		// Первой строкой — откуда взято: иначе сообщение node указывает во
		// временный файл, и искать негде.
		out := fmt.Sprintf("// извлечено из %s:%d\n%s", b.file, b.line, b.body)
		if err := os.WriteFile(filepath.Join(*outDir, name), []byte(out), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fmt.Printf("извлечено inline-скриптов: %d\n", len(all))
	// Разорванные блоки называются вслух: молчаливое сокращение покрытия
	// читается как «проверено всё», хотя проверено не всё.
	if torn > 0 {
		fmt.Printf("НЕ проверено (тег разорван конкатенацией Go): %d\n", torn)
	}
	if len(all) == 0 {
		// Ноль блоков означает не «всё чисто», а сломанный отбор.
		fmt.Fprintln(os.Stderr, "::error::не извлечено ни одного inline-скрипта — сломан отбор файлов")
		os.Exit(1)
	}
}

func extractFile(path string) ([]block, int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("разбор %s: %w", path, err)
	}
	var out []block
	opened, closed := 0, 0
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		text = commentRe.ReplaceAllString(text, "")
		bodies := extract(text)
		opened += len(openRe.FindAllString(text, -1))
		closed += len(closeRe.FindAllString(text, -1))
		line := fset.Position(lit.Pos()).Line
		for _, b := range bodies {
			out = append(out, block{file: path, line: line, body: b})
		}
		return true
	})
	// Считаем непарные теги ПО ФАЙЛУ, а не по литералу: блок, собираемый
	// конкатенацией, даёт непарный тег в двух литералах сразу, и подсчёт по
	// литералам вдвое завышал бы число непроверенного.
	return out, abs(opened - closed), nil
}

// extract возвращает тела inline-скриптов литерала, пригодные для node --check.
func extract(text string) []string {
	var out []string
	for _, m := range scriptRe.FindAllStringSubmatch(text, -1) {
		attrs, body := m[1], m[2]
		if strings.Contains(attrs, "src=") {
			continue
		}
		// type="application/json" и любой другой не-JS тип — это данные,
		// которые браузер не исполняет.
		if t := typeAttr(attrs); t != "" && t != "text/javascript" && t != "module" {
			continue
		}
		body = tmplIfRe.ReplaceAllString(body, placeholder)
		body = tmplActionRe.ReplaceAllString(body, placeholder)
		body = fmtVerbRe.ReplaceAllString(body, placeholder)
		if strings.TrimSpace(body) == "" {
			continue
		}
		out = append(out, body)
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func typeAttr(attrs string) string {
	if m := typeRe.FindStringSubmatch(attrs); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
