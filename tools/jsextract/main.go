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
//   - одиночные действия шаблонизатора {{.X}} / {{jsJSON .Y}};
//   - глаголы fmt (%s, %d, …), но только в format-аргументе известного
//     fmt.Sprintf/Fprintf/Appendf; обычный `%s` в литерале остаётся ошибкой.
//
// Ветвления {{if/with/range}} не вырезаются: для каждой ветви строится
// отдельный вариант скрипта. Иначе синтаксическая ошибка внутри условной ветви
// исчезла бы вместе с ней и гейт стал бы fail-open.
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
	scriptRe = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	// openRe/closeRe — для учёта блоков, разорванных конкатенацией Go.
	openRe       = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	closeRe      = regexp.MustCompile(`(?is)</script\s*>`)
	tmplActionRe = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
)

const placeholder = "0"

const goValuePlaceholder = "/* Go value omitted by jsextract */"

const maxTemplateVariants = 256

type extractedScript struct {
	body   string
	module bool
}

// block — извлечённый скрипт с координатами источника.
type block struct {
	file   string
	line   int
	body   string
	module bool
}

func main() {
	outDir := flag.String("out", "", "каталог для извлечённых .js (обязателен)")
	flag.Parse()
	if *outDir == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "использование: jsextract -out <каталог> <файл.go> [...]")
		os.Exit(2)
	}
	if err := os.MkdirAll(*outDir, 0o750); err != nil {
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
		if tornHere > 0 {
			fmt.Fprintf(os.Stderr, "::error::%s: непарных script-тегов в отдельных Go-литералах: %d\n", path, tornHere)
		}
	}

	for i, b := range all {
		ext := outputExtension(b.module)
		name := fmt.Sprintf("%s.%d%s", strings.NewReplacer("/", "_", "\\", "_").Replace(b.file), i, ext)
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
		fmt.Fprintf(os.Stderr, "::error::НЕ проверено (непарных script-тегов в отдельных Go-литералах): %d\n", torn)
		os.Exit(1)
	}
	if len(all) == 0 {
		// Ноль блоков означает не «всё чисто», а сломанный отбор.
		fmt.Fprintln(os.Stderr, "::error::не извлечено ни одного inline-скрипта — сломан отбор файлов")
		os.Exit(1)
	}
}

func outputExtension(module bool) string {
	if module {
		return ".mjs"
	}
	return ".js"
}

func extractFile(path string) ([]block, int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("разбор %s: %w", path, err)
	}
	var out []block
	torn := 0
	process := func(text string, pos token.Pos) {
		text = resolveGoValuePlaceholders(text)
		text = stripCommentsOutsideScripts(text)
		bodies, extractErr := extract(text)
		if extractErr != nil {
			err = fmt.Errorf("извлечение %s: %w", path, extractErr)
			return
		}
		opened := len(openRe.FindAllString(text, -1))
		closed := len(closeRe.FindAllString(text, -1))
		torn += abs(opened - closed)
		line := fset.Position(pos).Line
		for _, b := range bodies {
			out = append(out, block{file: path, line: line, body: b.body, module: b.module})
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if err != nil {
			return false
		}
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if node.Op != token.ADD {
				return true
			}
			text, ok := stringExpression(node)
			if !ok {
				return true
			}
			process(text, node.Pos())
			// The complete concatenation was processed; visiting its literals
			// again would duplicate complete scripts and report its fragments as
			// torn blocks.
			return false
		case *ast.CallExpr:
			text, ok := formatCallString(node)
			if !ok {
				return true
			}
			process(text, node.Pos())
			return false
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			text, unquoteErr := strconv.Unquote(node.Value)
			if unquoteErr != nil {
				return true
			}
			process(text, node.Pos())
			return true
		default:
			return true
		}
	})
	if err != nil {
		return nil, 0, err
	}
	// Непарность считается по каждой восстановленной строковой expression (или
	// отдельному литералу): теги из независимых statements не должны взаимно
	// погаситься. Такой блок экстрактор не видел, поэтому гейт останавливается.
	return out, torn, nil
}

// stringExpression reconstructs a compile-time string concatenation. Dynamic
// operands become a scalar placeholder, while nested fmt calls are rendered
// with their format directives normalized. The function deliberately handles
// only `+`: arbitrary Go code between two literals must never be copied into
// JavaScript as source text.
func stringExpression(expr ast.Expr) (string, bool) {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(node.Value)
		return text, err == nil
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, leftOK := stringExpression(node.X)
		right, rightOK := stringExpression(node.Y)
		if !leftOK && !rightOK {
			return "", false
		}
		if !leftOK {
			left = goValuePlaceholder
		}
		if !rightOK {
			right = goValuePlaceholder
		}
		return left + right, true
	case *ast.ParenExpr:
		return stringExpression(node.X)
	case *ast.CallExpr:
		return formatCallString(node)
	default:
		return "", false
	}
}

func formatCallString(call *ast.CallExpr) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return "", false
	}
	formatIndex := -1
	switch selector.Sel.Name {
	case "Sprintf":
		formatIndex = 0
	case "Fprintf", "Appendf":
		formatIndex = 1
	}
	if formatIndex < 0 || len(call.Args) <= formatIndex {
		return "", false
	}
	format, ok := stringExpression(call.Args[formatIndex])
	if !ok {
		return "", false
	}
	return normalizeFormatString(format), true
}

// normalizeFormatString approximates fmt's formatting pass for syntax
// checking: %% becomes a literal percent, while every value directive becomes
// a JavaScript scalar. It is called only for the format argument of known fmt
// functions, never for an arbitrary template literal containing `%s`.
func normalizeFormatString(format string) string {
	var out strings.Builder
	for i := 0; i < len(format); {
		if format[i] != '%' {
			out.WriteByte(format[i])
			i++
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			out.WriteByte('%')
			i += 2
			continue
		}

		// Skip argument indexes, flags, width and precision. The next byte is
		// the verb (fmt accepts arbitrary runes and reports unknown ones at
		// runtime), so replacing through it mirrors the rendered position.
		j := i + 1
		for j < len(format) && strings.ContainsRune("#0+- .123456789*[]", rune(format[j])) {
			j++
		}
		if j == len(format) {
			// A dangling percent is preserved: node will see the same broken
			// JavaScript that fmt would leave diagnostically corrupted.
			out.WriteString(format[i:])
			break
		}
		out.WriteString(placeholder)
		i = j + 1
	}
	return out.String()
}

func resolveGoValuePlaceholders(text string) string {
	for searchFrom := 0; ; {
		rel := strings.Index(text[searchFrom:], goValuePlaceholder)
		if rel < 0 {
			return text
		}
		start := searchFrom + rel
		end := start + len(goValuePlaceholder)
		if !insideJSString(activeScriptPrefix(text[:start])) && scalarNeededBefore(text, start) {
			text = text[:start] + placeholder + text[end:]
			searchFrom = start + len(placeholder)
			continue
		}
		searchFrom = end
	}
}

func activeScriptPrefix(text string) string {
	opens := openRe.FindAllStringIndex(text, -1)
	if len(opens) == 0 {
		return ""
	}
	lastOpen := opens[len(opens)-1]
	closes := closeRe.FindAllStringIndex(text, -1)
	if len(closes) > 0 && closes[len(closes)-1][0] > lastOpen[0] {
		return ""
	}
	return text[lastOpen[1]:]
}

func scalarNeededBefore(text string, pos int) bool {
	for pos > 0 {
		pos--
		if text[pos] == ' ' || text[pos] == '\t' || text[pos] == '\r' || text[pos] == '\n' {
			continue
		}
		return strings.ContainsRune("=(:,[!~?+-*/%&|^<>", rune(text[pos]))
	}
	return true
}

// insideJSString is intentionally a small lexer rather than a quote count:
// URLs and CSS snippets contain escaped quotes and comment text. A Go value
// concatenated inside a JavaScript string is safe as comment-shaped text;
// outside a string it may need the scalar fallback above.
func insideJSString(text string) bool {
	const (
		jsNormal = iota
		jsSingleQuote
		jsDoubleQuote
		jsTemplateQuote
		jsLineComment
		jsBlockComment
	)
	state := jsNormal
	escaped := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch state {
		case jsSingleQuote, jsDoubleQuote, jsTemplateQuote:
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if (state == jsSingleQuote && c == '\'') ||
				(state == jsDoubleQuote && c == '"') ||
				(state == jsTemplateQuote && c == '`') {
				state = jsNormal
			}
		case jsLineComment:
			if c == '\n' || c == '\r' {
				state = jsNormal
			}
		case jsBlockComment:
			if c == '*' && i+1 < len(text) && text[i+1] == '/' {
				state = jsNormal
				i++
			}
		default:
			switch c {
			case '\'':
				state = jsSingleQuote
			case '"':
				state = jsDoubleQuote
			case '`':
				state = jsTemplateQuote
			case '/':
				if i+1 < len(text) && text[i+1] == '/' {
					state = jsLineComment
					i++
				} else if i+1 < len(text) && text[i+1] == '*' {
					state = jsBlockComment
					i++
				}
			}
		}
	}
	return state == jsSingleQuote || state == jsDoubleQuote || state == jsTemplateQuote
}

// stripCommentsOutsideScripts removes markup/template comments that can
// contain an explanatory "<script>" without touching comment-looking text in
// a real script body. Applying a broad <!--.*?--> regex to the entire literal
// would erase JavaScript between two string literals containing those markers.
func stripCommentsOutsideScripts(text string) string {
	var out strings.Builder
	for pos := 0; pos < len(text); {
		scriptAt := -1
		if loc := openRe.FindStringIndex(text[pos:]); loc != nil {
			scriptAt = pos + loc[0]
		}
		htmlCommentAt := strings.Index(text[pos:], "<!--")
		if htmlCommentAt >= 0 {
			htmlCommentAt += pos
		}
		templateCommentAt := strings.Index(text[pos:], "{{/*")
		if templateCommentAt >= 0 {
			templateCommentAt += pos
		}

		commentAt, commentEnd := -1, ""
		if htmlCommentAt >= 0 {
			commentAt, commentEnd = htmlCommentAt, "-->"
		}
		if templateCommentAt >= 0 && (commentAt < 0 || templateCommentAt < commentAt) {
			commentAt, commentEnd = templateCommentAt, "*/}}"
		}
		if scriptAt >= 0 && (commentAt < 0 || scriptAt < commentAt) {
			openLoc := openRe.FindStringIndex(text[scriptAt:])
			openEnd := scriptAt + openLoc[1]
			closeLoc := closeRe.FindStringIndex(text[openEnd:])
			if closeLoc == nil {
				out.WriteString(text[pos:])
				break
			}
			closeEnd := openEnd + closeLoc[1]
			out.WriteString(text[pos:closeEnd])
			pos = closeEnd
			continue
		}
		if commentAt >= 0 {
			out.WriteString(text[pos:commentAt])
			relEnd := strings.Index(text[commentAt+len(commentEnd):], commentEnd)
			if relEnd < 0 {
				writeBlankPreservingNewlines(&out, text[commentAt:])
				break
			}
			end := commentAt + len(commentEnd) + relEnd + len(commentEnd)
			writeBlankPreservingNewlines(&out, text[commentAt:end])
			pos = end
			continue
		}
		out.WriteString(text[pos:])
		break
	}
	return out.String()
}

func writeBlankPreservingNewlines(out *strings.Builder, text string) {
	for _, r := range text {
		if r == '\n' || r == '\r' {
			out.WriteRune(r)
		} else {
			out.WriteByte(' ')
		}
	}
}

// extract возвращает тела inline-скриптов литерала, пригодные для node --check.
func extract(text string) ([]extractedScript, error) {
	var out []extractedScript
	for _, m := range scriptRe.FindAllStringSubmatch(text, -1) {
		attrs, body := m[1], m[2]
		attributes := parseScriptAttributes(attrs)
		if _, external := attributes["src"]; external {
			continue
		}
		// type="application/json" и любой другой не-JS тип — это данные,
		// которые браузер не исполняет.
		scriptType := strings.ToLower(strings.TrimSpace(attributes["type"]))
		if !isJavaScriptType(scriptType) {
			continue
		}
		variants, err := expandTemplateActions(body)
		if err != nil {
			return nil, err
		}
		for _, variant := range variants {
			if strings.TrimSpace(variant) == "" {
				continue
			}
			out = append(out, extractedScript{body: variant, module: scriptType == "module"})
		}
	}
	return out, nil
}

type templateToken struct {
	start int
	end   int
	kind  string
}

type templatePart struct {
	text     string
	branches [][]templatePart
}

// expandTemplateActions builds one JavaScript source for every possible
// template branch. Replacing a complete {{if}}...{{end}} block with a scalar
// would hide syntax errors inside that block. Expanding the branches also
// handles conditionals used in expression position without inventing invalid
// JavaScript around them.
func expandTemplateActions(body string) ([]string, error) {
	indices := tmplActionRe.FindAllStringIndex(body, -1)
	tokens := make([]templateToken, 0, len(indices))
	for _, index := range indices {
		tokens = append(tokens, templateToken{
			start: index[0],
			end:   index[1],
			kind:  templateActionKind(body[index[0]:index[1]]),
		})
	}

	tokenIndex, sourcePos := 0, 0
	parts, stop, err := parseTemplateSequence(body, tokens, &tokenIndex, &sourcePos)
	if err != nil {
		return nil, err
	}
	if stop != "" {
		return nil, fmt.Errorf("неожиданное шаблонное действие %q", stop)
	}
	return expandTemplateParts(parts)
}

// parseTemplateSequence parses until EOF or a consumed {{else}}/{{end}}. A
// control block becomes a part with alternative branches; ordinary template
// actions become a scalar placeholder.
func parseTemplateSequence(body string, tokens []templateToken, tokenIndex, sourcePos *int) ([]templatePart, string, error) {
	var parts []templatePart
	for *tokenIndex < len(tokens) {
		token := tokens[*tokenIndex]
		if token.start > *sourcePos {
			parts = append(parts, templatePart{text: body[*sourcePos:token.start]})
		}
		*tokenIndex++
		*sourcePos = token.end

		switch token.kind {
		case "if", "with", "range":
			first, stop, err := parseTemplateSequence(body, tokens, tokenIndex, sourcePos)
			if err != nil {
				return nil, "", err
			}
			branches := [][]templatePart{first}
			hasUnconditionalElse := false
			for stop == "else" || stop == "else-if" {
				if stop == "else" {
					hasUnconditionalElse = true
				}
				branch, nextStop, err := parseTemplateSequence(body, tokens, tokenIndex, sourcePos)
				if err != nil {
					return nil, "", err
				}
				branches = append(branches, branch)
				stop = nextStop
			}
			if stop != "end" {
				return nil, "", fmt.Errorf("шаблонный блок {{%s}} не закрыт {{end}}", token.kind)
			}
			// An if/with/range without a final plain else can render nothing.
			if !hasUnconditionalElse {
				branches = append(branches, nil)
			}
			parts = append(parts, templatePart{branches: branches})
		case "else", "else-if", "end":
			return parts, token.kind, nil
		case "comment":
			// Template comments render no bytes.
		default:
			parts = append(parts, templatePart{text: placeholder})
		}
	}
	if *sourcePos < len(body) {
		parts = append(parts, templatePart{text: body[*sourcePos:]})
		*sourcePos = len(body)
	}
	return parts, "", nil
}

func expandTemplateParts(parts []templatePart) ([]string, error) {
	variants := []string{""}
	for _, part := range parts {
		if part.branches == nil {
			for i := range variants {
				variants[i] += part.text
			}
			continue
		}

		var choices []string
		for _, branch := range part.branches {
			expanded, err := expandTemplateParts(branch)
			if err != nil {
				return nil, err
			}
			choices = append(choices, expanded...)
		}
		choices = uniqueStrings(choices)
		if len(variants)*len(choices) > maxTemplateVariants {
			return nil, fmt.Errorf("шаблон порождает больше %d вариантов inline-JS", maxTemplateVariants)
		}
		combined := make([]string, 0, len(variants)*len(choices))
		for _, prefix := range variants {
			for _, choice := range choices {
				combined = append(combined, prefix+choice)
			}
		}
		variants = uniqueStrings(combined)
	}
	return variants, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func templateActionKind(action string) string {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(action, "{{"), "}}"))
	inner = strings.TrimSpace(strings.TrimPrefix(inner, "-"))
	inner = strings.TrimSpace(strings.TrimSuffix(inner, "-"))
	fields := strings.Fields(inner)
	if len(fields) == 0 {
		return "action"
	}
	if strings.HasPrefix(inner, "/*") {
		return "comment"
	}
	switch fields[0] {
	case "if", "with", "range", "end":
		return fields[0]
	case "else":
		if len(fields) > 1 {
			return "else-if"
		}
		return "else"
	default:
		return "action"
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func parseScriptAttributes(raw string) map[string]string {
	attributes := make(map[string]string)
	for i := 0; i < len(raw); {
		for i < len(raw) && (isHTMLSpace(raw[i]) || raw[i] == '/') {
			i++
		}
		nameStart := i
		for i < len(raw) && !isHTMLSpace(raw[i]) && raw[i] != '=' && raw[i] != '/' {
			i++
		}
		if nameStart == i {
			i++
			continue
		}
		name := strings.ToLower(raw[nameStart:i])
		for i < len(raw) && isHTMLSpace(raw[i]) {
			i++
		}
		value := ""
		if i < len(raw) && raw[i] == '=' {
			i++
			for i < len(raw) && isHTMLSpace(raw[i]) {
				i++
			}
			if i < len(raw) && (raw[i] == '\'' || raw[i] == '"') {
				quote := raw[i]
				i++
				valueStart := i
				for i < len(raw) && raw[i] != quote {
					i++
				}
				value = raw[valueStart:i]
				if i < len(raw) {
					i++
				}
			} else {
				valueStart := i
				for i < len(raw) && !isHTMLSpace(raw[i]) && raw[i] != '/' {
					i++
				}
				value = raw[valueStart:i]
			}
		}
		if _, duplicate := attributes[name]; !duplicate {
			attributes[name] = value
		}
	}
	return attributes
}

func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}

func isJavaScriptType(scriptType string) bool {
	if scriptType != "module" {
		if semicolon := strings.IndexByte(scriptType, ';'); semicolon >= 0 {
			scriptType = strings.TrimSpace(scriptType[:semicolon])
		}
	}
	switch scriptType {
	case "", "module", "text/javascript", "application/javascript", "text/ecmascript", "application/ecmascript":
		return true
	default:
		return false
	}
}
