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
	"unicode"
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
	decls := namedGroupSyntax.FindAllStringSubmatch(pattern, -1)
	if len(decls) == 0 {
		return pattern, nil, nil
	}
	declared := make([]string, 0, len(decls))
	needAlias := false
	for _, d := range decls {
		declared = append(declared, d[1])
		if !isASCIIGroupName(d[1]) {
			needAlias = true
		}
	}
	if !needAlias {
		return pattern, nil, nil
	}
	prefix := aliasPrefix(declared)
	aliases := map[string]string{}
	byName := map[string]string{}
	n := 0
	out := namedGroupSyntax.ReplaceAllStringFunc(pattern, func(decl string) string {
		name := namedGroupSyntax.FindStringSubmatch(decl)[1]
		if isASCIIGroupName(name) {
			return decl
		}
		n++
		alias := fmt.Sprintf("%s%d", prefix, n)
		aliases[alias] = name
		byName[name] = alias
		return strings.Replace(decl, "<"+name+">", "<"+alias+">", 1)
	})
	return out, aliases, byName
}

// aliasPrefix подбирает префикс псевдонима, с которым не столкнётся ни одно имя
// группы из самого шаблона.
//
// Раньше префикс был жёстко «ob», и шаблон «(?P<ob1>x)-(?P<год>y)» превращался
// в «(?P<ob1>x)-(?P<ob1>y)». RE2 дубликаты имён не отвергает, поэтому дальше всё
// молча кривое: обе группы попадают в ГруппыПоИмени под одним ключом, побеждает
// последняя (#997). Цикл конечен: каждый шаг удлиняет префикс, а имён в шаблоне
// конечное число и все они конечной длины.
func aliasPrefix(declared []string) string {
	prefix := "ob"
	for {
		clash := false
		for _, name := range declared {
			if strings.HasPrefix(name, prefix) {
				clash = true
				break
			}
		}
		if !clash {
			return prefix
		}
		prefix += "_"
	}
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

// expandReplacement переводит ссылки на группы с не-ASCII именами в ссылки на
// ASCII-псевдонимы, с которыми группа реально скомпилирована — обе формы,
// «${имя}» и «$имя».
//
// Форма без скобок раньше не переводилась: считалось, что кириллица после «$»
// не распознаётся в принципе. Это неверно — Regexp.Expand читает имя через
// unicode.IsLetter, то есть «$год» разбирается как ссылка на группу «год».
// Такой группы в скомпилированном шаблоне нет (она переименована в псевдоним),
// а ссылка на несуществующую группу по контракту Expand заменяется пустой
// строкой: Заменить(С, "$год-$месяц") молча возвращал пустоту (#997).
//
// Разбор повторяет Expand: имя читается до первого символа, который не буква,
// не цифра и не «_», а «$$» — экранированный доллар, имя за ним не читается.
func (r *dslRegex) expandReplacement(repl string) string {
	if len(r.byName) == 0 {
		return repl
	}
	var b strings.Builder
	for i := 0; i < len(repl); {
		if repl[i] != '$' {
			b.WriteByte(repl[i])
			i++
			continue
		}
		if i+1 < len(repl) && repl[i+1] == '$' {
			b.WriteString("$$")
			i += 2
			continue
		}
		name, brace, rest, ok := extractGroupRef(repl[i:])
		if !ok {
			b.WriteByte('$')
			i++
			continue
		}
		switch alias, aliased := r.byName[name]; {
		case aliased:
			b.WriteString("${" + alias + "}")
		case brace:
			b.WriteString("${" + name + "}")
		default:
			b.WriteString("$" + name)
		}
		i = len(repl) - len(rest)
	}
	return b.String()
}

// extractGroupRef читает ссылку на группу в начале строки — «$имя» или
// «${имя}» — теми же правилами, что и Regexp.Expand. Возвращает имя, была ли
// форма со скобками, остаток строки и признак того, что ссылка разобрана.
// Неразобранное (одинокий «$», незакрытая скобка) Expand считает обычным
// текстом — здесь такой кусок тоже остаётся нетронутым.
func extractGroupRef(s string) (name string, brace bool, rest string, ok bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false, s, false
	}
	body := s[1:]
	if body[0] == '{' {
		brace = true
		body = body[1:]
	}
	i := 0
	for i < len(body) {
		c, size := utf8.DecodeRuneInString(body[i:])
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' {
			break
		}
		i += size
	}
	if i == 0 {
		return "", brace, s, false
	}
	name = body[:i]
	if brace {
		if i >= len(body) || body[i] != '}' {
			return "", brace, s, false
		}
		return name, true, body[i+1:], true
	}
	return name, false, body[i:], true
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
