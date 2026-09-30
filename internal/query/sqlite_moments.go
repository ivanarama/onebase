package query

import (
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Моменты на SQLite. У SQLite нет типа «момент времени»: дата лежит текстом в
// UTC, и сравнение идёт лексикографически. Текст при этом двух видов:
//
//   - реквизиты справочников и документов пишутся RFC3339 —
//     «2026-10-09T21:00:00Z» (storage.fieldValueDialect);
//   - период и реквизиты регистров накопления, сведений и бухгалтерии пишутся
//     привязанным time.Time — «2026-10-09 21:00:00+00:00»
//     (storage.sqliteTimeLayout).
//
// Календарные функции запроса (НачалоДня, КонецДня, …) с #1243 считают
// местные стенные часы и отдают их без зоны — «2026-10-10». Сравнение такой
// границы с колонкой сравнивало местную полночь с UTC-текстом, и на
// PostgreSQL и SQLite один отчёт давал разные строки: в Москве отбор «за 10
// октября» терял первые три часа дня и захватывал первые три часа следующего,
// а движение в 00:30 уже считалось «раньше 10 октября». Поэтому граница,
// которую сравнивают с моментом, переводится обратно в UTC — в формат той
// колонки, с которой её сравнивают (формат задаёт класс источника колонки:
// sourceClassEntity — RFC3339, sourceClassRegister — sqliteTimeLayout).
// Переводится сторона границы, а не колонка: индекс по колонке продолжает
// работать.

// sourceClassOf — класс источника: регистры (накопления, сведений,
// бухгалтерии) хранят моменты привязанным time.Time, остальные — RFC3339.
func sourceClassOf(typeUpper string) sourceClass {
	if isAccumRegType(typeUpper) || isInfoRegType(typeUpper) || isAccountRegType(typeUpper) {
		return sourceClassRegister
	}
	return sourceClassEntity
}

// firstSourceClass — класс того источника, чьи поля описывает buildColTypes
// (первый источник потока). Для виртуальной таблицы buildColTypes пуст, и
// класс не нужен.
func firstSourceClass(tokens []tok) sourceClass {
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].kind != tIdent {
			continue
		}
		upper := upperFast(tokens[i].val)
		if !isSourceType(upper) || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		if i+3 < len(tokens) && tokens[i+3].kind == tDot {
			return sourceClassUnknown
		}
		return sourceClassOf(upper)
	}
	return sourceClassUnknown
}

// isMomentCalendarFunc — календарные функции, чей результат сам момент
// (граница дня, месяца, года), а не число вроде Год/Месяц/День.
func isMomentCalendarFunc(name string) bool {
	switch name {
	case "началодня", "startofday", "конецдня", "endofday",
		"началомесяца", "startofmonth", "началогода", "startofyear":
		return true
	default:
		return false
	}
}

// momentBoundaries знает, в каком формате хранится колонка-момент в каждой
// области SELECT, и где стоят аргументы виртуальных таблиц. Области и
// квалификаторы те же, что у buildScopedColTypes/buildQualifiedColTypes.
//
// Период виртуальной таблицы моментом не считается: с периодичностью это
// усечённая метка («2026-10-10», «2026-10»), и перевод границы в UTC сломал бы
// сравнение, которое с ней совпадало.
type momentBoundaries struct {
	// columns — колонка-дата без квалификатора → класс источника, когда все
	// источники области с такой колонкой одного класса; иначе Unknown.
	columns map[int]map[string]sourceClass
	// qualifiers — имя, таблица или алиас источника → его колонки-даты и их
	// класс.
	qualifiers map[int]map[string]map[string]sourceClass
	// vtArgs — токены непосредственно в списке аргументов виртуальной
	// таблицы регистра: граница периода там сравнивается с period. Позиции —
	// в том потоке, который переписывает rewriteScalarFuncs.
	vtArgs map[int]bool
}

// buildMomentBoundaries строится по тем же токенам и областям, что
// buildScopedColTypes; vtArgs заполняет вызывающий по потоку, который
// переписывается.
func buildMomentBoundaries(tokens []tok, opts CompileOpts, sourceCtx sourceContext) *momentBoundaries {
	m := &momentBoundaries{
		columns:    map[int]map[string]sourceClass{},
		qualifiers: map[int]map[string]map[string]sourceClass{},
	}
	put := func(dst map[string]sourceClass, name string, kind sourceClass) {
		if current, ok := dst[name]; ok {
			if current != kind {
				dst[name] = sourceClassUnknown // неоднозначно — не угадываем
			}
			return
		}
		dst[name] = kind
	}
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].kind != tIdent || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		typeUpper := upperFast(tokens[i].val)
		if !isSourceType(typeUpper) {
			continue
		}
		scopeID, ok := sourceCtx.scopeIDAt(i)
		if !ok {
			continue
		}
		name := tokens[i+2].val
		kind := sourceClassOf(typeUpper)
		virtual := i+3 < len(tokens) && tokens[i+3].kind == tDot
		var fields []string
		for field, typ := range sourceColTypes(typeUpper, name, opts) {
			if typ != metadata.FieldTypeDate || virtual && (field == "period" || field == "период") {
				continue
			}
			fields = append(fields, field)
		}
		if m.columns[scopeID] == nil {
			m.columns[scopeID] = map[string]sourceClass{}
			m.qualifiers[scopeID] = map[string]map[string]sourceClass{}
		}
		for _, field := range fields {
			put(m.columns[scopeID], field, kind)
		}
		for _, q := range sourceQualifierNames(tokens, i, typeUpper, name) {
			q = lowerFast(q)
			if q == "" {
				continue
			}
			if m.qualifiers[scopeID][q] == nil {
				m.qualifiers[scopeID][q] = map[string]sourceClass{}
			}
			for _, field := range fields {
				put(m.qualifiers[scopeID][q], field, kind)
			}
		}
	}
	return m
}

// sourceQualifierNames — имена, которыми можно квалифицировать поле источника,
// начинающегося с tokens[i] (Тип . Имя): имя, таблица и алиас, если он есть.
// У виртуальной таблицы алиас стоит после списка аргументов:
// Регистр.X.Остатки(...) КАК Р — та же логика, что в разборе областей FROM.
func sourceQualifierNames(tokens []tok, i int, typeUpper, name string) []string {
	names := []string{name, sourceToTable(typeUpper, name)}
	aliasAt := func(pos int) {
		if pos+1 < len(tokens) && tokens[pos].kind == tIdent {
			upper := upperFast(tokens[pos].val)
			if (upper == "КАК" || upper == "AS") && tokens[pos+1].kind == tIdent {
				names = append(names, tokens[pos+1].val)
			}
		}
	}
	if i+5 < len(tokens) && tokens[i+3].kind == tDot && tokens[i+5].kind == tLParen {
		if closing := matchingCloseParen(tokens, i+5); closing >= 0 {
			aliasAt(closing + 1)
		}
		return names
	}
	aliasAt(i + 3)
	return names
}

// vtArgumentPositions отмечает токены, лежащие непосредственно (на первом
// уровне скобок) в списке аргументов виртуальной таблицы регистра.
func vtArgumentPositions(tokens []tok) map[int]bool {
	pos := map[int]bool{}
	for i := 0; i+5 < len(tokens); i++ {
		if tokens[i].kind != tIdent || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent ||
			tokens[i+3].kind != tDot || tokens[i+4].kind != tIdent || tokens[i+5].kind != tLParen {
			continue
		}
		typeUpper := upperFast(tokens[i].val)
		vt := upperFast(tokens[i+4].val)
		_, accumVT := accumVTKinds[vt]
		_, infoVT := infoVTKinds[vt]
		if !(accumVT && (isAccumRegType(typeUpper) || isAccountRegType(typeUpper)) || infoVT && isInfoRegType(typeUpper)) {
			continue
		}
		closing := matchingCloseParen(tokens, i+5)
		if closing < 0 {
			continue
		}
		depth := 0
		for j := i + 6; j < closing; j++ {
			if depth == 0 {
				pos[j] = true
			}
			switch tokens[j].kind {
			case tLParen:
				depth++
			case tRParen:
				depth--
			}
		}
	}
	return pos
}

// boundaryClass — класс партнёра, в чей формат надо перевести результат
// календарной функции tokens[i](…)tokens[end], чтобы сравнение шло по одному
// тексту. Unknown — партнёр не колонка-момент и не параметр-дата (например,
// другая календарная функция): тогда результат остаётся местным, как раньше.
func (m *momentBoundaries) boundaryClass(tokens []tok, i, end, tokenOffset int, scopeID int, hasScope bool, params map[string]any) sourceClass {
	if m == nil {
		return sourceClassUnknown
	}
	// Целый аргумент виртуальной таблицы — граница периода регистра.
	if m.vtArgs[tokenOffset+i] && i > 0 && (tokens[i-1].kind == tLParen || tokens[i-1].kind == tComma) &&
		end+1 < len(tokens) && (tokens[end+1].kind == tComma || tokens[end+1].kind == tRParen) {
		return sourceClassRegister
	}
	if !hasScope {
		return sourceClassUnknown
	}
	operand := momentOperand{m: m, tokens: tokens, scopeID: scopeID, params: params}
	if i >= 2 && isComparisonTok(tokens[i-1]) {
		return operand.before(i - 2)
	}
	if i >= 2 && isKeywordTok(tokens[i-1], "МЕЖДУ", "BETWEEN") {
		return operand.before(i - 2)
	}
	if i >= 2 && isKeywordTok(tokens[i-1], "И", "AND") {
		// Верхняя граница МЕЖДУ: X МЕЖДУ <нижняя> И F.
		if between := betweenKeywordBefore(tokens, i-2); between >= 1 {
			return operand.before(between - 1)
		}
	}
	if end+2 < len(tokens) && isComparisonTok(tokens[end+1]) {
		return operand.after(end + 2)
	}
	return sourceClassUnknown
}

// momentOperand распознаёт партнёра сравнения: колонку-момент (с
// квалификатором или без), МАКСИМУМ/МИНИМУМ от неё или параметр-дату.
type momentOperand struct {
	m       *momentBoundaries
	tokens  []tok
	scopeID int
	params  map[string]any
}

// before — операнд, который заканчивается на tokens[k].
func (o momentOperand) before(k int) sourceClass {
	if k < 0 || k >= len(o.tokens) {
		return sourceClassUnknown
	}
	switch o.tokens[k].kind {
	case tParam:
		return o.param(k)
	case tIdent:
		if k >= 1 && o.tokens[k-1].kind == tDot {
			if k >= 2 && o.tokens[k-2].kind == tIdent && (k < 3 || o.tokens[k-3].kind != tDot) {
				return o.qualifiedColumn(o.tokens[k-2].val, o.tokens[k].val)
			}
			return sourceClassUnknown // Т.Поле.Реквизит или выражение.Поле — не колонка источника
		}
		return o.column(o.tokens[k].val)
	case tRParen:
		open := matchingOpenParen(o.tokens, k)
		if open >= 1 && isMinMaxAggregate(o.tokens[open-1]) {
			return o.columnExpr(o.tokens[open+1 : k])
		}
	}
	return sourceClassUnknown
}

// after — операнд, который начинается с tokens[k].
func (o momentOperand) after(k int) sourceClass {
	if k < 0 || k >= len(o.tokens) {
		return sourceClassUnknown
	}
	switch o.tokens[k].kind {
	case tParam:
		return o.param(k)
	case tIdent:
		if k+1 < len(o.tokens) && o.tokens[k+1].kind == tLParen {
			if !isMinMaxAggregate(o.tokens[k]) {
				return sourceClassUnknown
			}
			if closing := matchingCloseParen(o.tokens, k+1); closing > k+1 {
				return o.columnExpr(o.tokens[k+2 : closing])
			}
			return sourceClassUnknown
		}
		if k+2 < len(o.tokens) && o.tokens[k+1].kind == tDot && o.tokens[k+2].kind == tIdent {
			if k+3 < len(o.tokens) && (o.tokens[k+3].kind == tDot || o.tokens[k+3].kind == tLParen) {
				return sourceClassUnknown // Т.Поле.Реквизит или вызов — не колонка-момент
			}
			return o.qualifiedColumn(o.tokens[k].val, o.tokens[k+2].val)
		}
		return o.column(o.tokens[k].val)
	}
	return sourceClassUnknown
}

func (o momentOperand) columnExpr(expr []tok) sourceClass {
	switch {
	case len(expr) == 1 && expr[0].kind == tIdent:
		return o.column(expr[0].val)
	case len(expr) == 3 && expr[0].kind == tIdent && expr[1].kind == tDot && expr[2].kind == tIdent:
		return o.qualifiedColumn(expr[0].val, expr[2].val)
	}
	return sourceClassUnknown
}

func (o momentOperand) column(name string) sourceClass {
	return o.m.columns[o.scopeID][lowerFast(name)]
}

func (o momentOperand) qualifiedColumn(qualifier, name string) sourceClass {
	return o.m.qualifiers[o.scopeID][lowerFast(qualifier)][lowerFast(name)]
}

// param — параметр-дата вне прямого сравнения с полем привязывается
// storage.normalizeSQLiteArgs в формате регистров (sqliteTimeLayout).
func (o momentOperand) param(k int) sourceClass {
	switch o.params[o.tokens[k].val].(type) {
	case time.Time, *time.Time:
		return sourceClassRegister
	}
	return sourceClassUnknown
}

func isComparisonTok(t tok) bool {
	if t.kind != tOp {
		return false
	}
	switch t.val {
	case "=", "<>", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

func isKeywordTok(t tok, names ...string) bool {
	if t.kind != tIdent {
		return false
	}
	upper := upperFast(t.val)
	for _, name := range names {
		if upper == name {
			return true
		}
	}
	return false
}

func isMinMaxAggregate(t tok) bool {
	return isKeywordTok(t, "МАКСИМУМ", "МИНИМУМ", "MAX", "MIN")
}

// betweenKeywordBefore ищет МЕЖДУ, к которому относится И перед верхней
// границей: идёт назад от tokens[k] по нижней границе, не выходя из её скобок
// и не переходя через другое И/ИЛИ или сравнение. -1 — это не МЕЖДУ.
func betweenKeywordBefore(tokens []tok, k int) int {
	depth := 0
	for j := k; j >= 0; j-- {
		switch tokens[j].kind {
		case tRParen:
			depth++
			continue
		case tLParen:
			if depth == 0 {
				return -1
			}
			depth--
			continue
		}
		if depth > 0 {
			continue
		}
		if isKeywordTok(tokens[j], "МЕЖДУ", "BETWEEN") {
			return j
		}
		if isKeywordTok(tokens[j], "И", "AND", "ИЛИ", "OR", "ГДЕ", "WHERE", "ИМЕЮЩИЕ", "HAVING", "КОГДА", "WHEN") || isComparisonTok(tokens[j]) {
			return -1
		}
	}
	return -1
}

func matchingCloseParen(tokens []tok, open int) int {
	depth := 0
	for j := open; j < len(tokens); j++ {
		switch tokens[j].kind {
		case tLParen:
			depth++
		case tRParen:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

func matchingOpenParen(tokens []tok, closing int) int {
	depth := 0
	for j := closing; j >= 0; j-- {
		switch tokens[j].kind {
		case tRParen:
			depth++
		case tLParen:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}
