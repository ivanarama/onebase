package interpreter

// Регулярные выражения в DSL (план 124).
//
// Синтаксис — RE2 (стандартный пакет regexp): нет обратных ссылок и lookahead,
// зато гарантировано линейное время. Конфигурация не может положить сервер
// «катастрофическим бэктрекингом» на подобранной строке — для публичных
// HTTP-сервисов (сайт, приём заявок) это решающее свойство.
//
// Тип регистрируется в evalNew (interpreter.go) рядом со встроенными
// Массив/Структура/Соответствие, а НЕ фабрикой «__factory_» через окружение:
// фабрики инжектируются точечно (NewChartFunctions — только в ui/handlers_dsl.go,
// NewServiceFunctions — только в dslvars), и объект оказался бы доступен в форме,
// но отсутствовал в регламентном задании или procrun.

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// maxRegexPatternLen — предел длины шаблона в рунах. Защита от шаблона,
	// собранного в цикле конкатенацией.
	maxRegexPatternLen = 4096
	// maxRegexInputLen — предел размера обрабатываемой строки в байтах.
	maxRegexInputLen = 4 << 20 // 4 МиБ
	// maxRegexMatches — сколько совпадений отдаёт НайтиВсе без явного предела.
	// Превышение — ошибка, а не тихое усечение: усечённый результат выглядит как
	// корректный отчёт с пропавшими строками, и это никто не замечает.
	maxRegexMatches = 10000
)

func init() {
	builtins["регексэкранировать"] = regexEscapeBuiltin
	builtins["regexescape"] = regexEscapeBuiltin
}

// РегексЭкранировать(Текст) — экранирует спецсимволы, чтобы строку можно было
// вставить в шаблон как литерал. Глобальная функция, а не метод объекта: нужна
// ДО того, как объект создан (сборка шаблона из пользовательского ввода).
func regexEscapeBuiltin(args []any, _ string, _ int) (any, error) {
	return regexp.QuoteMeta(strArg(args, 0)), nil
}

// dslRegex — скомпилированное регулярное выражение. Неизменяемый объект:
// шаблон компилируется один раз в конструкторе.
type dslRegex struct {
	re      *regexp.Regexp
	pattern string
	// aliases — псевдоним ASCII → исходное имя группы. Заполняется только для
	// имён с не-ASCII символами (см. prepareNamedGroups).
	aliases map[string]string
	// byName — исходное имя группы → псевдоним; нужен строке замены.
	byName map[string]string
}

// NewRegexObject — конструктор «Новый Регекс(Шаблон[, Флаги])».
func NewRegexObject(args []any) any {
	pattern := strArg(args, 0)
	if utf8.RuneCountInString(pattern) > maxRegexPatternLen {
		RaiseUserError(fmt.Sprintf("Регекс: шаблон длиннее %d символов", maxRegexPatternLen))
	}
	prefix := regexFlagPrefix(strArg(args, 1))
	prepared, aliases, byName := prepareNamedGroups(pattern)
	re, err := regexp.Compile(prefix + prepared)
	if err != nil {
		RaiseUserError(fmt.Sprintf("Регекс: не удалось скомпилировать шаблон «%s»: %v", pattern, err))
	}
	return &dslRegex{re: re, pattern: pattern, aliases: aliases, byName: byName}
}

// namedGroupSyntax находит объявления именованных групп: «(?P<имя>» и «(?<имя>».
var namedGroupSyntax = regexp.MustCompile(`\(\?P?<([^>]*)>`)

// prepareNamedGroups подменяет не-ASCII имена групп ASCII-псевдонимами.
//
// RE2 разрешает в имени группы только [A-Za-z0-9_], поэтому «(?P<год>\d{4})»
// не компилируется вовсе. В платформе, где вся прикладная лексика русская,
// конфигуратор напишет имя по-русски инстинктивно — и получит ошибку там, где
// ошибки нет. Поэтому имя заменяется на «obN», а наружу (ГруппыПоИмени, ${имя}
// в строке замены) возвращается исходное.
func prepareNamedGroups(pattern string) (string, map[string]string, map[string]string) {
	if !namedGroupSyntax.MatchString(pattern) {
		return pattern, nil, nil
	}
	aliases := map[string]string{}
	byName := map[string]string{}
	n := 0
	out := namedGroupSyntax.ReplaceAllStringFunc(pattern, func(decl string) string {
		name := namedGroupSyntax.FindStringSubmatch(decl)[1]
		if isASCIIGroupName(name) {
			return decl
		}
		n++
		alias := fmt.Sprintf("ob%d", n)
		aliases[alias] = name
		byName[name] = alias
		return strings.Replace(decl, "<"+name+">", "<"+alias+">", 1)
	})
	return out, aliases, byName
}

func isASCIIGroupName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// groupName возвращает исходное имя группы по имени, с которым она
// скомпилирована.
func (r *dslRegex) groupName(compiled string) string {
	if orig, ok := r.aliases[compiled]; ok {
		return orig
	}
	return compiled
}

// expandReplacement переводит ссылки «${имя}» на русские группы в ссылки на
// ASCII-псевдонимы, с которыми группа реально скомпилирована. Формы «$имя» без
// скобок нет: в Go имя после «$» читается до первого не-word символа, то есть
// кириллица там не распознаётся в принципе.
func (r *dslRegex) expandReplacement(repl string) string {
	if len(r.byName) == 0 {
		return repl
	}
	for name, alias := range r.byName {
		repl = strings.ReplaceAll(repl, "${"+name+"}", "${"+alias+"}")
	}
	return repl
}

// regexFlagPrefix переводит буквы флагов в префикс группы RE2. Пустая строка
// флагов даёт пустой префикс — шаблон компилируется как есть.
func regexFlagPrefix(flags string) string {
	flags = strings.TrimSpace(flags)
	if flags == "" {
		return ""
	}
	var b strings.Builder
	seen := map[rune]bool{}
	for _, f := range strings.ToLower(flags) {
		switch f {
		case 'i', 'm', 's':
			if !seen[f] {
				seen[f] = true
				b.WriteRune(f)
			}
		default:
			RaiseUserError(fmt.Sprintf("Регекс: неизвестный флаг «%c» (допустимы i, m, s)", f))
		}
	}
	return "(?" + b.String() + ")"
}

func (r *dslRegex) String() string   { return "Регекс[" + r.pattern + "]" }
func (r *dslRegex) TypeName() string { return "Регекс" }

func (r *dslRegex) Get(field string) any {
	switch strings.ToLower(field) {
	case "шаблон", "pattern":
		return r.pattern
	}
	return nil
}

// Set — no-op: шаблон после компиляции не меняется.
func (r *dslRegex) Set(string, any) {}

func (r *dslRegex) CallMethod(name string, args []any) any {
	switch strings.ToLower(name) {
	case "совпадает", "matches":
		return r.re.MatchString(r.input("Совпадает", args))

	case "найти", "find":
		s := r.input("Найти", args)
		loc := r.re.FindStringSubmatchIndex(s)
		if loc == nil {
			return nil // Неопределено
		}
		return r.matchStruct(s, loc)

	case "найтивсе", "findall":
		return r.findAll(args)

	case "заменить", "replace":
		s := r.input("Заменить", args)
		return r.re.ReplaceAllString(s, r.expandReplacement(strArg(args, 1)))

	case "разделить", "split":
		s := r.input("Разделить", args)
		// Предел «сколько частей вернуть»; без него (или при нуле/отрицательном)
		// делим полностью — Split(-1).
		limit := -1
		if len(args) > 1 {
			if n := int(floatArg(args, 1)); n > 0 {
				limit = n
			}
		}
		parts := r.re.Split(s, limit)
		items := make([]any, 0, len(parts))
		for _, p := range parts {
			items = append(items, p)
		}
		return NewArray(items)
	}
	// Молчаливое Неопределено спрятало бы опечатку в имени метода — падаем,
	// как остальные объекты интерпретатора.
	RaiseUserError("Регекс: неизвестный метод «" + name + "»")
	return nil
}

// input достаёт обрабатываемую строку и проверяет её размер. Имя метода нужно
// сообщению об ошибке: без него «строка слишком длинная» не подсказывает место.
func (r *dslRegex) input(method string, args []any) string {
	s := strArg(args, 0)
	if len(s) > maxRegexInputLen {
		RaiseUserError(fmt.Sprintf("Регекс.%s: строка длиннее %d байт", method, maxRegexInputLen))
	}
	return s
}

func (r *dslRegex) findAll(args []any) any {
	s := r.input("НайтиВсе", args)
	limit := 0
	if len(args) > 1 {
		limit = int(floatArg(args, 1))
	}
	// Явный предел — это «верни не более N», штатный режим. Без предела ищем на
	// один больше защитного максимума, чтобы отличить «ровно максимум» от
	// «слишком много» и упасть с внятной ошибкой вместо тихого усечения.
	n := limit
	if limit <= 0 {
		n = maxRegexMatches + 1
	}
	locs := r.re.FindAllStringSubmatchIndex(s, n)
	if limit <= 0 && len(locs) > maxRegexMatches {
		RaiseUserError(fmt.Sprintf(
			"Регекс.НайтиВсе: больше %d совпадений; сузьте шаблон или передайте предел вторым аргументом", maxRegexMatches))
	}
	items := make([]any, 0, len(locs))
	// Позиция считается бегущим счётчиком рун: совпадения идут по возрастанию
	// смещения, и пересчёт от начала строки на каждое (RuneCountInString(s[:i]))
	// давал бы квадратичное время на больших входах.
	runes, fromByte := 0, 0
	for _, loc := range locs {
		runes += utf8.RuneCountInString(s[fromByte:loc[0]])
		fromByte = loc[0]
		items = append(items, r.matchStructAt(s, loc, runes))
	}
	return NewArray(items)
}

// matchStruct собирает Структуру одного совпадения. Позиция — 1-based и в
// РУНАХ, как у СтрНайти (builtins.go: len([]rune(s[:idx]))+1); байтовое
// смещение Go на кириллице дало бы значение, не совпадающее с остальным DSL.
func (r *dslRegex) matchStruct(s string, loc []int) *Struct {
	return r.matchStructAt(s, loc, utf8.RuneCountInString(s[:loc[0]]))
}

// matchStructAt — то же с уже посчитанной руновой позицией начала совпадения.
func (r *dslRegex) matchStructAt(s string, loc []int, startRunes int) *Struct {
	res := NewStructFromMap(nil)
	value := s[loc[0]:loc[1]]
	res.Set("Значение", value)
	res.Set("Позиция", float64(startRunes+1))
	res.Set("Длина", float64(utf8.RuneCountInString(value)))

	groups := make([]any, 0, len(loc)/2)
	for i := 0; i*2 < len(loc); i++ {
		groups = append(groups, groupText(s, loc, i))
	}
	res.Set("Группы", NewArray(groups))

	named := &Map{}
	for i, gname := range r.re.SubexpNames() {
		if gname == "" {
			continue
		}
		named.CallMethod("вставить", []any{r.groupName(gname), groupText(s, loc, i)})
	}
	res.Set("ГруппыПоИмени", named)
	return res
}

// groupText возвращает текст группы. Не участвовавшая в совпадении группа даёт
// пустую строку, а не Неопределено: конкатенация с ней не должна падать.
func groupText(s string, loc []int, i int) string {
	start, end := loc[2*i], loc[2*i+1]
	if start < 0 || end < 0 {
		return ""
	}
	return s[start:end]
}
