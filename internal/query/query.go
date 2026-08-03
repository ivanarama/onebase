package query

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

// CompileOpts holds options for query compilation including register metadata
// needed to resolve virtual table references (Остатки, Обороты, СрезПоследних, …).
type CompileOpts struct {
	Params      map[string]any
	Registers   []*metadata.Register
	InfoRegs    []*metadata.InfoRegister
	AccountRegs []*metadata.AccountRegister
	Entities    []*metadata.Entity
	// RowFilters are SQL-side read predicates keyed by source object. The
	// compiler qualifies each predicate against the actual table alias.
	RowFilters map[SourceRef]*storage.Predicate
	// Dialect selects the SQL flavour. nil = PgDialect (default).
	Dialect storage.Dialect
}

func dialectOrDefault(d storage.Dialect) storage.Dialect {
	if d == nil {
		return storage.PgDialect{}
	}
	return d
}

// Result holds compiled PostgreSQL SQL and positional arguments.
type Result struct {
	SQL  string
	Args []any
	// Sources перечисляет объекты-источники запроса (для объектного RBAC, план 54).
	// Имя — исходное имя сущности/регистра; Kind — секция прав ("catalog"|
	// "document"|"register"|"inforeg"; "" — для неизвестных типов).
	Sources []SourceRef
	// ProjectionFields перечисляет логические поля, прочитанные выражениями
	// SELECT до применения SQL-алиасов. Поле используется security-gate отчётов,
	// AI и виджетов: сравнение с результирующими именами колонок позволяло
	// обойти masking через `Телефон КАК Контакт` или функцию над полем.
	// Значение "*" означает wildcard-проекцию.
	ProjectionFields []string
	// Projection — поэлементный разбор списка выборки (план 88E). Позволяет
	// маскировать защищённые поля в колонках результата вместо отказа во всём
	// запросе; при Projection.Simple == false действует прежний отказ по
	// ProjectionFields.
	Projection ProjectionPlan
}

// SourceRef — объект-источник запроса (для проверки прав доступа).
type SourceRef struct {
	Kind string
	Name string
}

// sourcePermKind переводит ключевое слово типа источника в секцию прав User.Has.
// Регистр бухгалтерии (РегистрБухгалтерии) проверяется по секции "register" — той
// же, что и регистры накопления; одноимённые регистр накопления и регбух делят
// одно право (на практике безопасно: обе — read-only регистры).
// Неизвестные типы → "" (проверяемой секции прав нет).
func sourcePermKind(typeUpper string) string {
	switch {
	case typeUpper == "СПРАВОЧНИК" || typeUpper == "CATALOG":
		return "catalog"
	case typeUpper == "ДОКУМЕНТ" || typeUpper == "DOCUMENT":
		return "document"
	case isAccumRegType(typeUpper):
		return "register"
	case isInfoRegType(typeUpper):
		return "inforeg"
	case isAccountRegType(typeUpper):
		return "register" // регбух проверяется как регистр (план 54, фикс зазора)
	}
	return ""
}

// Compile translates a 1C-style query to PostgreSQL SQL.
func Compile(src string, opts CompileOpts) (Result, error) {
	return translate(tokenize(src), opts)
}

// --- tokenizer ---

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tDot
	tComma
	tLParen
	tRParen
	tParam
	tStr
	tNum
	tOp
	tStar
)

type tok struct {
	kind tokKind
	val  string
}

func tokenize(src string) []tok {
	var out []tok
	runes := []rune(src)
	n := len(runes)
	i := 0
	for i < n {
		ch := runes[i]
		if unicode.IsSpace(ch) {
			i++
			continue
		}
		switch {
		case ch == '&':
			i++
			j := i
			for i < n && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				i++
			}
			out = append(out, tok{tParam, string(runes[j:i])})
		case ch == '"':
			i++
			j := i
			for i < n && runes[i] != '"' {
				i++
			}
			out = append(out, tok{tStr, string(runes[j:i])})
			if i < n {
				i++
			}
		case ch == '\'':
			i++
			j := i
			for i < n && runes[i] != '\'' {
				i++
			}
			out = append(out, tok{tStr, string(runes[j:i])})
			if i < n {
				i++
			}
		case ch == '.':
			out = append(out, tok{tDot, "."})
			i++
		case ch == ',':
			out = append(out, tok{tComma, ","})
			i++
		case ch == '(':
			out = append(out, tok{tLParen, "("})
			i++
		case ch == ')':
			out = append(out, tok{tRParen, ")"})
			i++
		case ch == '*':
			out = append(out, tok{tStar, "*"})
			i++
		case ch == '<':
			if i+1 < n && runes[i+1] == '>' {
				out = append(out, tok{tOp, "<>"})
				i += 2
			} else if i+1 < n && runes[i+1] == '=' {
				out = append(out, tok{tOp, "<="})
				i += 2
			} else {
				out = append(out, tok{tOp, "<"})
				i++
			}
		case ch == '>':
			if i+1 < n && runes[i+1] == '=' {
				out = append(out, tok{tOp, ">="})
				i += 2
			} else {
				out = append(out, tok{tOp, ">"})
				i++
			}
		case ch == '!' && i+1 < n && runes[i+1] == '=':
			out = append(out, tok{tOp, "<>"})
			i += 2
		case ch == '=' || ch == '+' || ch == '-' || ch == '/':
			out = append(out, tok{tOp, string(ch)})
			i++
		case unicode.IsLetter(ch) || ch == '_':
			j := i
			for i < n && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				i++
			}
			out = append(out, tok{tIdent, string(runes[j:i])})
		case unicode.IsDigit(ch):
			j := i
			for i < n && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
				i++
			}
			out = append(out, tok{tNum, string(runes[j:i])})
		default:
			i++
		}
	}
	out = append(out, tok{tEOF, ""})
	return out
}

// --- source type mapping ---

var sourcePrefix = map[string]string{
	"РЕГИСТРНАКОПЛЕНИЯ":    "рег_",
	"ACCUMULATIONREGISTER": "рег_",
	"РЕГИСТРСВЕДЕНИЙ":      "инфо_",
	"INFORMATIONREGISTER":  "инфо_",
	"РЕГИСТРБУХГАЛТЕРИИ":   "акк_",
	"ACCOUNTINGREGISTER":   "акк_",
	"СПРАВОЧНИК":           "",
	"CATALOG":              "",
	"ДОКУМЕНТ":             "",
	"DOCUMENT":             "",
}

func isSourceType(upper string) bool {
	_, ok := sourcePrefix[upper]
	return ok
}

func isAccumRegType(upper string) bool {
	return upper == "РЕГИСТРНАКОПЛЕНИЯ" || upper == "ACCUMULATIONREGISTER"
}

func isInfoRegType(upper string) bool {
	return upper == "РЕГИСТРСВЕДЕНИЙ" || upper == "INFORMATIONREGISTER"
}

func isAccountRegType(upper string) bool {
	return upper == "РЕГИСТРБУХГАЛТЕРИИ" || upper == "ACCOUNTINGREGISTER"
}

func sourceToTable(typeUpper, entityName string) string {
	return sourcePrefix[typeUpper] + strings.ToLower(entityName)
}

// --- virtual table kind maps ---

var accumVTKinds = map[string]string{
	"ОСТАТКИ":              "balances",
	"BALANCES":             "balances",
	"ОБОРОТЫ":              "turnovers",
	"TURNOVERS":            "turnovers",
	"ОСТАТКИИОБОРОТЫ":      "balances_turnovers",
	"BALANCESANDTURNOVERS": "balances_turnovers",
}

// periodicityLevels maps RU/EN periodicity keywords (for Обороты third argument)
// to an internal granularity key used by periodTruncSQL.
var periodicityLevels = map[string]string{
	"ДЕНЬ":    "day",
	"DAY":     "day",
	"НЕДЕЛЯ":  "week",
	"WEEK":    "week",
	"МЕСЯЦ":   "month",
	"MONTH":   "month",
	"КВАРТАЛ": "quarter",
	"QUARTER": "quarter",
	"ГОД":     "year",
	"YEAR":    "year",
	"ЗАПИСЬ":  "record",
	"RECORD":  "record",
}

// periodTruncSQL returns a SQL expression that truncates the period column
// to the given granularity, using dialect-appropriate functions.
func periodTruncSQL(level string, d storage.Dialect) string {
	switch d.Name() {
	case "sqlite":
		// p — нормализованное представление period для функций дат SQLite.
		// Берём первые 19 символов (`YYYY-MM-DD HH:MM:SS`), что отсекает хвост
		// таймзоны вида ` +0300 MSK`, который мог попасть в старые базы (см.
		// storage.sqliteTimeLayout): без этого strftime/date вернули бы NULL и
		// группировка по периоду молча схлопнулась бы. Для новых, уже ISO-данных
		// substr — это no-op.
		p := "substr(period,1,19)"
		switch level {
		case "day":
			return "date(" + p + ")"
		case "week":
			return "strftime('%Y-W%W', " + p + ")"
		case "month":
			return "strftime('%Y-%m', " + p + ")"
		case "quarter":
			return "(strftime('%Y', " + p + ") || '-Q' || CAST((CAST(strftime('%m', " + p + ") AS INTEGER)-1)/3+1 AS TEXT))"
		case "year":
			return "strftime('%Y', " + p + ")"
		case "record":
			return "period"
		}
	default: // postgres
		switch level {
		case "day":
			return "date_trunc('day', period)"
		case "week":
			return "date_trunc('week', period)"
		case "month":
			return "date_trunc('month', period)"
		case "quarter":
			return "date_trunc('quarter', period)"
		case "year":
			return "date_trunc('year', period)"
		case "record":
			return "period"
		}
	}
	return "period"
}

// detectPeriodicity checks whether tokens represent a single periodicity keyword.
// Returns the internal level key and true if so; ("", false) otherwise — in which
// case the caller should treat the tokens as a filter condition (backward compat).
func detectPeriodicity(tokens []tok) (string, bool) {
	if len(tokens) != 1 {
		return "", false
	}
	if tokens[0].kind != tIdent {
		return "", false
	}
	level, ok := periodicityLevels[strings.ToUpper(tokens[0].val)]
	return level, ok
}

var infoVTKinds = map[string]string{
	"СРЕЗПОСЛЕДНИХ": "last_slice",
	"LASTSLICE":     "last_slice",
	"СРЕЗПЕРВЫХ":    "first_slice",
	"FIRSTSLICE":    "first_slice",
}

// --- keyword mapping ---

var kwMap = map[string]string{
	// Russian structural keywords
	"ВЫБРАТЬ":       "SELECT",
	"РАЗЛИЧНЫЕ":     "DISTINCT",
	"ИЗ":            "FROM",
	"ГДЕ":           "WHERE",
	"СГРУППИРОВАТЬ": "GROUP",
	"УПОРЯДОЧИТЬ":   "ORDER",
	"ПО":            "ON", // standalone ПО without СГРУППИРОВАТЬ/УПОРЯДОЧИТЬ is always JOIN ON
	"ИМЕЯ":          "HAVING",
	"КАК":           "AS",
	"И":             "AND",
	"ИЛИ":           "OR",
	"НЕ":            "NOT",
	"ВЫБОР":         "CASE",
	"КОГДА":         "WHEN",
	"ТОГДА":         "THEN",
	"ИНАЧЕ":         "ELSE",
	"КОНЕЦ":         "END",
	"УБЫВ":          "DESC",
	"ВОЗР":          "ASC",
	"ЕСТЬ":          "IS",
	"ПУСТО":         "NULL",
	"В":             "IN",
	"МЕЖДУ":         "BETWEEN",
	"ОБЪЕДИНИТЬ":    "UNION",
	"ВСЕ":           "ALL",
	// JOIN keywords (Russian)
	"ВНУТРЕННЕЕ": "INNER",
	"ЛЕВОЕ":      "LEFT",
	"ПРАВОЕ":     "RIGHT",
	"ПОЛНОЕ":     "FULL",
	"СОЕДИНЕНИЕ": "JOIN",
	// English pass-through
	"SELECT":   "SELECT",
	"DISTINCT": "DISTINCT",
	"FROM":     "FROM",
	"WHERE":    "WHERE",
	"GROUP":    "GROUP",
	"ORDER":    "ORDER",
	"BY":       "BY",
	"ON":       "ON",
	"HAVING":   "HAVING",
	"AS":       "AS",
	"AND":      "AND",
	"OR":       "OR",
	"NOT":      "NOT",
	"CASE":     "CASE",
	"WHEN":     "WHEN",
	"THEN":     "THEN",
	"ELSE":     "ELSE",
	"END":      "END",
	"DESC":     "DESC",
	"ASC":      "ASC",
	"IS":       "IS",
	"NULL":     "NULL",
	"IN":       "IN",
	"BETWEEN":  "BETWEEN",
	"UNION":    "UNION",
	"ALL":      "ALL",
	// JOIN keywords (English pass-through)
	"INNER": "INNER",
	"LEFT":  "LEFT",
	"RIGHT": "RIGHT",
	"FULL":  "FULL",
	"OUTER": "OUTER",
	"JOIN":  "JOIN",
	"CROSS": "CROSS",
}

var aggFuncs = map[string]string{
	"СУММА":      "SUM",
	"КОЛИЧЕСТВО": "COUNT",
	"МИНИМУМ":    "MIN",
	"МАКСИМУМ":   "MAX",
	"СРЕДНЕЕ":    "AVG",
	"SUM":        "SUM",
	"COUNT":      "COUNT",
	"MIN":        "MIN",
	"MAX":        "MAX",
	"AVG":        "AVG",
}

func sqlKW(ident string) (string, bool) {
	kw, ok := kwMap[strings.ToUpper(ident)]
	return kw, ok
}

func sqlAgg(ident string) (string, bool) {
	kw, ok := aggFuncs[strings.ToUpper(ident)]
	return kw, ok
}

// --- translator ---

type querySection int

const (
	sectionSelect querySection = iota
	sectionFrom
	sectionWhere
	sectionGroupBy
	sectionOrderBy
	sectionHaving
	sectionOther
)

type refDimInfo struct {
	fieldName  string // lowercase dim name: "номенклатура"
	idCol      string // DB column: "номенклатура_id"
	joinAlias  string // SQL alias for auto-JOIN: "ref_номенклатура"
	joinTable  string // referenced catalog table: "номенклатура"
	isVT       bool   // true: VT outer query, JOIN ON uses fieldName instead of idCol
	refIsDoc   bool   // true: referenced entity is a document → display "номер", not "наименование"
	refEntity  string // original-case referenced entity name (для SourceRef.Name, RBAC план 54/#14)
	refSrcType string // тип-ключевое слово источника для sourcePermKind: "ДОКУМЕНТ"/"СПРАВОЧНИК"
}

func (rd refDimInfo) displayCol() string {
	if rd.refIsDoc {
		return rd.joinAlias + ".номер"
	}
	return rd.joinAlias + ".наименование"
}

type translator struct {
	tokens      []tok
	pos         int
	args        []any
	params      map[string]int // param name → 1-based index in args (0 = NULL sentinel)
	paramValues map[string]any
	opts        CompileOpts
	parts       []string
	prevWasDot  bool                          // true after emitting "." — used to resolve .Ссылка → .id
	colMap      map[string]string             // lowercase field name → actual column name (for reference dims)
	colTypes    map[string]metadata.FieldType // lowercase field name → type (для квалификации и CAST number)
	refDims     []refDimInfo                  // reference dimensions with auto-JOIN info
	mainTable   string                        // main FROM table/alias (set when source is emitted)
	mainEmitted bool                          // главная таблица FROM уже эмитирована (refDims авто-JOIN — только для неё)
	section     querySection                  // current clause context
	aliases     map[string]struct{}           // имена алиасов вывода (КАК ...) — их не квалифицируем и не CAST'им
	sources     []SourceRef                   // объекты-источники запроса (для RBAC, план 54)
	rowFilters  []pendingRowFilter            // RLS-фильтры обычных источников, внедряемые в WHERE
	rowsScoped  bool                          // true после внедрения rowFilters в outer WHERE
	rowApplied  []SourceRef                   // источники, к которым RLS-предикат реально внедрён (для финальной сверки)
	parenDepth  int                           // глубина незакрытых '(' в основном потоке (VT-аргументы считает parseVTArgs)
	sourceCtx   sourceContext                 // scoped-типы источников для системных колонок регистра
	unionDepths map[int]bool                  // глубины SELECT с UNION для compound ORDER BY
	unionOrders map[int]bool                  // ORDER BY относится ко всему UNION, алиасы таблиц там недоступны
}

type sourceClass uint8

const (
	sourceClassUnknown sourceClass = iota
	sourceClassEntity
	sourceClassRegister
)

type sourceContext struct {
	scopes       []sourceScope
	tokenScope   []int
	tokenSection []querySection
	tokenDepth   []int
}

type sourceScope struct {
	main           sourceClass
	mainTable      string
	sourceCount    int
	qualifiers     map[string]sourceClass
	derivedAliases map[string]int
	refAliases     map[string]struct{}
}

func (ctx sourceContext) scopeAt(tokenPos int) (sourceScope, bool) {
	scopeID, ok := ctx.scopeIDAt(tokenPos)
	if !ok {
		return sourceScope{}, false
	}
	return ctx.scopes[scopeID], true
}

func (ctx sourceContext) scopeIDAt(tokenPos int) (int, bool) {
	if tokenPos < 0 || tokenPos >= len(ctx.tokenScope) {
		return 0, false
	}
	scopeID := ctx.tokenScope[tokenPos]
	if scopeID < 0 || scopeID >= len(ctx.scopes) {
		return 0, false
	}
	return scopeID, true
}

func (ctx sourceContext) sectionAt(tokenPos int) querySection {
	if tokenPos < 0 || tokenPos >= len(ctx.tokenSection) {
		return sectionOther
	}
	return ctx.tokenSection[tokenPos]
}

func (ctx sourceContext) isReferenceAliasAt(tokenPos int, lower string) bool {
	section := ctx.sectionAt(tokenPos)
	if section != sectionGroupBy && section != sectionOrderBy && section != sectionHaving {
		return false
	}
	scope, ok := ctx.scopeAt(tokenPos)
	if !ok {
		return false
	}
	_, ok = scope.refAliases[lower]
	return ok
}

type pendingRowFilter struct {
	source SourceRef
	alias  string
	meta   *metadata.Entity
	filter *storage.Predicate
}

// addSource фиксирует объект-источник запроса для последующей проверки прав.
func (tr *translator) addSource(typeUpper, name string) {
	kind := sourcePermKind(typeUpper)
	for _, s := range tr.sources {
		if s.Kind == kind && s.Name == name {
			return // уже зафиксирован (напр. та же сущность и в FROM, и через ссылку)
		}
	}
	tr.sources = append(tr.sources, SourceRef{Kind: kind, Name: name})
}

// addRefSource регистрирует связанную сущность авто-JOIN ссылочного поля как
// источник запроса (#14): её наименование/номер попадает в результат, поэтому
// RBAC должен проверить право чтения на неё, а не только на главную таблицу.
func (tr *translator) addRefSource(rd refDimInfo) {
	if rd.refEntity == "" {
		return
	}
	tr.addSource(rd.refSrcType, rd.refEntity)
}

func (tr *translator) sourceRowFilter(kind, name string) *storage.Predicate {
	for src, pred := range tr.opts.RowFilters {
		if src.Kind == kind && strings.EqualFold(src.Name, name) {
			return pred
		}
	}
	return nil
}

// markRowApplied фиксирует, что RLS-предикат источника реально внедрён в SQL.
// Матчинг по kind+EqualFold(name) — как в sourceRowFilter, чтобы регистр имени
// источника (токен запроса vs metadata) не расходился со сверкой.
func (tr *translator) markRowApplied(kind, name string) {
	if tr.rowFilterApplied(kind, name) {
		return
	}
	tr.rowApplied = append(tr.rowApplied, SourceRef{Kind: kind, Name: name})
}

func (tr *translator) rowFilterApplied(kind, name string) bool {
	for _, s := range tr.rowApplied {
		if s.Kind == kind && strings.EqualFold(s.Name, name) {
			return true
		}
	}
	return false
}

// assertRowFiltersApplied — финальная защита: если у источника запроса есть
// активная строковая политика, но предикат не был внедрён ни одним из путей
// (главная таблица / скоуп-подзапрос джойна / условие авто-JOIN), запрос
// отклоняется. Так неучтённая форма запроса даёт fail-closed отказ, а не тихую
// утечку чужих строк в отчётах/AI, где object-gate ограниченный источник
// пропускает (право read у пользователя есть). См. план 79, этап 79E.
func (tr *translator) assertRowFiltersApplied() error {
	if len(tr.opts.RowFilters) == 0 {
		return nil
	}
	for _, src := range tr.sources {
		if tr.sourceRowFilter(src.Kind, src.Name) == nil {
			continue
		}
		if !tr.rowFilterApplied(src.Kind, src.Name) {
			return fmt.Errorf("строковая политика источника %s.%s не внедрена в запрос", src.Kind, src.Name)
		}
	}
	return nil
}

func (tr *translator) predicateEntityForSource(typeUpper, name string) *metadata.Entity {
	switch {
	case isAccumRegType(typeUpper):
		if reg := tr.findRegister(name); reg != nil {
			return storage.RegisterPredicateEntity(reg)
		}
	case isInfoRegType(typeUpper):
		if ir := tr.findInfoRegister(name); ir != nil {
			return storage.InfoRegisterPredicateEntity(ir)
		}
	case isAccountRegType(typeUpper):
		if ar := tr.findAccountRegister(name); ar != nil {
			return storage.AccountRegisterPredicateEntity(ar)
		}
	default:
		for _, e := range tr.opts.Entities {
			if strings.EqualFold(e.Name, name) {
				return e
			}
		}
	}
	return nil
}

func (tr *translator) addPendingRowFilter(typeUpper, name, alias string) error {
	kind := sourcePermKind(typeUpper)
	pred := tr.sourceRowFilter(kind, name)
	if pred == nil {
		return nil
	}
	// Отложенный фильтр главной таблицы внедряется в outer WHERE. Источники,
	// встреченные внутри скобок, должны идти через rowFilteredSourceSQL: внешний
	// WHERE ссылался бы на таблицу вне области видимости.
	if tr.parenDepth > 0 {
		return fmt.Errorf("строковая политика источника %s.%s: фильтрация вложенного источника не была скоупирована", kind, name)
	}
	meta := tr.predicateEntityForSource(typeUpper, name)
	if meta == nil {
		return fmt.Errorf("row filter source %s.%s metadata not found", kind, name)
	}
	tr.rowFilters = append(tr.rowFilters, pendingRowFilter{
		source: SourceRef{Kind: kind, Name: name},
		alias:  alias,
		meta:   meta,
		filter: pred,
	})
	tr.markRowApplied(kind, name)
	return nil
}

func (tr *translator) rowFilterCondition(kind, name string, meta *metadata.Entity, alias string) (string, error) {
	pred := tr.sourceRowFilter(kind, name)
	if pred == nil {
		return "", nil
	}
	if meta == nil {
		return "", fmt.Errorf("row filter source %s.%s metadata not found", kind, name)
	}
	sql, args, _, err := storage.PredicateSQLQualified(dialectOrDefault(tr.opts.Dialect), meta, pred, len(tr.args)+1, alias)
	if err != nil {
		return "", err
	}
	tr.args = append(tr.args, args...)
	tr.markRowApplied(kind, name)
	return sql, nil
}

func (tr *translator) rowFilteredSourceSQL(typeUpper, name, tableName, alias string) (string, bool, error) {
	kind := sourcePermKind(typeUpper)
	pred := tr.sourceRowFilter(kind, name)
	if pred == nil {
		return "", false, nil
	}
	meta := tr.predicateEntityForSource(typeUpper, name)
	if meta == nil {
		return "", false, fmt.Errorf("row filter source %s.%s metadata not found", kind, name)
	}
	sql, args, _, err := storage.PredicateSQLQualified(dialectOrDefault(tr.opts.Dialect), meta, pred, len(tr.args)+1, "")
	if err != nil {
		return "", false, fmt.Errorf("row filter %s.%s: %w", kind, name, err)
	}
	tr.args = append(tr.args, args...)
	tr.markRowApplied(kind, name)
	return fmt.Sprintf("(SELECT * FROM %s WHERE %s) AS %s", tableName, sql, alias), true, nil
}

func (tr *translator) pendingRowFilterConditions() ([]string, error) {
	conds := make([]string, 0, len(tr.rowFilters))
	for _, rf := range tr.rowFilters {
		sql, args, _, err := storage.PredicateSQLQualified(dialectOrDefault(tr.opts.Dialect), rf.meta, rf.filter, len(tr.args)+1, rf.alias)
		if err != nil {
			return nil, fmt.Errorf("row filter %s.%s: %w", rf.source.Kind, rf.source.Name, err)
		}
		if sql == "" {
			continue
		}
		tr.args = append(tr.args, args...)
		conds = append(conds, "("+sql+")")
	}
	return conds, nil
}

func (tr *translator) emitPendingRowFiltersAsWhere() error {
	if tr.rowsScoped {
		return nil
	}
	tr.rowsScoped = true
	if len(tr.rowFilters) == 0 {
		return nil
	}
	conds, err := tr.pendingRowFilterConditions()
	if err != nil {
		return err
	}
	if len(conds) == 0 {
		return nil
	}
	tr.emit("WHERE")
	tr.emit(strings.Join(conds, " AND "))
	return nil
}

func (tr *translator) emitPendingRowFiltersAfterWhere() error {
	if tr.rowsScoped {
		return nil
	}
	tr.rowsScoped = true
	if len(tr.rowFilters) == 0 {
		return nil
	}
	conds, err := tr.pendingRowFilterConditions()
	if err != nil {
		return err
	}
	if len(conds) == 0 {
		return nil
	}
	tr.emit(strings.Join(conds, " AND "))
	tr.emit("AND")
	return nil
}

func (tr *translator) peek(offset int) tok {
	i := tr.pos + offset
	if i >= len(tr.tokens) {
		return tok{tEOF, ""}
	}
	return tr.tokens[i]
}

func (tr *translator) advance() tok {
	t := tr.tokens[tr.pos]
	tr.pos++
	return t
}

func (tr *translator) emit(s string) {
	tr.parts = append(tr.parts, s)
}

func (tr *translator) build() string {
	var sb strings.Builder
	for i, p := range tr.parts {
		if i > 0 {
			prev := tr.parts[i-1]
			noBefore := p == "," || p == ")" || p == "." || p == "("
			noAfter := prev == "(" || prev == "."
			if !noBefore && !noAfter {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(p)
	}
	return sb.String()
}

// addParam registers a named parameter and returns its SQL placeholder.
// If the value is []any (DSL array converted by unwrapArrayParams), it expands
// to a comma-joined list of placeholders suitable for IN (...) clauses.
func (tr *translator) addParam(name string) string {
	v := tr.paramValues[name]
	if v == nil {
		return "NULL"
	}
	d := dialectOrDefault(tr.opts.Dialect)

	// Expand array parameters for IN clauses: IN (&Param) → IN ($1, $2, $3)
	if items, ok := v.([]any); ok {
		if len(items) == 0 {
			return "NULL"
		}
		var placeholders []string
		for _, item := range items {
			// Skip nil and empty strings — they'd cause cast mismatch (uuid = text)
			if item == nil {
				continue
			}
			if s, ok := item.(string); ok && s == "" {
				continue
			}
			tr.args = append(tr.args, item)
			ph := d.Placeholder(len(tr.args))
			if d.Name() == "postgres" {
				ph += castSuffix(d, item)
			}
			placeholders = append(placeholders, ph)
		}
		if len(placeholders) == 0 {
			return "NULL"
		}
		return strings.Join(placeholders, ", ")
	}

	if d.Name() != "postgres" {
		// SQLite uses positional `?` — each occurrence consumes a separate arg slot.
		// Unlike PostgreSQL's $N, `?` cannot be reused for the same param.
		tr.args = append(tr.args, v)
		return d.Placeholder(len(tr.args))
	}
	// PostgreSQL: $N references can be shared — deduplicate by param name.
	if _, exists := tr.params[name]; !exists {
		tr.args = append(tr.args, v)
		tr.params[name] = len(tr.args)
	}
	return d.Placeholder(tr.params[name]) + castSuffix(d, v)
}

// castSuffix returns the explicit cast suffix for v on the active dialect.
// PG benefits from "::text"/"::numeric" hints; SQLite ignores types so we
// return "".
func castSuffix(d storage.Dialect, v any) string {
	if d.Name() != "postgres" {
		return ""
	}
	return pgCast(v)
}

// parseVTArgs collects argument groups from a virtual-table call.
// The opening "(" has already been consumed; this method consumes until the matching ")".
func (tr *translator) parseVTArgs() [][]tok {
	var groups [][]tok
	var current []tok
	depth := 0
	for {
		t := tr.advance()
		if t.kind == tEOF {
			break
		}
		switch {
		case t.kind == tLParen:
			depth++
			current = append(current, t)
		case t.kind == tRParen && depth > 0:
			depth--
			current = append(current, t)
		case t.kind == tRParen: // depth == 0, closing paren
			groups = append(groups, current)
			return groups
		case t.kind == tComma && depth == 0:
			groups = append(groups, current)
			current = nil
		default:
			current = append(current, t)
		}
	}
	return groups
}

// translateFilterTokens translates a token slice to a SQL expression fragment,
// resolving &params through the translator's shared state.
func (tr *translator) translateFilterTokens(tokens []tok) string {
	var parts []string
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch t.kind {
		case tParam:
			parts = append(parts, tr.addParam(t.val))
		case tIdent:
			upper := strings.ToUpper(t.val)
			if kw, ok := kwMap[upper]; ok {
				parts = append(parts, kw)
			} else if agg, ok := aggFuncs[upper]; ok && i+1 < len(tokens) && tokens[i+1].kind == tLParen {
				parts = append(parts, agg)
			} else {
				lower := strings.ToLower(t.val)
				if col, ok := tr.colMap[lower]; ok {
					parts = append(parts, col)
				} else {
					parts = append(parts, lower)
				}
			}
		case tStr:
			parts = append(parts, "'"+strings.ReplaceAll(t.val, "'", "''")+"'")
		case tNum, tOp, tStar:
			parts = append(parts, t.val)
		case tComma:
			parts = append(parts, ",")
		case tLParen:
			parts = append(parts, "(")
		case tRParen:
			parts = append(parts, ")")
		case tDot:
			parts = append(parts, ".")
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (tr *translator) findRegister(name string) *metadata.Register {
	nl := strings.ToLower(name)
	for _, r := range tr.opts.Registers {
		if strings.ToLower(r.Name) == nl {
			return r
		}
	}
	return nil
}

func (tr *translator) findInfoRegister(name string) *metadata.InfoRegister {
	nl := strings.ToLower(name)
	for _, r := range tr.opts.InfoRegs {
		if strings.ToLower(r.Name) == nl {
			return r
		}
	}
	return nil
}

func dimCols(dims []metadata.Field) []string {
	names := make([]string, len(dims))
	for i, d := range dims {
		names[i] = metadata.ColumnName(d)
	}
	return names
}

// dimSelCols returns SELECT list entries for dimension fields, aliasing
// reference columns (e.g. "номенклатура_id AS номенклатура") so that outer
// queries and DSL code can reference dimensions by their logical names.
func dimSelCols(dims []metadata.Field) []string {
	result := make([]string, len(dims))
	for i, d := range dims {
		col := metadata.ColumnName(d)
		name := strings.ToLower(d.Name)
		if col != name {
			result[i] = col + " AS " + name
		} else {
			result[i] = col
		}
	}
	return result
}

// buildAccumVT generates a SQL subquery for an accumulation register virtual table.
func (tr *translator) buildAccumVT(vtKind, regName string, args [][]tok) (subq, alias string, err error) {
	reg := tr.findRegister(regName)
	if reg == nil {
		return "", "", fmt.Errorf("accumulation register %q not found; pass Registers in CompileOpts", regName)
	}
	switch vtKind {
	case "balances":
		return tr.genBalances(reg, args)
	case "turnovers":
		return tr.genTurnovers(reg, args)
	case "balances_turnovers":
		return tr.genBalancesAndTurnovers(reg, args)
	}
	return "", "", fmt.Errorf("unknown accumulation virtual table: %s", vtKind)
}

// buildInfoVT generates a SQL subquery for an information register virtual table.
func (tr *translator) buildInfoVT(vtKind, regName string, args [][]tok) (subq, alias string, err error) {
	ir := tr.findInfoRegister(regName)
	if ir == nil {
		return "", "", fmt.Errorf("information register %q not found; pass InfoRegs in CompileOpts", regName)
	}
	switch vtKind {
	case "last_slice":
		return tr.genLastSlice(ir, args)
	case "first_slice":
		return tr.genFirstSlice(ir, args)
	}
	return "", "", fmt.Errorf("unknown information virtual table: %s", vtKind)
}

func (tr *translator) findAccountRegister(name string) *metadata.AccountRegister {
	nl := strings.ToLower(name)
	for _, r := range tr.opts.AccountRegs {
		if strings.ToLower(r.Name) == nl {
			return r
		}
	}
	return nil
}

// translateAccountFilter переводит токены фильтра виртуальной таблицы регистра
// бухгалтерии, временно регистрируя в colMap разрешение имён, специфичных для ВТ:
// «Счёт» → a.code и «СубконтоN» / «<ИмяСубконто>» → r.субконтоN. Изменения
// откатываются после перевода, чтобы не повлиять на внешний запрос.
func (tr *translator) translateAccountFilter(ar *metadata.AccountRegister, tokens []tok) string {
	if tr.colMap == nil {
		tr.colMap = make(map[string]string)
	}
	saved := make(map[string]*string)
	set := func(k, v string) {
		if old, ok := tr.colMap[k]; ok {
			s := old
			saved[k] = &s
		} else {
			saved[k] = nil
		}
		tr.colMap[k] = v
	}
	set("счёт", "a.code")
	set("счет", "a.code")
	for i := range ar.Subconto {
		col := metadata.SubcontoColumn(i + 1)
		set(strings.ToLower(col), "r."+col)
		set(strings.ToLower(ar.Subconto[i].Name), "r."+col)
	}
	res := tr.translateFilterTokens(tokens)
	for k, v := range saved {
		if v == nil {
			delete(tr.colMap, k)
		} else {
			tr.colMap[k] = *v
		}
	}
	return res
}

// accountMomentCondition — аналог momentTimeCondition для регистра бухгалтерии:
// таблица движений присоединяется под алиасом r, а колонка регистратора —
// «регистратор» (не recorder). Строит «строго до момента» с исключением самого
// документа при перепроведении.
func (tr *translator) accountMomentCondition(mt momentTimeValue) string {
	d := dialectOrDefault(tr.opts.Dialect)
	period, docID := mt.PointInTime()
	tr.args = append(tr.args, period)
	pph := d.Placeholder(len(tr.args))
	if docID == "" {
		return "r.period <= " + pph
	}
	id, err := uuid.Parse(docID)
	if err != nil {
		return "r.period <= " + pph
	}
	tr.args = append(tr.args, period)
	pph2 := d.Placeholder(len(tr.args))
	if d.Name() == "sqlite" {
		tr.args = append(tr.args, id.String())
	} else {
		tr.args = append(tr.args, id)
	}
	docPH := d.Placeholder(len(tr.args))
	return fmt.Sprintf("(r.period < %s OR (r.period = %s AND r.регистратор != %s))",
		pph, pph2, docPH)
}

// accountSubcontoSelect returns SELECT and GROUP BY fragments that roll account
// balances/turnovers out by every declared subconto (aliased субконтоN).
func accountSubcontoSelect(ar *metadata.AccountRegister) (selCols []string, groupCols []string) {
	for i := range ar.Subconto {
		col := metadata.SubcontoColumn(i + 1)
		selCols = append(selCols, "r."+col+" AS "+col)
		groupCols = append(groupCols, "r."+col)
	}
	return selCols, groupCols
}

// buildAccountVT generates a SQL subquery for an accounting register virtual table.
func (tr *translator) buildAccountVT(vtKind, regName string, args [][]tok) (subq, alias string, err error) {
	ar := tr.findAccountRegister(regName)
	if ar == nil {
		return "", "", fmt.Errorf("accounting register %q not found; pass AccountRegs in CompileOpts", regName)
	}
	switch vtKind {
	case "balances":
		return tr.genAccountBalances(ar, args)
	case "turnovers":
		return tr.genAccountTurnovers(ar, args)
	}
	return "", "", fmt.Errorf("unknown accounting virtual table: %s", vtKind)
}

func (tr *translator) genAccountBalances(ar *metadata.AccountRegister, args [][]tok) (string, string, error) {
	table := metadata.AccountRegTableName(ar.Name)
	alias := "остатки_" + strings.ToLower(ar.Name)

	// План 80: быстрый путь через предрасчитанные помесячные итоги вместо SUM по
	// всей истории проводок. Включается, когда заведомо эквивалентен on-the-fly:
	// итоги применимы, нет отбора по счёту (args[1]) и нет активной строковой
	// политики. Текущие остатки — SUM по всем месяцам; Остатки(&Момент) — месяцы
	// до момента (из итогов) + хвост проводок месяца момента (из акк_).
	if ar.TotalsUsable() && !accountHasFilterArg(args) && tr.sourceRowFilter("register", ar.Name) == nil {
		if !anyArgTokens(args) {
			return tr.genAccountBalancesFromTotals(ar), alias, nil
		}
		if sql, ok, err := tr.genAccountBalancesFromTotalsAtMoment(ar, args[0]); err != nil {
			return "", "", err
		} else if ok {
			return sql, alias, nil
		}
		// момент/дата не распознаны как значение — обычный путь ниже
	}

	var resCols []string
	for _, r := range ar.Resources {
		col := strings.ToLower(r.Name)
		resCols = append(resCols,
			"COALESCE(SUM(CASE WHEN r.счётдт = a.code THEN r."+col+" ELSE 0 END),0) AS "+col+"_дт",
			"COALESCE(SUM(CASE WHEN r.счёткт = a.code THEN r."+col+" ELSE 0 END),0) AS "+col+"_кт",
			"COALESCE(SUM(CASE WHEN r.счётдт = a.code THEN r."+col+" ELSE -r."+col+" END),0) AS "+col+"остаток",
		)
	}

	subSel, subGroup := accountSubcontoSelect(ar)

	selectList := "a.code AS счёт, a.name AS наименование"
	if len(subSel) > 0 {
		selectList += ", " + strings.Join(subSel, ", ")
	}
	if len(resCols) > 0 {
		selectList += ", " + strings.Join(resCols, ", ")
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(selectList)
	sb.WriteString(" FROM _accounts a LEFT JOIN ")
	sb.WriteString(table)
	sb.WriteString(" r ON (r.счётдт = a.code OR r.счёткт = a.code)")

	var conds []string
	if len(args) > 0 && len(args[0]) > 0 {
		// Первый аргумент — момент времени (&МВ = МоментВремени документа) или дата.
		if mt := tr.firstArgMoment(args[0]); mt != nil {
			conds = append(conds, tr.accountMomentCondition(mt))
		} else if s := tr.translateFilterTokens(args[0]); s != "" && s != "NULL" {
			conds = append(conds, "r.period <= "+s)
		}
	}
	if len(conds) > 0 {
		sb.WriteString(" AND ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	if s, err := tr.rowFilterCondition("register", ar.Name, storage.AccountRegisterPredicateEntity(ar), "r"); err != nil {
		return "", "", fmt.Errorf("row filter %s: %w", ar.Name, err)
	} else if s != "" {
		sb.WriteString(" AND ")
		sb.WriteString(s)
	}
	if len(args) > 1 && len(args[1]) > 0 {
		if s := tr.translateAccountFilter(ar, args[1]); s != "" {
			sb.WriteString(" WHERE (")
			sb.WriteString(s)
			sb.WriteString(")")
		}
	}
	sb.WriteString(" GROUP BY a.code, a.name")
	if len(subGroup) > 0 {
		sb.WriteString(", " + strings.Join(subGroup, ", "))
	}

	return sb.String(), alias, nil
}

// accountHasFilterArg сообщает, передан ли виртуальной таблице бухрегистра отбор
// по счёту (args[1]). При отборе быстрый путь итогов не используется.
func accountHasFilterArg(args [][]tok) bool {
	return len(args) >= 2 && len(args[1]) > 0
}

// genAccountBalancesFromTotals — текущие остатки бухрегистра из предрасчитанных
// итогов (план 80): сальдо счёта = Σ(Дт − Кт) по всем месяцам. LEFT JOIN на
// _accounts и набор колонок совпадают с genAccountBalances, чтобы внешний запрос
// не различал источник (счета без проводок дают строку с нулевым сальдо).
func (tr *translator) genAccountBalancesFromTotals(ar *metadata.AccountRegister) string {
	totals := metadata.AccountRegTotalsTableName(ar.Name)
	var resCols []string
	for _, r := range ar.Resources {
		col := metadata.ColumnName(r)
		dc, cc := col+"_дт", col+"_кт"
		resCols = append(resCols,
			"COALESCE(SUM(t."+dc+"),0) AS "+dc,
			"COALESCE(SUM(t."+cc+"),0) AS "+cc,
			"COALESCE(SUM(t."+dc+" - t."+cc+"),0) AS "+col+"остаток",
		)
	}
	selectList := "a.code AS счёт, a.name AS наименование"
	var subGroup []string
	for i := range ar.Subconto {
		col := metadata.SubcontoColumn(i + 1)
		selectList += ", t." + col + " AS " + col
		subGroup = append(subGroup, "t."+col)
	}
	if len(resCols) > 0 {
		selectList += ", " + strings.Join(resCols, ", ")
	}
	sql := "SELECT " + selectList + " FROM _accounts a LEFT JOIN " + totals +
		" t ON t.счёт = a.code GROUP BY a.code, a.name"
	if len(subGroup) > 0 {
		sql += ", " + strings.Join(subGroup, ", ")
	}
	return sql
}

// genAccountBalancesFromTotalsAtMoment — остатки бухрегистра НА МОМЕНТ из итогов:
// обороты Дт/Кт месяцев СТРОГО ДО месяца момента (из итоги_акк_*) + хвост проводок
// внутри месяца момента до самого момента (из акк_*), развёрнутый на дебет/кредит.
// Границу месяца вычисляем в Go, поэтому в SQL нет диалектных функций дат. Колонки
// (счёт, наименование, субконто…, <res>_дт/_кт/остаток) и LEFT JOIN на _accounts
// совпадают с genAccountBalances. ok=false — момент/дата не распознаны как значение
// (вызывающий падает на обычный расчёт).
//
// SQLite-плейсхолдеры анонимные ('?'), поэтому аргументы добавляются в tr.args
// СТРОГО в порядке появления плейсхолдеров в тексте: prior (ключ месяца), затем
// каждая tail-половина (начало месяца + условие момента со своими аргументами).
func (tr *translator) genAccountBalancesFromTotalsAtMoment(ar *metadata.AccountRegister, arg0 []tok) (string, bool, error) {
	d := dialectOrDefault(tr.opts.Dialect)

	var period time.Time
	var buildCond func() string
	if mt := tr.firstArgMoment(arg0); mt != nil {
		period, _ = mt.PointInTime()
		buildCond = func() string { return tr.accountMomentCondition(mt) }
	} else if dt, ok := tr.firstArgDate(arg0); ok {
		period = dt
		buildCond = func() string {
			tr.args = append(tr.args, dt)
			return "r.period <= " + d.Placeholder(len(tr.args))
		}
	} else {
		return "", false, nil
	}
	period = period.UTC()
	var subCols []string
	for i := range ar.Subconto {
		subCols = append(subCols, metadata.SubcontoColumn(i+1))
	}

	// prior: обороты Дт/Кт месяцев строго до месяца момента из итогов.
	tr.args = append(tr.args, period.Format("2006-01"))
	mkPH := d.Placeholder(len(tr.args))
	priorSel := append([]string{"счёт"}, subCols...)
	for _, res := range ar.Resources {
		col := metadata.ColumnName(res)
		priorSel = append(priorSel, col+"_дт", col+"_кт")
	}
	prior := "SELECT " + strings.Join(priorSel, ", ") + " FROM " + metadata.AccountRegTotalsTableName(ar.Name) +
		" WHERE " + metadata.RegisterTotalsMonthCol + " < " + mkPH

	// tail-половина: проводки [начало месяца момента; момент], развёрнутые на счёт.
	tailHalf := func(acctCol string, debit bool) string {
		tr.args = append(tr.args, monthStartOf(period))
		msPH := d.Placeholder(len(tr.args))
		cond := buildCond()
		sel := []string{"r." + acctCol + " AS счёт"}
		for _, c := range subCols {
			sel = append(sel, "r."+c+" AS "+c)
		}
		for _, res := range ar.Resources {
			col := metadata.ColumnName(res)
			if debit {
				sel = append(sel, "r."+col+" AS "+col+"_дт", "0 AS "+col+"_кт")
			} else {
				sel = append(sel, "0 AS "+col+"_дт", "r."+col+" AS "+col+"_кт")
			}
		}
		return "SELECT " + strings.Join(sel, ", ") + " FROM " + metadata.AccountRegTableName(ar.Name) +
			" r WHERE r.period >= " + msPH + " AND " + cond
	}
	tailDt := tailHalf("счётдт", true)
	tailKt := tailHalf("счёткт", false)
	inner := prior + " UNION ALL " + tailDt + " UNION ALL " + tailKt

	// Внешний агрегат: LEFT JOIN _accounts (все счета) + сальдо = Σ(Дт − Кт).
	outer := "a.code AS счёт, a.name AS наименование"
	var groupSub []string
	for _, c := range subCols {
		outer += ", u." + c + " AS " + c
		groupSub = append(groupSub, "u."+c)
	}
	for _, res := range ar.Resources {
		col := metadata.ColumnName(res)
		dc, cc := col+"_дт", col+"_кт"
		outer += ", COALESCE(SUM(u." + dc + "),0) AS " + dc +
			", COALESCE(SUM(u." + cc + "),0) AS " + cc +
			", COALESCE(SUM(u." + dc + " - u." + cc + "),0) AS " + col + "остаток"
	}
	sql := "SELECT " + outer + " FROM _accounts a LEFT JOIN (" + inner + ") u ON u.счёт = a.code GROUP BY a.code, a.name"
	if len(groupSub) > 0 {
		sql += ", " + strings.Join(groupSub, ", ")
	}
	return sql, true, nil
}

// accountTurnoversHasFilterArg — передан ли оборотам отбор по счёту (args[2]).
func accountTurnoversHasFilterArg(args [][]tok) bool {
	return len(args) >= 3 && len(args[2]) > 0
}

// turnoverTotalsSel — колонки итогов для оборотов: счёт, субконто…, <res>_дт/_кт.
func turnoverTotalsSel(ar *metadata.AccountRegister, subCols []string) []string {
	sel := append([]string{"счёт"}, subCols...)
	for _, res := range ar.Resources {
		col := metadata.ColumnName(res)
		sel = append(sel, col+"_дт", col+"_кт")
	}
	return sel
}

// genAccountTurnoversFromTotals — обороты бухрегистра из помесячных итогов (план 80).
// Полный период (без границ) — сумма всех итогов. Диапазон [from, to] дат: полные
// месяцы строго между месяцами from и to берутся из итогов, частичные месяцы на
// концах — из проводок акк_ (развёрнутых на Дт/Кт). Один месяц — целиком из проводок.
// Момент/одна граница/не-дата → ok=false (обычный расчёт). Аргументы добавляются в
// порядке плейсхолдеров (анонимные '?' SQLite).
func (tr *translator) genAccountTurnoversFromTotals(ar *metadata.AccountRegister, args [][]tok) (string, bool, error) {
	d := dialectOrDefault(tr.opts.Dialect)
	var subCols []string
	for i := range ar.Subconto {
		subCols = append(subCols, metadata.SubcontoColumn(i+1))
	}
	totals := metadata.AccountRegTotalsTableName(ar.Name)

	hasFrom := len(args) > 0 && len(args[0]) > 0
	hasTo := len(args) > 1 && len(args[1]) > 0

	// Полный период: сумма всех помесячных итогов по счёту.
	if !hasFrom && !hasTo {
		inner := "SELECT " + strings.Join(turnoverTotalsSel(ar, subCols), ", ") + " FROM " + totals
		return tr.accountTurnoversOuter(ar, subCols, inner), true, nil
	}
	if !hasFrom || !hasTo {
		return "", false, nil // односторонний диапазон — на лету
	}
	fromT, ok1 := tr.firstArgDate(args[0])
	toT, ok2 := tr.firstArgDate(args[1])
	if !ok1 || !ok2 {
		return "", false, nil // моменты/не-даты — на лету
	}
	fromT, toT = fromT.UTC(), toT.UTC()

	// tail-половина: проводки периода [lo, hi)/[lo, hi], развёрнутые на счёт.
	tailHalf := func(acctCol string, debit bool, lo, hi time.Time, hiIncl bool) string {
		tr.args = append(tr.args, lo)
		loPH := d.Placeholder(len(tr.args))
		tr.args = append(tr.args, hi)
		hiPH := d.Placeholder(len(tr.args))
		hiOp := " < "
		if hiIncl {
			hiOp = " <= "
		}
		sel := []string{"r." + acctCol + " AS счёт"}
		for _, c := range subCols {
			sel = append(sel, "r."+c+" AS "+c)
		}
		for _, res := range ar.Resources {
			col := metadata.ColumnName(res)
			if debit {
				sel = append(sel, "r."+col+" AS "+col+"_дт", "0 AS "+col+"_кт")
			} else {
				sel = append(sel, "0 AS "+col+"_дт", "r."+col+" AS "+col+"_кт")
			}
		}
		return "SELECT " + strings.Join(sel, ", ") + " FROM " + metadata.AccountRegTableName(ar.Name) +
			" r WHERE r.period >= " + loPH + " AND r.period" + hiOp + hiPH
	}

	var halves []string
	if fromT.Format("2006-01") == toT.Format("2006-01") {
		// один месяц — весь диапазон из проводок.
		halves = append(halves,
			tailHalf("счётдт", true, fromT, toT, true),
			tailHalf("счёткт", false, fromT, toT, true))
	} else {
		// полные месяцы строго между from и to — из итогов.
		tr.args = append(tr.args, fromT.Format("2006-01"))
		mkFromPH := d.Placeholder(len(tr.args))
		tr.args = append(tr.args, toT.Format("2006-01"))
		mkToPH := d.Placeholder(len(tr.args))
		prior := "SELECT " + strings.Join(turnoverTotalsSel(ar, subCols), ", ") + " FROM " + totals +
			" WHERE " + metadata.RegisterTotalsMonthCol + " > " + mkFromPH +
			" AND " + metadata.RegisterTotalsMonthCol + " < " + mkToPH
		fromNext := monthStartOf(fromT).AddDate(0, 1, 0)
		toStart := monthStartOf(toT)
		halves = append(halves, prior,
			tailHalf("счётдт", true, fromT, fromNext, false),
			tailHalf("счёткт", false, fromT, fromNext, false),
			tailHalf("счётдт", true, toStart, toT, true),
			tailHalf("счёткт", false, toStart, toT, true))
	}
	return tr.accountTurnoversOuter(ar, subCols, strings.Join(halves, " UNION ALL ")), true, nil
}

// accountTurnoversOuter агрегирует внутренний источник (итоги/разворот) в обороты
// Дт/Кт по счёту, присоединяя наименование из _accounts. Только счета с движениями
// (их и содержит источник) — как HAVING в genAccountTurnovers.
func (tr *translator) accountTurnoversOuter(ar *metadata.AccountRegister, subCols []string, inner string) string {
	out := "u.счёт AS счёт, a.name AS наименование"
	var groupSub []string
	for _, c := range subCols {
		out += ", u." + c + " AS " + c
		groupSub = append(groupSub, "u."+c)
	}
	for _, res := range ar.Resources {
		col := metadata.ColumnName(res)
		out += ", COALESCE(SUM(u." + col + "_дт),0) AS " + col + "_дт" +
			", COALESCE(SUM(u." + col + "_кт),0) AS " + col + "_кт"
	}
	sql := "SELECT " + out + " FROM (" + inner + ") u JOIN _accounts a ON a.code = u.счёт GROUP BY u.счёт, a.name"
	if len(groupSub) > 0 {
		sql += ", " + strings.Join(groupSub, ", ")
	}
	return sql
}

func (tr *translator) genAccountTurnovers(ar *metadata.AccountRegister, args [][]tok) (string, string, error) {
	table := metadata.AccountRegTableName(ar.Name)
	alias := "обороты_" + strings.ToLower(ar.Name)

	// План 80: быстрый путь оборотов через помесячные итоги. Применим без отбора по
	// счёту (args[2]) и без строковой политики. Полный период или обе границы-даты —
	// из итогов (+ хвосты частичных месяцев); момент/односторонний диапазон — на лету.
	if ar.TotalsUsable() && !accountTurnoversHasFilterArg(args) && tr.sourceRowFilter("register", ar.Name) == nil {
		if sql, ok, err := tr.genAccountTurnoversFromTotals(ar, args); err != nil {
			return "", "", err
		} else if ok {
			return sql, alias, nil
		}
	}

	var resCols []string
	for _, r := range ar.Resources {
		col := strings.ToLower(r.Name)
		resCols = append(resCols,
			"COALESCE(SUM(CASE WHEN r.счётдт = a.code THEN r."+col+" ELSE 0 END),0) AS "+col+"_дт",
			"COALESCE(SUM(CASE WHEN r.счёткт = a.code THEN r."+col+" ELSE 0 END),0) AS "+col+"_кт",
		)
	}

	subSel, subGroup := accountSubcontoSelect(ar)

	selectList := "a.code AS счёт, a.name AS наименование"
	if len(subSel) > 0 {
		selectList += ", " + strings.Join(subSel, ", ")
	}
	if len(resCols) > 0 {
		selectList += ", " + strings.Join(resCols, ", ")
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(selectList)
	sb.WriteString(" FROM _accounts a LEFT JOIN ")
	sb.WriteString(table)
	sb.WriteString(" r ON (r.счётдт = a.code OR r.счёткт = a.code)")

	var conds []string
	if len(args) > 0 && len(args[0]) > 0 {
		if s := tr.translateFilterTokens(args[0]); s != "" && s != "NULL" {
			conds = append(conds, "r.period >= "+s)
		}
	}
	if len(args) > 1 && len(args[1]) > 0 {
		if s := tr.translateFilterTokens(args[1]); s != "" && s != "NULL" {
			conds = append(conds, "r.period <= "+s)
		}
	}
	if len(conds) > 0 {
		sb.WriteString(" AND ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	if s, err := tr.rowFilterCondition("register", ar.Name, storage.AccountRegisterPredicateEntity(ar), "r"); err != nil {
		return "", "", fmt.Errorf("row filter %s: %w", ar.Name, err)
	} else if s != "" {
		sb.WriteString(" AND ")
		sb.WriteString(s)
	}
	if len(args) > 2 && len(args[2]) > 0 {
		if s := tr.translateAccountFilter(ar, args[2]); s != "" {
			sb.WriteString(" WHERE (")
			sb.WriteString(s)
			sb.WriteString(")")
		}
	}
	sb.WriteString(" GROUP BY a.code, a.name")
	if len(subGroup) > 0 {
		sb.WriteString(", " + strings.Join(subGroup, ", "))
	}
	sb.WriteString(" HAVING SUM(CASE WHEN r.id IS NOT NULL THEN 1 ELSE 0 END) > 0")

	return sb.String(), alias, nil
}

func (tr *translator) genBalances(reg *metadata.Register, args [][]tok) (string, string, error) {
	tableName := metadata.RegisterTableName(reg.Name)
	alias := "остатки_" + strings.ToLower(reg.Name)

	// План 80: быстрый путь через предрасчитанные помесячные итоги вместо SUM по
	// всей истории. Включается только когда заведомо эквивалентен on-the-fly:
	// итоги применимы (включены, регистр балансовый и без атрибутов), нет отбора
	// в аргументах (args[1]) и нет активной строковой политики (её применяет
	// обычный путь). Текущие остатки — SUM по всем месяцам; Остатки(&Момент) —
	// месяцы до момента + хвост движений месяца момента.
	if reg.TotalsUsable() && !hasFilterArg(args) && tr.sourceRowFilter("register", reg.Name) == nil {
		if !anyArgTokens(args) {
			return tr.genBalancesFromTotals(reg), alias, nil
		}
		if sql, ok, err := tr.genBalancesFromTotalsAtMoment(reg, args[0]); err != nil {
			return "", "", err
		} else if ok {
			return sql, alias, nil
		}
		// момент/дата не распознаны как значение — обычный путь ниже
	}

	dims := dimCols(reg.Dimensions)       // actual col names for GROUP BY / WHERE
	selDims := dimSelCols(reg.Dimensions) // aliased names for SELECT

	var cols []string
	cols = append(cols, selDims...)
	for _, r := range reg.Resources {
		col := strings.ToLower(r.Name)
		cols = append(cols,
			"SUM(CASE WHEN вид_движения = 'Приход' THEN "+col+" ELSE -"+col+" END) AS "+col+"остаток")
	}
	// атрибуты — не часть ключа измерения, но должны быть
	// доступны в outer SELECT/WHERE. Берём MIN(col) — детерминированно
	// и работает в обоих диалектах (TEXT/UUID сравнимы лексикографически).
	cols = append(cols, attributeAggCols(reg.Attributes)...)

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(cols, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(tableName)

	var conds []string
	if len(args) > 0 && len(args[0]) > 0 {
		// момент времени документа — особое условие.
		if mt := tr.firstArgMoment(args[0]); mt != nil {
			conds = append(conds, tr.momentTimeCondition(mt))
		} else if s := tr.translateFilterTokens(args[0]); s != "" && s != "NULL" {
			conds = append(conds, "period <= "+s)
		}
	}
	if len(args) > 1 && len(args[1]) > 0 {
		if s := tr.translateFilterTokens(args[1]); s != "" {
			conds = append(conds, s)
		}
	}
	if s, err := tr.rowFilterCondition("register", reg.Name, storage.RegisterPredicateEntity(reg), ""); err != nil {
		return "", "", fmt.Errorf("row filter %s: %w", reg.Name, err)
	} else if s != "" {
		conds = append(conds, s)
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	if len(dims) > 0 {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(dims, ", "))
	}

	return sb.String(), alias, nil
}

// anyArgTokens сообщает, есть ли у виртуальной таблицы непустые аргументы
// (момент времени или условие отбора). Пусто → «текущие остатки».
func anyArgTokens(args [][]tok) bool {
	for _, a := range args {
		if len(a) > 0 {
			return true
		}
	}
	return false
}

// hasFilterArg сообщает, передан ли виртуальной таблице отбор (args[1]).
// При отборе быстрый путь итогов не используется — работает обычный расчёт.
func hasFilterArg(args [][]tok) bool {
	return len(args) >= 2 && len(args[1]) > 0
}

func monthStartOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// genBalancesFromTotals — текущие остатки из помесячных итогов: SUM оборотов по
// всем месяцам. Колонки совпадают с обычным genBalances (измерения +
// <ресурс>остаток), чтобы внешний запрос не различал источник.
func (tr *translator) genBalancesFromTotals(reg *metadata.Register) string {
	cols := append([]string{}, dimSelCols(reg.Dimensions)...)
	for _, r := range reg.Resources {
		col := strings.ToLower(r.Name)
		cols = append(cols, "SUM("+col+") AS "+col+"остаток")
	}
	sql := "SELECT " + strings.Join(cols, ", ") + " FROM " + metadata.RegisterTotalsTableName(reg.Name)
	if len(reg.Dimensions) > 0 {
		sql += " GROUP BY " + strings.Join(dimCols(reg.Dimensions), ", ")
	}
	return sql
}

// firstArgDate распознаёт первый аргумент VT как &Param со значением time.Time
// (простая дата, не момент времени). Нужен для быстрого пути Остатки(&Дата).
func (tr *translator) firstArgDate(args []tok) (time.Time, bool) {
	if len(args) != 1 || args[0].kind != tParam {
		return time.Time{}, false
	}
	if t, ok := tr.paramValues[args[0].val].(time.Time); ok {
		return t, true
	}
	return time.Time{}, false
}

// genBalancesFromTotalsAtMoment строит остатки на момент времени из итогов:
// обороты месяцев СТРОГО ДО месяца момента (из итоги_*) + знаковый хвост
// движений внутри месяца момента до самого момента (из рег_*). Границу месяца
// (месяц-ключ и его начало) вычисляем в Go из значения момента/даты, поэтому в
// SQL нет диалектных функций дат. ok=false — момент/дата не распознаны как
// значение (вызывающий падает на обычный путь).
func (tr *translator) genBalancesFromTotalsAtMoment(reg *metadata.Register, arg0 []tok) (string, bool, error) {
	d := dialectOrDefault(tr.opts.Dialect)

	emit := func(period time.Time, condSQL func() string) string {
		period = period.UTC()
		// Порядок добавления аргументов = порядку плейсхолдеров в SQL:
		// сначала месяц-ключ (prior), затем начало месяца (tail), затем условие
		// момента (его аргументы добавляет condSQL).
		tr.args = append(tr.args, period.Format("2006-01"))
		mkPH := d.Placeholder(len(tr.args))
		tr.args = append(tr.args, monthStartOf(period))
		msPH := d.Placeholder(len(tr.args))
		cond := condSQL()
		return tr.buildTotalsMomentSQL(reg, mkPH, msPH, cond)
	}

	if mt := tr.firstArgMoment(arg0); mt != nil {
		period, _ := mt.PointInTime()
		return emit(period, func() string { return tr.momentTimeCondition(mt) }), true, nil
	}
	if dt, ok := tr.firstArgDate(arg0); ok {
		return emit(dt, func() string {
			tr.args = append(tr.args, dt)
			return "period <= " + d.Placeholder(len(tr.args))
		}), true, nil
	}
	return "", false, nil
}

// buildTotalsMomentSQL собирает подзапрос «prior UNION ALL tail» и внешнюю
// агрегацию. Внутренние ресурсы приводятся к алиасам r0, r1… (знаковый оборот),
// внешний SELECT суммирует их в <ресурс>остаток — колонки как у genBalances.
func (tr *translator) buildTotalsMomentSQL(reg *metadata.Register, mkPH, msPH, condSQL string) string {
	totals := metadata.RegisterTotalsTableName(reg.Name)
	src := metadata.RegisterTableName(reg.Name)
	dims := dimCols(reg.Dimensions)

	priorCols := append([]string{}, dims...)
	tailCols := append([]string{}, dims...)
	for i, r := range reg.Resources {
		rescol := strings.ToLower(r.Name)
		ri := fmt.Sprintf("r%d", i)
		priorCols = append(priorCols, rescol+" AS "+ri)
		tailCols = append(tailCols, "CASE WHEN вид_движения = 'Приход' THEN "+rescol+" ELSE -"+rescol+" END AS "+ri)
	}
	prior := "SELECT " + strings.Join(priorCols, ", ") + " FROM " + totals +
		" WHERE " + metadata.RegisterTotalsMonthCol + " < " + mkPH
	tail := "SELECT " + strings.Join(tailCols, ", ") + " FROM " + src +
		" WHERE period >= " + msPH + " AND " + condSQL
	inner := prior + " UNION ALL " + tail

	outerCols := append([]string{}, dimSelCols(reg.Dimensions)...)
	for i, r := range reg.Resources {
		rescol := strings.ToLower(r.Name)
		outerCols = append(outerCols, "SUM(r"+fmt.Sprint(i)+") AS "+rescol+"остаток")
	}
	sql := "SELECT " + strings.Join(outerCols, ", ") + " FROM (" + inner + ") AS итоги_мт"
	if len(dims) > 0 {
		sql += " GROUP BY " + strings.Join(dims, ", ")
	}
	return sql
}

// genBalancesAndTurnoversFromTotals строит ОстаткиИОбороты(&start, &end) из
// итогов. Начальный остаток (period < start) = обороты месяцев до месяца start
// (из итоги_*) + знаковый хвост движений месяца start до start (из рег_*).
// Приход/расход = движения в [start, end] по виду. Конечный = начальный +
// приход − расход. Колонки совпадают с обычным genBalancesAndTurnovers.
func (tr *translator) genBalancesAndTurnoversFromTotals(reg *metadata.Register, start, end time.Time) string {
	d := dialectOrDefault(tr.opts.Dialect)
	totals := metadata.RegisterTotalsTableName(reg.Name)
	src := metadata.RegisterTableName(reg.Name)
	dims := dimCols(reg.Dimensions)

	// Аргументы в порядке появления плейсхолдеров в SQL.
	// Ключи итогов и границы месяцев хранятся в UTC. Без нормализации момент
	// около начала месяца с ненулевым offset мог выбрать соседний месяц итогов.
	tr.args = append(tr.args, start.UTC().Format("2006-01"))
	mkPH := d.Placeholder(len(tr.args))
	tr.args = append(tr.args, monthStartOf(start))
	msPH := d.Placeholder(len(tr.args))
	tr.args = append(tr.args, start)
	s1PH := d.Placeholder(len(tr.args))
	tr.args = append(tr.args, start)
	s2PH := d.Placeholder(len(tr.args))
	tr.args = append(tr.args, end)
	ePH := d.Placeholder(len(tr.args))

	// Внутренние алиасы на ресурс i: n=начальный вклад, p=приход, r=расход.
	priorCols := append([]string{}, dims...)
	ntailCols := append([]string{}, dims...)
	turnCols := append([]string{}, dims...)
	for i, res := range reg.Resources {
		c := strings.ToLower(res.Name)
		n, p, r := fmt.Sprintf("n%d", i), fmt.Sprintf("p%d", i), fmt.Sprintf("r%d", i)
		priorCols = append(priorCols, c+" AS "+n, "0 AS "+p, "0 AS "+r)
		ntailCols = append(ntailCols,
			"CASE WHEN вид_движения = 'Приход' THEN "+c+" ELSE -"+c+" END AS "+n, "0 AS "+p, "0 AS "+r)
		turnCols = append(turnCols, "0 AS "+n,
			"CASE WHEN вид_движения = 'Приход' THEN "+c+" ELSE 0 END AS "+p,
			"CASE WHEN вид_движения = 'Расход' THEN "+c+" ELSE 0 END AS "+r)
	}
	prior := "SELECT " + strings.Join(priorCols, ", ") + " FROM " + totals +
		" WHERE " + metadata.RegisterTotalsMonthCol + " < " + mkPH
	ntail := "SELECT " + strings.Join(ntailCols, ", ") + " FROM " + src +
		" WHERE period >= " + msPH + " AND period < " + s1PH
	turn := "SELECT " + strings.Join(turnCols, ", ") + " FROM " + src +
		" WHERE period >= " + s2PH + " AND period <= " + ePH
	inner := prior + " UNION ALL " + ntail + " UNION ALL " + turn

	outer := append([]string{}, dimSelCols(reg.Dimensions)...)
	for i, res := range reg.Resources {
		c := strings.ToLower(res.Name)
		n, p, r := fmt.Sprintf("n%d", i), fmt.Sprintf("p%d", i), fmt.Sprintf("r%d", i)
		outer = append(outer,
			"SUM("+n+") AS "+c+"начальный",
			"SUM("+p+") AS "+c+"приход",
			"SUM("+r+") AS "+c+"расход",
			"SUM("+n+") + SUM("+p+") - SUM("+r+") AS "+c+"конечный")
	}
	sql := "SELECT " + strings.Join(outer, ", ") + " FROM (" + inner + ") AS оио_" + strings.ToLower(reg.Name)
	if len(dims) > 0 {
		sql += " GROUP BY " + strings.Join(dims, ", ")
	}
	return sql
}

func (tr *translator) genTurnovers(reg *metadata.Register, args [][]tok) (string, string, error) {
	tableName := metadata.RegisterTableName(reg.Name)
	alias := "обороты_" + strings.ToLower(reg.Name)
	d := dialectOrDefault(tr.opts.Dialect)
	dims := dimCols(reg.Dimensions)
	selDims := dimSelCols(reg.Dimensions)

	// Detect periodicity in args[2]: if it is a single keyword like Месяц/День/…,
	// treat it as periodicity and shift the filter to args[3] (if present).
	var periodLevel string
	filterArgIdx := 2
	if len(args) > 2 && len(args[2]) > 0 {
		if pl, ok := detectPeriodicity(args[2]); ok {
			periodLevel = pl
			filterArgIdx = -1
		}
	}

	var cols []string
	// Period column (truncated) — first, before dimensions, matching 1C convention.
	// Alias is the physical "period" column name (Latin), so that the outer query's
	// Период → systemColAlias → "period" reference resolves on both engines.
	if periodLevel != "" {
		cols = append(cols, periodTruncSQL(periodLevel, d)+" AS period")
	}
	cols = append(cols, selDims...)
	for _, r := range reg.Resources {
		col := strings.ToLower(r.Name)
		cols = append(cols,
			"SUM(CASE WHEN вид_движения = 'Приход' THEN "+col+" ELSE 0 END) AS "+col+"приход",
			"SUM(CASE WHEN вид_движения = 'Расход' THEN "+col+" ELSE 0 END) AS "+col+"расход",
			"SUM(CASE WHEN вид_движения = 'Приход' THEN "+col+" ELSE -"+col+" END) AS "+col+"оборот",
		)
	}
	cols = append(cols, attributeAggCols(reg.Attributes)...)

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(cols, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(tableName)

	var conds []string
	if len(args) > 0 && len(args[0]) > 0 {
		if s := tr.translateFilterTokens(args[0]); s != "" && s != "NULL" {
			conds = append(conds, "period >= "+s)
		}
	}
	if len(args) > 1 && len(args[1]) > 0 {
		if s := tr.translateFilterTokens(args[1]); s != "" && s != "NULL" {
			conds = append(conds, "period <= "+s)
		}
	}
	// Filter: from args[filterArgIdx] when periodicity is absent, or from args[3]
	// when periodicity was detected in args[2].
	filterTokens := filterArg(filterArgIdx, periodLevel, args)
	if len(filterTokens) > 0 {
		if s := tr.translateFilterTokens(filterTokens); s != "" {
			conds = append(conds, s)
		}
	}
	if s, err := tr.rowFilterCondition("register", reg.Name, storage.RegisterPredicateEntity(reg), ""); err != nil {
		return "", "", fmt.Errorf("row filter %s: %w", reg.Name, err)
	} else if s != "" {
		conds = append(conds, s)
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	if len(dims) > 0 || periodLevel != "" {
		var groupBy []string
		if periodLevel != "" {
			groupBy = append(groupBy, periodTruncSQL(periodLevel, d))
		}
		groupBy = append(groupBy, dims...)
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(groupBy, ", "))
	}

	return sb.String(), alias, nil
}

// filterArg returns the filter tokens from args, accounting for periodicity shift.
// When periodicity was detected in args[2], the filter lives in args[3].
func filterArg(idx int, periodLevel string, args [][]tok) []tok {
	if periodLevel != "" {
		// periodicity consumed args[2]; filter is in args[3] if present
		if len(args) > 3 && len(args[3]) > 0 {
			return args[3]
		}
		return nil
	}
	if idx >= 0 && len(args) > idx && len(args[idx]) > 0 {
		return args[idx]
	}
	return nil
}

// filterPresent сообщает, задан ли аргумент-условие (граница/отбор) непусто и
// не «пусто» (одиночный &Param с nil/пустым списком). Проверяет структурно, БЕЗ
// трансляции — чтобы не добавить аргумент раньше времени (иначе плейсхолдеры и
// аргументы разъедутся при повторной трансляции границ).
func (tr *translator) filterPresent(tokens []tok) bool {
	if len(tokens) == 0 {
		return false
	}
	if len(tokens) == 1 && tokens[0].kind == tParam {
		v := tr.paramValues[tokens[0].val]
		if v == nil {
			return false
		}
		if arr, ok := v.([]any); ok && len(arr) == 0 {
			return false
		}
	}
	return true
}

func (tr *translator) genBalancesAndTurnovers(reg *metadata.Register, args [][]tok) (string, string, error) {
	tableName := metadata.RegisterTableName(reg.Name)
	alias := "остаткиоборотов_" + strings.ToLower(reg.Name)
	dims := dimCols(reg.Dimensions)
	selDims := dimSelCols(reg.Dimensions)

	// План 80, этап 3: быстрый путь ОстаткиИОбороты(&Начало, &Конец). Начальный
	// остаток (вся история до начала — самая дорогая часть) берётся из итогов,
	// приход/расход — сканом движений, ограниченным периодом [начало, конец];
	// конечный = начальный + приход − расход (выводится). Только когда обе
	// границы — даты-значения, нет отбора (args[2]) и активной строковой политики;
	// иначе обычный расчёт ниже.
	if reg.TotalsUsable() && tr.sourceRowFilter("register", reg.Name) == nil &&
		(len(args) <= 2 || len(args[2]) == 0) && len(args) >= 2 {
		if start, ok1 := tr.firstArgDate(args[0]); ok1 {
			if end, ok2 := tr.firstArgDate(args[1]); ok2 {
				// Обратный диапазон оставляем обычному пути: его историческая
				// семантика ограничения строк period <= end отличается от UNION.
				if !start.After(end) {
					return tr.genBalancesAndTurnoversFromTotals(reg, start, end), alias, nil
				}
			}
		}
	}

	// Границы начало/конец транслируются заново на КАЖДОЕ вхождение (start()/
	// end()/periodCond()), а не один раз. На SQLite плейсхолдер '?' анонимный и
	// позиционный: один startSQL/endSQL, вставленный в SQL многократно, давал бы
	// больше '?', чем привязанных аргументов, — запрос падал с «missing
	// argument». Повторная трансляция добавляет аргумент под каждый плейсхолдер;
	// порядок добавления совпадает с порядком плейсхолдеров в собираемом SQL
	// (столбцы SELECT слева направо, затем WHERE). На PostgreSQL это лишь
	// дублирует значение под разными $N — результат тот же.
	hasStart := len(args) > 0 && tr.filterPresent(args[0])
	hasEnd := len(args) > 1 && tr.filterPresent(args[1])
	hasFilter := len(args) > 2 && tr.filterPresent(args[2])
	start := func() string { return tr.translateFilterTokens(args[0]) }
	end := func() string { return tr.translateFilterTokens(args[1]) }
	periodCond := func() string {
		switch {
		case hasStart && hasEnd:
			return " AND period >= " + start() + " AND period <= " + end()
		case hasStart:
			return " AND period >= " + start()
		case hasEnd:
			return " AND period <= " + end()
		}
		return ""
	}

	var cols []string
	cols = append(cols, selDims...)
	for _, r := range reg.Resources {
		col := strings.ToLower(r.Name)
		if hasStart {
			cols = append(cols,
				"SUM(CASE WHEN вид_движения = 'Приход' AND period < "+start()+
					" THEN "+col+" WHEN вид_движения = 'Расход' AND period < "+start()+
					" THEN -"+col+" ELSE 0 END) AS "+col+"начальный")
		}
		// periodCond() вызывается отдельно для прихода и расхода — каждый со
		// своими плейсхолдерами (нельзя переиспользовать одну строку на SQLite).
		cols = append(cols,
			"SUM(CASE WHEN вид_движения = 'Приход'"+periodCond()+" THEN "+col+" ELSE 0 END) AS "+col+"приход",
			"SUM(CASE WHEN вид_движения = 'Расход'"+periodCond()+" THEN "+col+" ELSE 0 END) AS "+col+"расход",
		)
		if hasEnd {
			cols = append(cols,
				"SUM(CASE WHEN вид_движения = 'Приход' AND period <= "+end()+
					" THEN "+col+" WHEN вид_движения = 'Расход' AND period <= "+end()+
					" THEN -"+col+" ELSE 0 END) AS "+col+"конечный")
		}
	}
	cols = append(cols, attributeAggCols(reg.Attributes)...)

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(cols, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(tableName)

	var conds []string
	if hasEnd {
		conds = append(conds, "period <= "+end())
	}
	if hasFilter {
		conds = append(conds, tr.translateFilterTokens(args[2]))
	}
	if s, err := tr.rowFilterCondition("register", reg.Name, storage.RegisterPredicateEntity(reg), ""); err != nil {
		return "", "", fmt.Errorf("row filter %s: %w", reg.Name, err)
	} else if s != "" {
		conds = append(conds, s)
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	if len(dims) > 0 {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(dims, ", "))
	}

	return sb.String(), alias, nil
}

// attributeAggCols returns "MIN(col) AS col" expressions for each attribute.
// Атрибуты не часть ключа измерения, поэтому в SELECT их нельзя оставлять
// без агрегата (SQL ошибся бы). MIN — детерминированный выбор. Если атрибут
// варьируется в пределах одного значения измерения, MIN отдаст
// лексикографически минимальное; в стабильных учётных моделях такое
// нехарактерно.
func attributeAggCols(attrs []metadata.Field) []string {
	out := make([]string, 0, len(attrs))
	for _, a := range attrs {
		col := metadata.ColumnName(a)
		out = append(out, "MIN("+col+") AS "+col)
	}
	return out
}

// genInfoSlice generates SrezPoslednih/SrezPervyh SQL.
// direction: "DESC" → СрезПоследних (last); "ASC" → СрезПервых (first).
// For non-periodic info registers, DISTINCT semantics are unnecessary —
// we just SELECT FROM the table with WHERE filter.
func (tr *translator) genInfoSlice(ir *metadata.InfoRegister, args [][]tok, direction string, aliasPrefix string) (string, string, error) {
	d := dialectOrDefault(tr.opts.Dialect)
	tableName := metadata.InfoRegTableName(ir.Name)
	alias := aliasPrefix + strings.ToLower(ir.Name)
	dims := dimCols(ir.Dimensions)
	selDims := dimSelCols(ir.Dimensions)

	var resCols []string
	for _, r := range ir.Resources {
		resCols = append(resCols, strings.ToLower(r.Name))
	}

	periodOp := "<="
	if direction == "ASC" {
		periodOp = ">="
	}

	var conds []string
	if ir.Periodic && len(args) > 0 && len(args[0]) > 0 {
		// МоментВремени для info-регистра — берём только Period,
		// recorder в info-таблицах не используется для исключения.
		if mt := tr.firstArgMoment(args[0]); mt != nil {
			d := dialectOrDefault(tr.opts.Dialect)
			p, _ := mt.PointInTime()
			tr.args = append(tr.args, p)
			conds = append(conds, "period "+periodOp+" "+d.Placeholder(len(tr.args)))
		} else if s := tr.translateFilterTokens(args[0]); s != "" && s != "NULL" {
			conds = append(conds, "period "+periodOp+" "+s)
		}
	}
	filterIdx := 1
	if !ir.Periodic {
		filterIdx = 0
	}
	if len(args) > filterIdx && len(args[filterIdx]) > 0 {
		if s := tr.translateFilterTokens(args[filterIdx]); s != "" {
			conds = append(conds, s)
		}
	}
	if s, err := tr.rowFilterCondition("inforeg", ir.Name, storage.InfoRegisterPredicateEntity(ir), ""); err != nil {
		return "", "", fmt.Errorf("row filter %s: %w", ir.Name, err)
	} else if s != "" {
		conds = append(conds, s)
	}
	where := strings.Join(conds, " AND ")

	if ir.Periodic && len(dims) > 0 {
		// SELECT (per dim) the row with latest/earliest period — uses
		// d.LatestPerKey, which gives DISTINCT ON for PG and ROW_NUMBER()
		// OVER (PARTITION BY) for SQLite.
		// selDims: aliased col names for SELECT; dims: actual col names for PARTITION BY.
		selectCols := append([]string{"period"}, append(append([]string{}, selDims...), resCols...)...)
		return d.LatestPerKey(selectCols, dims, []string{"period " + direction}, tableName, "", where), alias, nil
	}

	// Non-periodic or no dimensions — plain SELECT.
	var sb strings.Builder
	var allCols []string
	allCols = append(allCols, selDims...)
	allCols = append(allCols, resCols...)
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(allCols, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(tableName)
	if where != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	return sb.String(), alias, nil
}

func (tr *translator) genLastSlice(ir *metadata.InfoRegister, args [][]tok) (string, string, error) {
	return tr.genInfoSlice(ir, args, "DESC", "срезпоследних_")
}

func (tr *translator) genFirstSlice(ir *metadata.InfoRegister, args [][]tok) (string, string, error) {
	return tr.genInfoSlice(ir, args, "ASC", "срезпервых_")
}

// --- reference dimension auto-join ---

// preScanRefDims pre-scans tokens to find the first simple (non-virtual) register source
// and returns reference dimension info for auto-JOIN generation. Only fields actually
// referenced in the query produce JOINs to avoid ambiguous-column errors.
func preScanRefDims(tokens []tok, opts CompileOpts) []refDimInfo {
	return filterUsedRefDims(preScanAllRefDims(tokens, opts), tokens)
}

func preScanAllRefDims(tokens []tok, opts CompileOpts) []refDimInfo {
	for i := 0; i+2 < len(tokens); i++ {
		t := tokens[i]
		if t.kind != tIdent {
			continue
		}
		upper := strings.ToUpper(t.val)
		if !isSourceType(upper) || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		regName := tokens[i+2].val
		// VT source: TypeName.EntityName.VTName(...)
		if i+3 < len(tokens) && tokens[i+3].kind == tDot {
			if isAccumRegType(upper) {
				for _, reg := range opts.Registers {
					if strings.EqualFold(reg.Name, regName) {
						return buildVTRefDimInfos(append(reg.Dimensions, reg.Attributes...), opts.Entities)
					}
				}
			} else if isInfoRegType(upper) {
				for _, ir := range opts.InfoRegs {
					if strings.EqualFold(ir.Name, regName) {
						return buildVTRefDimInfos(ir.Dimensions, opts.Entities)
					}
				}
			}
			return nil
		}
		// Regular source
		if isAccumRegType(upper) {
			for _, reg := range opts.Registers {
				if strings.EqualFold(reg.Name, regName) {
					// С entities, чтобы refIsDoc выставился: измерение-ссылка на
					// документ должно отображаться через .номер, а не .наименование.
					return buildRefDimInfosWithEntities(append(reg.Dimensions, reg.Attributes...), opts.Entities)
				}
			}
		} else if isInfoRegType(upper) {
			for _, ir := range opts.InfoRegs {
				if strings.EqualFold(ir.Name, regName) {
					return buildRefDimInfosWithEntities(ir.Dimensions, opts.Entities)
				}
			}
		}
		// Document / Catalog sources
		for _, ent := range opts.Entities {
			if strings.EqualFold(ent.Name, regName) {
				return buildRefDimInfosWithEntities(ent.Fields, opts.Entities)
			}
		}
	}
	return nil
}

// filterUsedRefDims removes refDims for fields not referenced in the query tokens.
// This avoids unnecessary JOINs that can cause "ambiguous column name" errors when
// a joined table shares column names (e.g. дата) with the main table.
func filterUsedRefDims(dims []refDimInfo, tokens []tok) []refDimInfo {
	if len(dims) == 0 {
		return nil
	}
	used := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		if t.kind == tIdent {
			used[strings.ToLower(t.val)] = true
		}
	}
	var out []refDimInfo
	for _, rd := range dims {
		if used[rd.fieldName] {
			out = append(out, rd)
		}
	}
	return out
}

// buildVTRefDimInfos creates refDimInfos for VT outer queries where the subquery
// aliases _id columns to logical names, so JOIN ON uses fieldName instead of idCol.
// entities позволяет выставить refIsDoc: ссылка-измерение на документ отображается
// через .номер, а не .наименование (у документов нет наименования).
func buildVTRefDimInfos(dims []metadata.Field, entities []*metadata.Entity) []refDimInfo {
	var result []refDimInfo
	for _, d := range dims {
		if d.RefEntity != "" {
			fn := strings.ToLower(d.Name)
			rd := refDimInfo{
				fieldName:  fn,
				idCol:      fn, // VT aliased from _id
				joinAlias:  "ref_" + fn,
				joinTable:  strings.ToLower(d.RefEntity),
				isVT:       true,
				refEntity:  d.RefEntity,
				refSrcType: "СПРАВОЧНИК",
			}
			for _, e := range entities {
				if strings.EqualFold(e.Name, d.RefEntity) {
					rd.refEntity = e.Name // оригинальный регистр имени из метаданных
					if e.Kind == metadata.KindDocument {
						rd.refIsDoc = true
						rd.refSrcType = "ДОКУМЕНТ"
					}
					break
				}
			}
			result = append(result, rd)
		}
	}
	return result
}

func buildRefDimInfosWithEntities(dims []metadata.Field, entities []*metadata.Entity) []refDimInfo {
	var result []refDimInfo
	for _, d := range dims {
		if d.RefEntity != "" {
			rd := refDimInfo{
				fieldName:  strings.ToLower(d.Name),
				idCol:      strings.ToLower(d.Name) + "_id",
				joinAlias:  "ref_" + strings.ToLower(d.Name),
				joinTable:  strings.ToLower(d.RefEntity),
				refEntity:  d.RefEntity,
				refSrcType: "СПРАВОЧНИК",
			}
			for _, e := range entities {
				if strings.EqualFold(e.Name, d.RefEntity) {
					rd.refEntity = e.Name // оригинальный регистр имени из метаданных
					if e.Kind == metadata.KindDocument {
						rd.refIsDoc = true
						rd.refSrcType = "ДОКУМЕНТ"
					}
					break
				}
			}
			result = append(result, rd)
		}
	}
	return result
}

func (tr *translator) findRefDim(name string) *refDimInfo {
	for i := range tr.refDims {
		if tr.refDims[i].fieldName == name {
			return &tr.refDims[i]
		}
	}
	return nil
}

// emitVTSubquery emits a VT subquery with its alias, detects optional user alias (КАК),
// and adds auto-JOINs for reference dimensions using the correct alias.
func (tr *translator) emitVTSubquery(subq, defaultAlias string) error {
	alias := defaultAlias
	if p := tr.peek(0); p.kind == tIdent {
		pUpper := strings.ToUpper(p.val)
		if pUpper == "КАК" || pUpper == "AS" {
			tr.advance()
			if a := tr.peek(0); a.kind == tIdent {
				alias = strings.ToLower(tr.advance().val)
			}
		}
	}
	tr.emit("(" + subq + ") AS " + alias)
	// Авто-JOIN refDims — только для главной таблицы (первого источника FROM),
	// иначе при присоединении через явный JOIN авто-JOIN вклинивается перед ON
	// присоединяемого источника (план 39, п.50).
	if tr.section == sectionFrom && !tr.mainEmitted {
		for _, rd := range tr.refDims {
			if rd.isVT {
				joinCond := fmt.Sprintf("%s.id = %s.%s", rd.joinAlias, alias, rd.fieldName)
				if s, err := tr.rowFilterCondition(sourcePermKind(rd.refSrcType), rd.refEntity, tr.predicateEntityForSource(rd.refSrcType, rd.refEntity), rd.joinAlias); err != nil {
					return err
				} else if s != "" {
					joinCond += " AND " + s
				}
				tr.emit(fmt.Sprintf("LEFT JOIN %s %s ON %s", rd.joinTable, rd.joinAlias, joinCond))
				// #14: связанная сущность ссылочного измерения VT — источник RBAC.
				tr.addRefSource(rd)
			}
		}
	}
	tr.mainEmitted = true
	return nil
}

// --- main translator loop ---

// buildColMap creates a mapping from lowercase field name to actual DB column name
// for reference-type fields. It scans tokens to find the specific source register
// so that reference dims from one register don't pollute queries against another.
func buildColMap(tokens []tok, opts CompileOpts) map[string]string {
	m := map[string]string{}
	addFields := func(fields []metadata.Field) {
		for _, f := range fields {
			name := strings.ToLower(f.Name)
			col := metadata.ColumnName(f)
			if col != name {
				m[name] = col
			}
		}
	}

	// Find the specific register being queried (same scan as preScanRefDims).
	vtSourceFound := false
	for i := 0; i+2 < len(tokens); i++ {
		t := tokens[i]
		if t.kind != tIdent {
			continue
		}
		upper := strings.ToUpper(t.val)
		if !isSourceType(upper) || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		if i+3 < len(tokens) && tokens[i+3].kind == tDot {
			vtSourceFound = true
			continue // skip virtual tables
		}
		regName := tokens[i+2].val
		if isAccumRegType(upper) {
			for _, reg := range opts.Registers {
				if strings.EqualFold(reg.Name, regName) {
					addFields(reg.Dimensions)
					addFields(reg.Resources)
					addFields(reg.Attributes)
					return m
				}
			}
		} else if isInfoRegType(upper) {
			for _, ir := range opts.InfoRegs {
				if strings.EqualFold(ir.Name, regName) {
					addFields(ir.Dimensions)
					addFields(ir.Resources)
					return m
				}
			}
		}
		// Found a source but not a register — stop searching (entity query needs no colMap)
		return m
	}

	// VT query: the subquery already aliases _id cols to DSL names, so the outer
	// query must reference those logical names — return empty colMap.
	if vtSourceFound {
		return m
	}

	// Fallback: no explicit register source — build from all (rare, backward-compat)
	for _, reg := range opts.Registers {
		addFields(reg.Dimensions)
		addFields(reg.Resources)
		addFields(reg.Attributes)
	}
	for _, ir := range opts.InfoRegs {
		addFields(ir.Dimensions)
		addFields(ir.Resources)
	}
	return m
}

// buildColTypes маппит имя поля (lowercase) → его тип для запрашиваемого
// источника (справочник/документ ИЛИ регистр). В отличие от buildColMap (только
// поля с переименованной колонкой), здесь — ВСЕ поля, чтобы:
//   - п.48: квалифицировать собственные колонки префиксом таблицы при авто-JOIN
//     (иначе одноимённая колонка присоединённого каталога даёт ambiguous column);
//   - п.49: на SQLite оборачивать number-колонки в CAST(... AS NUMERIC) в
//     сравнениях/сортировке (number хранится как TEXT → иначе строковое сравнение).
func buildColTypes(tokens []tok, opts CompileOpts) map[string]metadata.FieldType {
	m := map[string]metadata.FieldType{}
	add := func(fields []metadata.Field) {
		for _, f := range fields {
			m[strings.ToLower(f.Name)] = f.Type
		}
	}
	for i := 0; i+2 < len(tokens); i++ {
		t := tokens[i]
		if t.kind != tIdent {
			continue
		}
		upper := strings.ToUpper(t.val)
		if !isSourceType(upper) || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		if i+3 < len(tokens) && tokens[i+3].kind == tDot {
			return m // VT-источник: внешний запрос работает по логическим алиасам
		}
		name := tokens[i+2].val
		switch {
		case isAccumRegType(upper):
			for _, reg := range opts.Registers {
				if strings.EqualFold(reg.Name, name) {
					add(reg.Dimensions)
					add(reg.Resources)
					add(reg.Attributes)
					return m
				}
			}
		case isInfoRegType(upper):
			for _, ir := range opts.InfoRegs {
				if strings.EqualFold(ir.Name, name) {
					add(ir.Dimensions)
					add(ir.Resources)
					return m
				}
			}
		default: // справочник / документ
			for _, e := range opts.Entities {
				if strings.EqualFold(e.Name, name) {
					add(e.Fields)
					return m
				}
			}
		}
		return m
	}
	return m
}

// needsNumberCast — true, если колонку поля number нужно обернуть в
// CAST(... AS NUMERIC): только на SQLite (там number хранится как TEXT) и только
// в позициях сравнения/сортировки (WHERE/HAVING/ORDER BY). В ВЫБРАТЬ не кастим —
// вывод остаётся точным TEXT.
func (tr *translator) needsNumberCast(lower string) bool {
	if _, isAlias := tr.aliases[lower]; isAlias {
		return false // ссылка на алиас вывода (КАК ...), а не сырая колонка
	}
	if tr.colTypes[lower] != metadata.FieldTypeNumber {
		return false
	}
	if dialectOrDefault(tr.opts.Dialect).Name() != "sqlite" {
		return false
	}
	switch tr.section {
	case sectionWhere, sectionHaving, sectionOrderBy:
		return true
	}
	return false
}

// sqliteDateParamComparedToField распознаёт time.Time-параметр в прямом
// сравнении с полем date. Такие параметры сериализуются тем же UTC RFC3339,
// что и значения полей сущностей: иначе SQLite сравнивает разные TEXT-форматы
// лексикографически по разделителю ('T' против пробела) и ошибается (#462).
func (tr *translator) sqliteDateParamComparedToField(idx int) bool {
	if dialectOrDefault(tr.opts.Dialect).Name() != "sqlite" || !tr.timeParamAt(idx) {
		return false
	}
	if tr.dateFieldAt(idx-2) && tr.comparisonOpAt(idx-1) {
		return true
	}
	if tr.comparisonOpAt(idx+1) && tr.dateFieldAt(idx+2) {
		return true
	}
	// Первый или второй параметр правой части BETWEEN.
	if tr.dateFieldAt(idx-2) && tr.keywordAt(idx-1, "МЕЖДУ", "BETWEEN") {
		return true
	}
	return tr.dateFieldAt(idx-4) &&
		tr.keywordAt(idx-3, "МЕЖДУ", "BETWEEN") &&
		tr.timeParamAt(idx-2) &&
		tr.keywordAt(idx-1, "И", "AND")
}

func (tr *translator) addSQLiteDateParam(name string) string {
	var value any
	switch v := tr.paramValues[name].(type) {
	case time.Time:
		value = v.UTC().Format(time.RFC3339)
	case *time.Time:
		if v != nil {
			value = v.UTC().Format(time.RFC3339)
		}
	}
	tr.args = append(tr.args, value)
	return dialectOrDefault(tr.opts.Dialect).Placeholder(len(tr.args))
}

func (tr *translator) dateFieldAt(idx int) bool {
	if idx < 0 || idx >= len(tr.tokens) || tr.tokens[idx].kind != tIdent {
		return false
	}
	return tr.colTypes[strings.ToLower(tr.tokens[idx].val)] == metadata.FieldTypeDate
}

func (tr *translator) timeParamAt(idx int) bool {
	if idx < 0 || idx >= len(tr.tokens) || tr.tokens[idx].kind != tParam {
		return false
	}
	switch tr.paramValues[tr.tokens[idx].val].(type) {
	case time.Time, *time.Time:
		return true
	default:
		return false
	}
}

func (tr *translator) comparisonOpAt(idx int) bool {
	if idx < 0 || idx >= len(tr.tokens) || tr.tokens[idx].kind != tOp {
		return false
	}
	switch tr.tokens[idx].val {
	case "=", "<>", "!=", "<", "<=", ">", ">=":
		return true
	default:
		return false
	}
}

func (tr *translator) keywordAt(idx int, names ...string) bool {
	if idx < 0 || idx >= len(tr.tokens) || tr.tokens[idx].kind != tIdent {
		return false
	}
	got := strings.ToUpper(tr.tokens[idx].val)
	for _, name := range names {
		if got == name {
			return true
		}
	}
	return false
}

// qualifyOwn префиксует собственную колонку основной таблицы её именем/алиасом,
// когда активны авто-JOIN'ы (п.48) — иначе одноимённая колонка присоединённого
// каталога вызывает ambiguous column. Неизвестные идентификаторы не трогаем.
func (tr *translator) qualifyOwn(col, lower string) string {
	if _, isAlias := tr.aliases[lower]; isAlias {
		return col // алиас вывода, не колонка таблицы
	}
	if len(tr.refDims) > 0 && tr.mainTable != "" {
		_, own := tr.colTypes[lower]
		if own {
			return tr.mainTable + "." + col
		}
	}
	return col
}

// qualifyReference квалифицирует виртуальное поле Ссылка/Reference/Ref по
// основному источнику именно текущего SELECT-scope. Выходные алиасы обрабатываются
// отдельно: глобальная tr.aliases не должна влиять на вложенные/UNION SELECT.
func (tr *translator) qualifyReference(col string) string {
	if tr.inUnionOrder() {
		return col
	}
	scope, ok := tr.sourceCtx.scopeAt(tr.pos - 1)
	if !ok || scope.mainTable == "" {
		return col
	}
	if len(tr.refDims) == 0 && scope.sourceCount < 2 {
		return col
	}
	return scope.mainTable + "." + col
}

func (tr *translator) inUnionOrder() bool {
	for depth := tr.parenDepth; depth >= 0; depth-- {
		if tr.unionOrders[depth] {
			return true
		}
	}
	return false
}

func isReferenceName(lower string) bool {
	return lower == "ссылка" || lower == "reference" || lower == "ref"
}

// emitOwnColumn эмитит неквалифицированную собственную колонку с учётом п.48/п.49.
func (tr *translator) emitOwnColumn(col, lower string) {
	col = tr.qualifyOwn(col, lower)
	if tr.needsNumberCast(lower) {
		tr.emit("CAST(" + col + " AS NUMERIC)")
		return
	}
	tr.emit(col)
}

// emitQualifiedColumn эмитит колонку после точки (алиас.поле). Для number на
// SQLite оборачивает весь `алиас.поле` в CAST, забирая уже эмитнутые алиас и "."
// из tr.parts (build() не ставит пробелов вокруг точки).
func (tr *translator) emitQualifiedColumn(col, lower string) {
	if tr.needsNumberCast(lower) && len(tr.parts) >= 2 && tr.parts[len(tr.parts)-1] == "." {
		alias := tr.parts[len(tr.parts)-2]
		tr.parts = tr.parts[:len(tr.parts)-2]
		tr.emit("CAST(" + alias + "." + col + " AS NUMERIC)")
		return
	}
	tr.emit(col)
}

// preScanMainTable заранее (до основного прохода) находит имя основной таблицы
// или её алиас (КАК/AS). Нужно для п.48: колонки секции ВЫБРАТЬ эмитятся раньше
// ИЗ, поэтому без пред-скана mainTable там ещё пуст и квалификация не сработала бы.
// Для VT-источника возвращает "" (внешний запрос работает по логическим алиасам).
func preScanMainTable(tokens []tok) string {
	for i := 0; i+2 < len(tokens); i++ {
		t := tokens[i]
		if t.kind != tIdent {
			continue
		}
		upper := strings.ToUpper(t.val)
		if !isSourceType(upper) || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		if i+3 < len(tokens) && tokens[i+3].kind == tDot {
			return ""
		}
		table := sourceToTable(upper, tokens[i+2].val)
		if j := i + 3; j+1 < len(tokens) && tokens[j].kind == tIdent {
			if u := strings.ToUpper(tokens[j].val); u == "КАК" || u == "AS" {
				if tokens[j+1].kind == tIdent {
					return strings.ToLower(tokens[j+1].val)
				}
			}
		}
		return table
	}
	return ""
}

// preScanSourceContext определяет по каждому SELECT-scope, какие квалификаторы
// относятся к регистрам, а какие — к документам/справочникам. Это нужно до
// основного прохода, потому что SELECT транслируется раньше FROM, а русские
// имена системных колонок регистра (Период, Регистратор, ...) совпадают с
// допустимыми прикладными полями сущностей. Scope привязан к токенам, а не
// только к глубине скобок: Период внутри Год(Период) остаётся в родительском
// SELECT, а SELECT-подзапрос получает собственный main/aliases.
func preScanSourceContext(tokens []tok) sourceContext {
	ctx := sourceContext{
		tokenScope:   make([]int, len(tokens)),
		tokenSection: make([]querySection, len(tokens)),
		tokenDepth:   make([]int, len(tokens)),
	}
	for i := range ctx.tokenScope {
		ctx.tokenScope[i] = -1
		ctx.tokenSection[i] = sectionOther
	}
	type scopeFrame struct {
		id    int
		depth int
	}
	var active []scopeFrame
	var sections []querySection
	depth := 0

	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		ctx.tokenDepth[i] = depth
		if t.kind == tIdent {
			if kw, ok := sqlKW(t.val); ok && kw == "SELECT" {
				// UNION starts a sibling SELECT at the same depth; nested SELECT
				// keeps every shallower parent frame active.
				for len(active) > 0 && active[len(active)-1].depth >= depth {
					active = active[:len(active)-1]
				}
				scopeID := len(ctx.scopes)
				ctx.scopes = append(ctx.scopes, sourceScope{
					qualifiers:     map[string]sourceClass{},
					derivedAliases: map[string]int{},
					refAliases:     map[string]struct{}{},
				})
				sections = append(sections, sectionSelect)
				active = append(active, scopeFrame{id: scopeID, depth: depth})
			}
		}
		if len(active) > 0 {
			frame := active[len(active)-1]
			scopeID := frame.id
			// EXTRACT(YEAR FROM ...) содержит SQL-слово FROM внутри выражения.
			// Секцию SELECT меняют только ключевые слова на глубине самого SELECT.
			if t.kind == tIdent && depth == frame.depth {
				if kw, ok := sqlKW(t.val); ok {
					switch kw {
					case "SELECT":
						sections[scopeID] = sectionSelect
					case "FROM":
						sections[scopeID] = sectionFrom
					case "WHERE":
						sections[scopeID] = sectionWhere
					case "GROUP":
						sections[scopeID] = sectionGroupBy
					case "ORDER":
						sections[scopeID] = sectionOrderBy
					case "HAVING":
						sections[scopeID] = sectionHaving
					}
				}
			}
			ctx.tokenScope[i] = scopeID
			ctx.tokenSection[i] = sections[scopeID]
			if sections[scopeID] == sectionSelect && t.kind == tIdent && depth == frame.depth {
				if up := strings.ToUpper(t.val); (up == "КАК" || up == "AS") &&
					i+1 < len(tokens) && tokens[i+1].kind == tIdent {
					alias := strings.ToLower(tokens[i+1].val)
					if isReferenceName(alias) {
						ctx.scopes[scopeID].refAliases[alias] = struct{}{}
					}
				}
			}
		}

		// Производная таблица в FROM/JOIN сама является источником текущего
		// SELECT. Без этого следующий обычный JOIN ошибочно становился mainTable.
		if t.kind == tIdent && len(active) > 0 {
			if kw, ok := sqlKW(t.val); ok && (kw == "FROM" || kw == "JOIN") &&
				i+2 < len(tokens) && tokens[i+1].kind == tLParen &&
				tokens[i+2].kind == tIdent {
				if nestedKW, nested := sqlKW(tokens[i+2].val); nested && nestedKW == "SELECT" {
					scope := &ctx.scopes[active[len(active)-1].id]
					isMain := scope.sourceCount == 0
					scope.sourceCount++
					nesting := 0
					for j := i + 1; j < len(tokens); j++ {
						switch tokens[j].kind {
						case tLParen:
							nesting++
						case tRParen:
							nesting--
							if nesting == 0 {
								aliasPos := j + 1
								if aliasPos+1 < len(tokens) && tokens[aliasPos].kind == tIdent {
									aliasUpper := strings.ToUpper(tokens[aliasPos].val)
									if (aliasUpper == "КАК" || aliasUpper == "AS") && tokens[aliasPos+1].kind == tIdent {
										alias := strings.ToLower(tokens[aliasPos+1].val)
										if isMain {
											scope.mainTable = alias
										}
									}
								}
								j = len(tokens)
							}
						}
					}
				}
			}
		}

		if i+2 >= len(tokens) {
			switch t.kind {
			case tLParen:
				depth++
			case tRParen:
				for len(active) > 0 && active[len(active)-1].depth >= depth {
					active = active[:len(active)-1]
				}
				if depth > 0 {
					depth--
				}
			}
			continue
		}
		if tokens[i].kind != tIdent {
			switch t.kind {
			case tLParen:
				depth++
			case tRParen:
				for len(active) > 0 && active[len(active)-1].depth >= depth {
					active = active[:len(active)-1]
				}
				if depth > 0 {
					depth--
				}
			}
			continue
		}
		typeUpper := strings.ToUpper(tokens[i].val)
		if len(active) == 0 || !isSourceType(typeUpper) || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		scope := &ctx.scopes[active[len(active)-1].id]
		isMain := scope.sourceCount == 0
		scope.sourceCount++

		class := sourceClassEntity
		if isAccumRegType(typeUpper) || isInfoRegType(typeUpper) || isAccountRegType(typeUpper) {
			class = sourceClassRegister
		}
		if scope.main == sourceClassUnknown {
			scope.main = class
		}
		if isMain {
			// Виртуальная таблица эмитится как подзапрос со специальным алиасом,
			// который здесь не вычисляем. Для обычного источника сохраняем имя,
			// чтобы bare-Ссылка квалифицировалась в своём SELECT-scope.
			if i+3 >= len(tokens) || tokens[i+3].kind != tDot {
				scope.mainTable = sourceToTable(typeUpper, tokens[i+2].val)
			}
		}

		entityName := strings.ToLower(tokens[i+2].val)
		scope.qualifiers[entityName] = class
		scope.qualifiers[sourceToTable(typeUpper, tokens[i+2].val)] = class

		// У обычного источника КАК/AS следует сразу за именем сущности.
		aliasPos := i + 3
		if aliasPos+1 < len(tokens) && tokens[aliasPos].kind == tIdent {
			aliasUpper := strings.ToUpper(tokens[aliasPos].val)
			if (aliasUpper == "КАК" || aliasUpper == "AS") && tokens[aliasPos+1].kind == tIdent {
				alias := strings.ToLower(tokens[aliasPos+1].val)
				scope.qualifiers[alias] = class
				if isMain {
					scope.mainTable = alias
				}
				continue
			}
		}

		// У виртуальной таблицы пользовательский алиас находится после списка
		// аргументов: Регистр.X.Остатки(...) КАК Р.
		if i+5 >= len(tokens) || tokens[i+3].kind != tDot || tokens[i+5].kind != tLParen {
			continue
		}
		depth := 0
		for j := i + 5; j < len(tokens); j++ {
			switch tokens[j].kind {
			case tLParen:
				depth++
			case tRParen:
				depth--
				if depth == 0 {
					aliasPos = j + 1
					if aliasPos+1 < len(tokens) && tokens[aliasPos].kind == tIdent {
						aliasUpper := strings.ToUpper(tokens[aliasPos].val)
						if (aliasUpper == "КАК" || aliasUpper == "AS") && tokens[aliasPos+1].kind == tIdent {
							alias := strings.ToLower(tokens[aliasPos+1].val)
							scope.qualifiers[alias] = class
							if isMain {
								scope.mainTable = alias
							}
						}
					}
					j = len(tokens)
				}
			}
		}
	}
	linkDerivedSourceScopes(tokens, &ctx)
	return ctx
}

func linkDerivedSourceScopes(tokens []tok, ctx *sourceContext) {
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].kind != tIdent || tokens[i+1].kind != tLParen || tokens[i+2].kind != tIdent {
			continue
		}
		kw, ok := sqlKW(tokens[i].val)
		if !ok || (kw != "FROM" && kw != "JOIN") {
			continue
		}
		nestedKW, ok := sqlKW(tokens[i+2].val)
		if !ok || nestedKW != "SELECT" {
			continue
		}
		parentID, parentOK := ctx.scopeIDAt(i)
		childID, childOK := ctx.scopeIDAt(i + 2)
		if !parentOK || !childOK || parentID == childID {
			continue
		}

		nesting := 0
		for j := i + 1; j < len(tokens); j++ {
			switch tokens[j].kind {
			case tLParen:
				nesting++
			case tRParen:
				nesting--
				if nesting != 0 {
					continue
				}
				aliasPos := j + 1
				if aliasPos+1 < len(tokens) && tokens[aliasPos].kind == tIdent {
					aliasKW, aliasOK := sqlKW(tokens[aliasPos].val)
					if aliasOK && aliasKW == "AS" && tokens[aliasPos+1].kind == tIdent {
						alias := strings.ToLower(tokens[aliasPos+1].val)
						ctx.scopes[parentID].derivedAliases[alias] = childID
					}
				}
				j = len(tokens)
			}
		}
	}
}

func (ctx sourceContext) scopeProjectsSystemColumn(tokens []tok, scopeID int, name string, seen map[int]bool) bool {
	if scopeID < 0 || scopeID >= len(ctx.scopes) || seen[scopeID] {
		return false
	}
	seen[scopeID] = true
	defer delete(seen, scopeID)

	selectPos := -1
	baseDepth := 0
	for i, t := range tokens {
		id, ok := ctx.scopeIDAt(i)
		if !ok || id != scopeID || t.kind != tIdent {
			continue
		}
		if kw, ok := sqlKW(t.val); ok && kw == "SELECT" {
			selectPos = i
			baseDepth = ctx.tokenDepth[i]
			break
		}
	}
	if selectPos < 0 {
		return false
	}

	end := len(tokens)
	for i := selectPos + 1; i < len(tokens); i++ {
		id, ok := ctx.scopeIDAt(i)
		if !ok || id != scopeID || ctx.tokenDepth[i] != baseDepth || tokens[i].kind != tIdent {
			continue
		}
		if kw, ok := sqlKW(tokens[i].val); ok && kw == "FROM" {
			end = i
			break
		}
	}

	exprStart := selectPos + 1
	for i := exprStart; i <= end; i++ {
		if i < end && (tokens[i].kind != tComma || ctx.tokenDepth[i] != baseDepth) {
			continue
		}
		if ctx.projectionIsSystemColumn(tokens, scopeID, baseDepth, exprStart, i, name, seen) {
			return true
		}
		exprStart = i + 1
	}
	return false
}

func (ctx sourceContext) projectionIsSystemColumn(
	tokens []tok,
	scopeID, baseDepth, start, end int,
	name string,
	seen map[int]bool,
) bool {
	for start < end && tokens[start].kind == tIdent {
		if kw, ok := sqlKW(tokens[start].val); ok && (kw == "DISTINCT" || kw == "ALL") {
			start++
			continue
		}
		break
	}
	if start >= end {
		return false
	}

	for i := start; i+1 < end; i++ {
		id, ok := ctx.scopeIDAt(i)
		if !ok || id != scopeID || ctx.tokenDepth[i] != baseDepth || tokens[i].kind != tIdent {
			continue
		}
		if kw, ok := sqlKW(tokens[i].val); ok && kw == "AS" && tokens[i+1].kind == tIdent {
			if !strings.EqualFold(tokens[i+1].val, name) {
				return false
			}
			return ctx.systemColumnIdentifierAt(tokens, i+1, seen)
		}
	}

	start, end = trimProjectionParentheses(tokens, start, end)
	if end-start == 1 && tokens[start].kind == tStar {
		scope := ctx.scopes[scopeID]
		if childID, derived := scope.derivedAliases[scope.mainTable]; derived {
			return ctx.scopeProjectsSystemColumn(tokens, childID, name, seen)
		}
		return scope.main == sourceClassRegister
	}
	if end-start == 3 && tokens[start].kind == tIdent && tokens[start+1].kind == tDot &&
		tokens[start+2].kind == tStar {
		scope := ctx.scopes[scopeID]
		qualifier := strings.ToLower(tokens[start].val)
		if class, known := scope.qualifiers[qualifier]; known {
			return class == sourceClassRegister
		}
		if childID, derived := scope.derivedAliases[qualifier]; derived {
			return ctx.scopeProjectsSystemColumn(tokens, childID, name, seen)
		}
		return false
	}
	if end-start == 1 && tokens[start].kind == tIdent && strings.EqualFold(tokens[start].val, name) {
		return ctx.systemColumnIdentifierAt(tokens, start, seen)
	}
	if end-start == 3 && tokens[start].kind == tIdent && tokens[start+1].kind == tDot &&
		tokens[start+2].kind == tIdent && strings.EqualFold(tokens[start+2].val, name) {
		return ctx.systemColumnIdentifierAt(tokens, start+2, seen)
	}
	return false
}

func trimProjectionParentheses(tokens []tok, start, end int) (int, int) {
	for end-start >= 2 && tokens[start].kind == tLParen && tokens[end-1].kind == tRParen {
		nesting := 0
		match := -1
		for i := start; i < end; i++ {
			switch tokens[i].kind {
			case tLParen:
				nesting++
			case tRParen:
				nesting--
				if nesting == 0 {
					match = i
					i = end
				}
			}
		}
		if match != end-1 {
			break
		}
		start++
		end--
	}
	return start, end
}

func (ctx sourceContext) systemColumnIdentifierAt(tokens []tok, tokenPos int, seen map[int]bool) bool {
	if tokenPos < 0 || tokenPos >= len(tokens) || tokens[tokenPos].kind != tIdent {
		return false
	}
	name := tokens[tokenPos].val
	if _, ok := systemColAlias(name); !ok {
		return false
	}
	scopeID, ok := ctx.scopeIDAt(tokenPos)
	if !ok {
		return false
	}
	scope := ctx.scopes[scopeID]
	if tokenPos >= 2 && tokens[tokenPos-1].kind == tDot {
		qualifier := strings.ToLower(tokens[tokenPos-2].val)
		if class, known := scope.qualifiers[qualifier]; known {
			return class == sourceClassRegister
		}
		if childID, derived := scope.derivedAliases[qualifier]; derived {
			return ctx.scopeProjectsSystemColumn(tokens, childID, name, seen)
		}
		return false
	}
	if childID, derived := scope.derivedAliases[scope.mainTable]; derived {
		return ctx.scopeProjectsSystemColumn(tokens, childID, name, seen)
	}
	return scope.main == sourceClassRegister
}

// rewriteGroupingReferenceAliases разворачивает зарезервированный выходной
// алиас Ссылка/Reference/Ref обратно в выражение SELECT при обращении из
// GROUP BY/HAVING. PostgreSQL не разрешает SELECT-алиасы в HAVING, а при
// авто-JOIN оба диалекта могут трактовать id как неоднозначную входную колонку.
// ORDER BY оставляем без изменений: там выходной алиас имеет нужный приоритет.
func rewriteGroupingReferenceAliases(tokens []tok) []tok {
	ctx := preScanSourceContext(tokens)
	expressions := make(map[int][]tok)

	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].kind != tIdent || tokens[i+1].kind != tIdent {
			continue
		}
		kw, ok := sqlKW(tokens[i].val)
		if !ok || kw != "AS" || !isReferenceName(strings.ToLower(tokens[i+1].val)) {
			continue
		}
		scopeID, ok := ctx.scopeIDAt(i)
		if !ok || ctx.sectionAt(i) != sectionSelect {
			continue
		}
		start := selectExpressionStart(tokens, ctx, scopeID, i)
		if start >= i {
			continue
		}
		expressions[scopeID] = copyGroupingAliasExpression(tokens, ctx, start, i)
	}
	if len(expressions) == 0 {
		return tokens
	}

	out := make([]tok, 0, len(tokens))
	for i, t := range tokens {
		section := ctx.sectionAt(i)
		if t.kind == tIdent && isReferenceName(strings.ToLower(t.val)) &&
			(section == sectionGroupBy || section == sectionHaving) &&
			(i == 0 || tokens[i-1].kind != tDot) {
			if scopeID, ok := ctx.scopeIDAt(i); ok {
				if expr := expressions[scopeID]; len(expr) > 0 {
					out = append(out, tok{kind: tLParen, val: "("})
					out = append(out, expr...)
					out = append(out, tok{kind: tRParen, val: ")"})
					continue
				}
			}
		}
		out = append(out, t)
	}
	return out
}

func selectExpressionStart(tokens []tok, ctx sourceContext, scopeID, aliasPos int) int {
	nesting := 0
	for i := aliasPos - 1; i >= 0; i-- {
		switch tokens[i].kind {
		case tRParen:
			nesting++
			continue
		case tLParen:
			if nesting > 0 {
				nesting--
				continue
			}
		}
		if nesting != 0 {
			continue
		}
		if id, ok := ctx.scopeIDAt(i); !ok || id != scopeID {
			continue
		}
		if tokens[i].kind == tComma {
			return i + 1
		}
		if tokens[i].kind == tIdent {
			if kw, ok := sqlKW(tokens[i].val); ok && kw == "SELECT" {
				start := i + 1
				if start < aliasPos && tokens[start].kind == tIdent {
					if kw, ok := sqlKW(tokens[start].val); ok && (kw == "DISTINCT" || kw == "ALL") {
						start++
					}
				}
				return start
			}
		}
	}
	return aliasPos
}

func copyGroupingAliasExpression(tokens []tok, ctx sourceContext, start, end int) []tok {
	expr := make([]tok, 0, end-start)
	for i := start; i < end; i++ {
		t := tokens[i]
		if t.kind == tIdent && isReferenceName(strings.ToLower(t.val)) &&
			(i == start || tokens[i-1].kind != tDot) {
			prevAlias := i > start && tokens[i-1].kind == tIdent &&
				(strings.EqualFold(tokens[i-1].val, "КАК") || strings.EqualFold(tokens[i-1].val, "AS"))
			if !prevAlias && !ctx.isReferenceAliasAt(i, strings.ToLower(t.val)) {
				if scope, ok := ctx.scopeAt(i); ok && scope.mainTable != "" {
					expr = append(expr,
						tok{kind: tIdent, val: scope.mainTable},
						tok{kind: tDot, val: "."},
					)
				}
			}
		}
		expr = append(expr, t)
	}
	return expr
}

// systemColumnAlias разрешает русское имя системной колонки только в контексте
// регистра. Для документов и справочников такое имя является обычным
// прикладным полем и должно остаться кириллическим.
func (tr *translator) systemColumnAlias(name string, prevDot bool) (string, bool) {
	col, ok := systemColAlias(name)
	if !ok {
		return "", false
	}
	scope, hasScope := tr.sourceCtx.scopeAt(tr.pos - 1)
	if !hasScope {
		return "", false
	}
	if prevDot && tr.pos >= 3 {
		qualifier := strings.ToLower(tr.tokens[tr.pos-3].val)
		if class, known := scope.qualifiers[qualifier]; known {
			return col, class == sourceClassRegister
		}
		if childID, derived := scope.derivedAliases[qualifier]; derived {
			return col, tr.sourceCtx.scopeProjectsSystemColumn(
				tr.tokens,
				childID,
				name,
				map[int]bool{},
			)
		}
		// Обращение через ссылочное поле регистра ведёт к документу или
		// справочнику, поэтому Регистр.Документ.Период — не системный period.
		if tr.findRefDim(qualifier) != nil {
			return "", false
		}
	}
	if childID, derived := scope.derivedAliases[scope.mainTable]; derived {
		return col, tr.sourceCtx.scopeProjectsSystemColumn(
			tr.tokens,
			childID,
			name,
			map[int]bool{},
		)
	}
	return col, scope.main == sourceClassRegister
}

// projectionFieldNames extracts identifiers used by SELECT expressions before
// aliases are applied. It deliberately errs on the safe side: qualifiers and
// otherwise harmless identifiers may be included, but an underlying field must
// never be omitted merely because it is wrapped in an expression or renamed.
func projectionFieldNames(tokens []tok) []string {
	type selectFrame struct {
		depth  int
		active bool
	}
	var frames []selectFrame
	depth := 0
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, name)
	}
	projectionActive := func() bool {
		for i := len(frames) - 1; i >= 0; i-- {
			if frames[i].active {
				return true
			}
		}
		return false
	}

	for i, t := range tokens {
		switch t.kind {
		case tLParen:
			depth++
			continue
		case tRParen:
			for len(frames) > 0 && frames[len(frames)-1].depth >= depth {
				frames = frames[:len(frames)-1]
			}
			if depth > 0 {
				depth--
			}
			continue
		}
		if t.kind == tIdent {
			kw, isKW := sqlKW(t.val)
			switch kw {
			case "SELECT":
				frames = append(frames, selectFrame{depth: depth, active: true})
				continue
			case "FROM":
				for j := len(frames) - 1; j >= 0; j-- {
					if frames[j].depth == depth && frames[j].active {
						frames[j].active = false
						break
					}
				}
				continue
			}
			if !projectionActive() || isKW {
				continue
			}
			if i > 0 && tokens[i-1].kind == tIdent {
				prev := strings.ToUpper(tokens[i-1].val)
				if prev == "КАК" || prev == "AS" {
					continue // output alias, not a source field
				}
			}
			if i+1 < len(tokens) && tokens[i+1].kind == tLParen {
				continue // function name; its arguments are inspected separately
			}
			add(t.val)
			continue
		}
		if t.kind == tStar && projectionActive() {
			add("*")
		}
	}
	return out
}

func expandReferenceProjection(fields []string, dims []refDimInfo) []string {
	out := append([]string(nil), fields...)
	contains := func(name string) bool {
		for _, field := range fields {
			if strings.EqualFold(field, name) {
				return true
			}
		}
		return false
	}
	appendUnique := func(name string) {
		for _, field := range out {
			if strings.EqualFold(field, name) {
				return
			}
		}
		out = append(out, name)
	}
	for _, dim := range dims {
		if !contains(dim.fieldName) {
			continue
		}
		if dim.refIsDoc {
			appendUnique("Номер")
		} else {
			appendUnique("Наименование")
		}
	}
	return out
}

func translate(tokens []tok, opts CompileOpts) (Result, error) {
	if opts.Params == nil {
		opts.Params = map[string]any{}
	}
	projectionFields := projectionFieldNames(tokens)
	projectionPlan := analyzeProjection(tokens)
	tokens = rewriteGroupingReferenceAliases(tokens)
	// расширяем НачалоДня/Год/Месяц/ОКР/АБС/ЦЕЛ/... в SQL-эквиваленты
	// до основной трансляции, чтобы остальные шаги ничего не знали о них.
	tokens = rewriteScalarFuncs(tokens, dialectName(opts.Dialect))
	tokens = rewriteStrftime(tokens, dialectName(opts.Dialect))
	tr := &translator{
		tokens:      tokens,
		params:      map[string]int{},
		paramValues: opts.Params,
		opts:        opts,
		colMap:      buildColMap(tokens, opts),
		colTypes:    buildColTypes(tokens, opts),
		mainTable:   preScanMainTable(tokens),
		refDims:     preScanRefDims(tokens, opts),
		sourceCtx:   preScanSourceContext(tokens),
		aliases:     map[string]struct{}{},
		unionDepths: map[int]bool{},
		unionOrders: map[int]bool{},
		section:     sectionOther,
	}
	for {
		t := tr.peek(0)
		if t.kind == tEOF {
			break
		}
		upper := strings.ToUpper(t.val)

		// Source type: TypeName.EntityName[.VirtualTable(args)] → table or subquery
		if t.kind == tIdent && isSourceType(upper) &&
			tr.peek(1).kind == tDot && tr.peek(2).kind == tIdent {

			// Check for virtual table: TypeName.EntityName.VTName(...)
			if tr.peek(3).kind == tDot && tr.peek(4).kind == tIdent &&
				tr.peek(5).kind == tLParen {
				vt4Upper := strings.ToUpper(tr.peek(4).val)

				if vtKind, ok := accumVTKinds[vt4Upper]; ok && isAccumRegType(upper) {
					tr.advance() // TypeName
					tr.advance() // .
					regName := tr.advance().val
					tr.addSource(upper, regName)
					tr.advance() // .
					tr.advance() // VTName
					tr.advance() // (
					vtArgs := tr.parseVTArgs()
					subq, alias, err := tr.buildAccumVT(vtKind, regName, vtArgs)
					if err != nil {
						return Result{}, err
					}
					if err := tr.emitVTSubquery(subq, alias); err != nil {
						return Result{}, err
					}
					continue
				}

				if vtKind, ok := infoVTKinds[vt4Upper]; ok && isInfoRegType(upper) {
					tr.advance() // TypeName
					tr.advance() // .
					regName := tr.advance().val
					tr.addSource(upper, regName)
					tr.advance() // .
					tr.advance() // VTName
					tr.advance() // (
					vtArgs := tr.parseVTArgs()
					subq, alias, err := tr.buildInfoVT(vtKind, regName, vtArgs)
					if err != nil {
						return Result{}, err
					}
					if err := tr.emitVTSubquery(subq, alias); err != nil {
						return Result{}, err
					}
					continue
				}

				if vtKind, ok := accumVTKinds[vt4Upper]; ok && isAccountRegType(upper) {
					tr.advance() // TypeName
					tr.advance() // .
					regName := tr.advance().val
					tr.addSource(upper, regName)
					tr.advance() // .
					tr.advance() // VTName
					tr.advance() // (
					vtArgs := tr.parseVTArgs()
					subq, alias, err := tr.buildAccountVT(vtKind, regName, vtArgs)
					if err != nil {
						return Result{}, err
					}
					if err := tr.emitVTSubquery(subq, alias); err != nil {
						return Result{}, err
					}
					continue
				}
			}

			// п.47: известное имя VT после источника, но без "(" — иначе суффикс
			// молча уходит в имя физической таблицы и падает рантайм `no such table`.
			// Выдаём понятную ошибку компиляции вместо тихой поломки виджета.
			if tr.peek(3).kind == tDot && tr.peek(4).kind == tIdent && tr.peek(5).kind != tLParen {
				vtName := tr.peek(4).val
				vtU := strings.ToUpper(vtName)
				_, isAccumVT := accumVTKinds[vtU]
				_, isInfoVT := infoVTKinds[vtU]
				if isAccumVT || isInfoVT {
					return Result{}, i18nerr.Errorf("виртуальная таблица %q требует круглые скобки: .%s(...)", vtName, vtName)
				}
			}

			// Regular source: TypeName.EntityName → table_name [+ КАК alias] [+ auto-JOINs]
			tr.advance()
			tr.advance()
			entity := tr.advance()
			tr.addSource(upper, entity.val)
			tableName := sourceToTable(upper, entity.val)
			// Главная таблица — первый источник FROM. Присоединяемые через явный
			// JOIN (ЛЕВОЕ СОЕДИНЕНИЕ ... ПО ...) не должны перезаписывать mainTable
			// и не порождают повторных авто-JOIN'ов refDims (это поля главной
			// таблицы) — иначе авто-JOIN вклинивается между присоединяемой таблицей
			// и её ON, ломая SQL (план 39, п.50).
			isMain := !tr.mainEmitted
			if isMain {
				tr.mainTable = tableName
			}
			sourceAlias := tableName
			hasAlias := false
			// Consume optional КАК/AS alias before emitting the source. Joined
			// restricted sources are scoped as subqueries and need the final alias
			// up front.
			if p := tr.peek(0); p.kind == tIdent {
				pUpper := strings.ToUpper(p.val)
				if pUpper == "КАК" || pUpper == "AS" {
					tr.advance()
					if a := tr.peek(0); a.kind == tIdent {
						aliasName := strings.ToLower(tr.advance().val)
						sourceAlias = aliasName
						hasAlias = true
						// Собственные колонки квалифицируем алиасом, а не именем
						// таблицы (иначе `таблица.col` не совпадёт с `AS алиас`).
						if isMain {
							tr.mainTable = aliasName
						}
					}
				}
			}
			if !isMain || tr.parenDepth > 0 {
				filtered, ok, err := tr.rowFilteredSourceSQL(upper, entity.val, tableName, sourceAlias)
				if err != nil {
					return Result{}, err
				}
				if ok {
					tr.emit(filtered)
				} else {
					tr.emit(tableName)
					if hasAlias {
						tr.emit("AS " + sourceAlias)
					}
				}
			} else {
				tr.emit(tableName)
				if hasAlias {
					tr.emit("AS " + sourceAlias)
				}
				if err := tr.addPendingRowFilter(upper, entity.val, sourceAlias); err != nil {
					return Result{}, err
				}
			}
			if tr.section == sectionFrom && isMain {
				// ON ссылается на источник через tr.mainTable: это имя таблицы
				// либо её алиас (КАК р). Использование сырого tableName при
				// наличии алиаса давало `no such column: таблица.col`.
				for _, rd := range tr.refDims {
					joinCond := fmt.Sprintf("%s.id = %s.%s", rd.joinAlias, tr.mainTable, rd.idCol)
					if s, err := tr.rowFilterCondition(sourcePermKind(rd.refSrcType), rd.refEntity, tr.predicateEntityForSource(rd.refSrcType, rd.refEntity), rd.joinAlias); err != nil {
						return Result{}, err
					} else if s != "" {
						joinCond += " AND " + s
					}
					tr.emit(fmt.Sprintf("LEFT JOIN %s %s ON %s", rd.joinTable, rd.joinAlias, joinCond))
					// #14: авто-JOIN ссылочного поля читает наименование/номер
					// связанной сущности — регистрируем её как источник для RBAC,
					// иначе чтение через ссылку обходит проверку прав.
					tr.addRefSource(rd)
				}
			}
			tr.mainEmitted = true
			continue
		}

		// Multi-word: СГРУППИРОВАТЬ ПО / УПОРЯДОЧИТЬ ПО
		if t.kind == tIdent && (upper == "СГРУППИРОВАТЬ" || upper == "УПОРЯДОЧИТЬ") {
			if tr.parenDepth == 0 {
				if err := tr.emitPendingRowFiltersAsWhere(); err != nil {
					return Result{}, err
				}
			}
			tr.advance()
			if upper == "УПОРЯДОЧИТЬ" {
				if tr.unionDepths[tr.parenDepth] {
					tr.unionOrders[tr.parenDepth] = true
				}
				tr.section = sectionOrderBy
				tr.emit("ORDER BY")
			} else {
				tr.section = sectionGroupBy
				tr.emit("GROUP BY")
			}
			if tr.peek(0).kind == tIdent && strings.ToUpper(tr.peek(0).val) == "ПО" {
				tr.advance()
			}
			continue
		}

		// Parameter: &Name → $N or NULL
		if t.kind == tParam {
			tr.prevWasDot = false
			normalizeDate := tr.sqliteDateParamComparedToField(tr.pos)
			tr.advance()
			if normalizeDate {
				tr.emit(tr.addSQLiteDateParam(t.val))
			} else {
				tr.emit(tr.addParam(t.val))
			}
			continue
		}

		// String literal
		if t.kind == tStr {
			tr.prevWasDot = false
			tr.advance()
			tr.emit("'" + strings.ReplaceAll(t.val, "'", "''") + "'")
			continue
		}

		// Number / star / operator
		if t.kind == tNum || t.kind == tStar || t.kind == tOp {
			tr.prevWasDot = false
			tr.advance()
			tr.emit(t.val)
			continue
		}

		// Punctuation
		if t.kind == tComma || t.kind == tLParen || t.kind == tRParen {
			tr.prevWasDot = false
			// Считаем глубину подзапросов/группировок: источник ИЗ, встреченный
			// при parenDepth>0, лежит внутри подзапроса, а не в верхнем FROM.
			// (Скобки виртуальных таблиц регистров съедает parseVTArgs — сюда не
			// попадают, баланс не нарушают.)
			if t.kind == tLParen {
				tr.parenDepth++
			} else if t.kind == tRParen && tr.parenDepth > 0 {
				delete(tr.unionDepths, tr.parenDepth)
				delete(tr.unionOrders, tr.parenDepth)
				tr.parenDepth--
			}
			tr.advance()
			tr.emit(t.val)
			continue
		}

		if t.kind == tDot {
			tr.advance()
			tr.emit(".")
			tr.prevWasDot = true
			continue
		}

		// Identifiers: aggregate function (only before "("), keyword, or lowercase field name
		if t.kind == tIdent {
			tr.advance()
			prevDot := tr.prevWasDot
			tr.prevWasDot = false
			lower := strings.ToLower(t.val)
			nextIsDot := tr.peek(0).kind == tDot
			prevAlias := false
			if tr.pos >= 2 {
				if pv := strings.ToUpper(tr.tokens[tr.pos-2].val); pv == "КАК" || pv == "AS" {
					prevAlias = !prevDot
				}
			}
			// Ссылка / Reference → id (virtual primary-key field, like 1C).
			// Работает и после точки (Н.Ссылка → н.id), и без алиаса
			// (ВЫБРАТЬ Ссылка ИЗ Справочник.X → SELECT id FROM x).
			if isReferenceName(lower) {
				switch {
				case prevAlias:
					// Имя виртуального поля зарезервировано: исторически
					// `КАК Ссылка` создаёт SQL-алиас id. Важно не пропускать его
					// через qualifyReference — `AS таблица.id` невалиден.
					tr.emit("id")
				case prevDot:
					tr.emit("id")
				case tr.sourceCtx.isReferenceAliasAt(tr.pos-1, lower):
					tr.emit("id")
				default:
					tr.emit(tr.qualifyReference("id"))
				}
				continue
			}
			// Системные колонки регистра — PascalCase русские алиасы
			// (см. systemColAlias). Работает и с префиксом (Х.Период), и без,
			// но только если соответствующий источник действительно регистр.
			if col, ok := tr.systemColumnAlias(t.val, prevDot); ok {
				tr.emit(col)
				continue
			}
			if agg, ok := sqlAgg(t.val); ok && tr.peek(0).kind == tLParen {
				tr.emit(agg)
			} else if kw, ok := sqlKW(t.val); ok {
				switch kw {
				case "UNION":
					if len(tr.opts.RowFilters) > 0 {
						return Result{}, fmt.Errorf("row-level filters for UNION queries are not supported yet")
					}
					tr.unionDepths[tr.parenDepth] = true
				case "WHERE":
					tr.emit("WHERE")
					tr.section = sectionWhere
					if tr.parenDepth == 0 {
						if err := tr.emitPendingRowFiltersAfterWhere(); err != nil {
							return Result{}, err
						}
					}
					continue
				case "GROUP", "HAVING", "ORDER":
					if tr.parenDepth == 0 {
						if err := tr.emitPendingRowFiltersAsWhere(); err != nil {
							return Result{}, err
						}
					}
					if kw == "ORDER" && tr.unionDepths[tr.parenDepth] {
						tr.unionOrders[tr.parenDepth] = true
					}
				}
				tr.emit(kw)
				// track clause context
				switch kw {
				case "SELECT":
					tr.section = sectionSelect
				case "FROM":
					tr.section = sectionFrom
				case "GROUP":
					tr.section = sectionGroupBy
				case "HAVING":
					tr.section = sectionHaving
				case "ORDER":
					tr.section = sectionOrderBy
				}
			} else {
				if prevAlias {
					// Имя алиаса вывода (КАК <name>) — не колонка: эмитим как есть
					// и запоминаем, чтобы ссылки на него не квалифицировать/CAST'ить.
					tr.aliases[lower] = struct{}{}
					tr.emit(lower)
				} else if tr.section == sectionFrom && !prevDot {
					tr.emit(lower)
				} else if rd := tr.findRefDim(lower); rd != nil && !prevDot {
					if nextIsDot {
						tr.emit(rd.joinAlias)
					} else {
						switch tr.section {
						case sectionSelect:
							tr.emit(rd.displayCol())
							if p := strings.ToUpper(tr.peek(0).val); p != "КАК" && p != "AS" {
								tr.emit("AS")
								tr.emit(rd.fieldName)
								tr.aliases[strings.ToLower(rd.fieldName)] = struct{}{}
							}
						case sectionGroupBy, sectionOrderBy:
							tr.emit(rd.displayCol())
						default:
							tr.emit(rd.idCol)
						}
					}
				} else if col, ok := tr.colMap[lower]; ok && !prevDot {
					tr.emitOwnColumn(col, lower)
				} else if prevDot {
					if rd := tr.findRefDim(lower); rd != nil {
						tr.emit(rd.idCol)
					} else if c, ok2 := tr.colMap[lower]; ok2 {
						tr.emitQualifiedColumn(c, lower)
					} else {
						tr.emitQualifiedColumn(lower, lower)
					}
				} else {
					tr.emitOwnColumn(lower, lower)
				}
			}
			continue
		}

		tr.advance()
	}
	if err := tr.emitPendingRowFiltersAsWhere(); err != nil {
		return Result{}, err
	}
	if err := tr.assertRowFiltersApplied(); err != nil {
		return Result{}, err
	}
	return Result{
		SQL:              tr.build(),
		Args:             tr.args,
		Sources:          tr.sources,
		ProjectionFields: expandReferenceProjection(projectionFields, tr.refDims),
		Projection:       expandProjectionRefDims(projectionPlan, tr.refDims),
	}, nil
}

// dialectName возвращает строковое имя диалекта SQL для opts.Dialect; nil → "pg"
// (значение по умолчанию из dialectOrDefault). Используется в выборе шаблонов
// функций даты — strftime в SQLite, EXTRACT в PostgreSQL.
func dialectName(d storage.Dialect) string {
	if d == nil {
		return "pg"
	}
	return d.Name()
}

// funcRewrite — пара prefix/suffix токенов, которые оборачивают исходный
// аргумент скалярной функции. Например, Год(x) в SQLite → CAST(strftime('%Y', x)
// AS INTEGER), а ОКР(x, 2) → ROUND(x, 2).
type funcRewrite struct {
	prefix []tok
	suffix []tok
}

// tokenizeFragment токенизирует SQL-фрагмент и убирает финальный EOF.
func tokenizeFragment(s string) []tok {
	t := tokenize(s)
	if len(t) > 0 && t[len(t)-1].kind == tEOF {
		t = t[:len(t)-1]
	}
	return t
}

// scalarFuncRewrites возвращает таблицу замен скалярных 1С-функций под нужный
// диалект: функции даты (Год/НачалоДня/…) и математика (ОКР/АБС/ЦЕЛ). Ключи в
// нижнем регистре, сопоставление case-insensitive. Набор имён согласован со
// скриптовым DSL (см. interpreter/builtins.go), чтобы Окр/Абс/Цел работали и в
// модуле, и в тексте запроса (issue #39).
func scalarFuncRewrites(dialect string) map[string]funcRewrite {
	rw := func(prefix, suffix string) funcRewrite {
		return funcRewrite{prefix: tokenizeFragment(prefix), suffix: tokenizeFragment(suffix)}
	}
	// Математика. ROUND/ABS существуют в обоих диалектах с той же сигнатурой,
	// поэтому общие. round/abs-алиасы — для симметрии с RU-именами (фактически
	// no-op, нативный SQL и так бы прошёл).
	m := map[string]funcRewrite{
		"окр":   rw("ROUND(", ")"),
		"round": rw("ROUND(", ")"),
		"абс":   rw("ABS(", ")"),
		"abs":   rw("ABS(", ")"),
	}
	switch dialect {
	case "sqlite":
		// Цел — усечение к нулю (как decimal.Truncate(0) в DSL). В SQLite
		// CAST(x AS INTEGER) усекает к нулю.
		m["цел"] = rw("CAST(", " AS INTEGER)")
		m["int"] = rw("CAST(", " AS INTEGER)")
		m["началодня"] = rw("date(", ")")
		m["startofday"] = rw("date(", ")")
		m["конецдня"] = rw("datetime(date(", "), '+1 day', '-1 second')")
		m["endofday"] = rw("datetime(date(", "), '+1 day', '-1 second')")
		m["началомесяца"] = rw("date(", ", 'start of month')")
		m["startofmonth"] = rw("date(", ", 'start of month')")
		m["началогода"] = rw("date(", ", 'start of year')")
		m["startofyear"] = rw("date(", ", 'start of year')")
		m["год"] = rw("CAST(strftime('%Y',", ") AS INTEGER)")
		m["year"] = rw("CAST(strftime('%Y',", ") AS INTEGER)")
		m["месяц"] = rw("CAST(strftime('%m',", ") AS INTEGER)")
		m["month"] = rw("CAST(strftime('%m',", ") AS INTEGER)")
		m["день"] = rw("CAST(strftime('%d',", ") AS INTEGER)")
		m["day"] = rw("CAST(strftime('%d',", ") AS INTEGER)")
	default: // pg
		// Цел — усечение к нулю. В PG CAST(x AS INTEGER) округлял бы (half-even),
		// поэтому берём TRUNC, которое усекает к нулю.
		m["цел"] = rw("TRUNC(", ")")
		m["int"] = rw("TRUNC(", ")")
		m["началодня"] = rw("date_trunc('day',", ")")
		m["startofday"] = rw("date_trunc('day',", ")")
		m["конецдня"] = rw("(date_trunc('day',", ") + INTERVAL '1 day' - INTERVAL '1 microsecond')")
		m["endofday"] = rw("(date_trunc('day',", ") + INTERVAL '1 day' - INTERVAL '1 microsecond')")
		m["началомесяца"] = rw("date_trunc('month',", ")")
		m["startofmonth"] = rw("date_trunc('month',", ")")
		m["началогода"] = rw("date_trunc('year',", ")")
		m["startofyear"] = rw("date_trunc('year',", ")")
		// CAST(... AS INTEGER) — портативно (PG поддерживает обе формы,
		// :: не транслируется нашим токенизатором).
		m["год"] = rw("CAST(EXTRACT(YEAR FROM", ") AS INTEGER)")
		m["year"] = rw("CAST(EXTRACT(YEAR FROM", ") AS INTEGER)")
		m["месяц"] = rw("CAST(EXTRACT(MONTH FROM", ") AS INTEGER)")
		m["month"] = rw("CAST(EXTRACT(MONTH FROM", ") AS INTEGER)")
		m["день"] = rw("CAST(EXTRACT(DAY FROM", ") AS INTEGER)")
		m["day"] = rw("CAST(EXTRACT(DAY FROM", ") AS INTEGER)")
	}
	return m
}

// rewriteScalarFuncs обходит токены и разворачивает скалярные 1С-функции —
// Год(x), НачалоДня(x), ОКР(x, n), ЦЕЛ(x), … — в соответствующий SQL-шаблон
// диалекта. Раскрытие — на уровне токенов: сохраняются tIdent/tStr/tLParen и
// т.п., чтобы основной транслятор обработал внутренний аргумент через обычные
// правила (resolve ref dims, параметры и т.п.). Рекурсивно для вложенных
// вызовов: Месяц(НачалоМесяца(x)) и ОКР(СУММА(x), 0) тоже разворачиваются.
func rewriteScalarFuncs(tokens []tok, dialect string) []tok {
	rewrites := scalarFuncRewrites(dialect)
	var out []tok
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.kind == tIdent && i+1 < len(tokens) && tokens[i+1].kind == tLParen {
			key := strings.ToLower(t.val)
			if rw, ok := rewrites[key]; ok {
				// поиск парной закрывающей )
				depth := 0
				end := -1
				for j := i + 1; j < len(tokens); j++ {
					switch tokens[j].kind {
					case tLParen:
						depth++
					case tRParen:
						depth--
						if depth == 0 {
							end = j
						}
					}
					if end >= 0 {
						break
					}
				}
				if end < 0 {
					// нет пары — оставляем как есть, пусть SQL потом упадёт явно
					out = append(out, t)
					continue
				}
				inner := tokens[i+2 : end]
				inner = rewriteScalarFuncs(inner, dialect) // рекурсия
				out = append(out, rw.prefix...)
				out = append(out, inner...)
				out = append(out, rw.suffix...)
				i = end // пропускаем закрывающую )
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// momentTimeValue — контракт для DSL-значения «момент времени».
// Реализуется *runtime.MomentTime; интерфейс объявлен здесь чтобы не
// тянуть импорт runtime в query.
type momentTimeValue interface {
	PointInTime() (period time.Time, docID string)
}

// momentTimeCondition строит SQL-условие «строго до момента»:
//
//	(period < $1 OR (period = $1 AND recorder != $2))
//
// Логика «recorder != docID» гарантирует, что при перепроведении сам
// документ исключается из собственной сводки.
func (tr *translator) momentTimeCondition(mt momentTimeValue) string {
	d := dialectOrDefault(tr.opts.Dialect)
	period, docID := mt.PointInTime()
	tr.args = append(tr.args, period)
	periodPH := d.Placeholder(len(tr.args))
	if docID == "" {
		// document-less moment — простое сравнение
		return "period <= " + periodPH
	}
	id, err := uuid.Parse(docID)
	if err != nil {
		// docID — невалидный UUID, безопасный fallback: только по периоду.
		return "period <= " + periodPH
	}
	// period используется в SQL дважды (period < ... OR period = ...).
	// SQLite-плейсхолдеры анонимные ('?') — каждый '?' это отдельный
	// позиционный аргумент, поэтому period нужно добавить ВТОРЫМ аргументом
	// под второй плейсхолдер, иначе аргументы съезжают («missing argument»).
	// Для нумерованных диалектов ($n) лишний дубль period безвреден.
	tr.args = append(tr.args, period)
	periodPH2 := d.Placeholder(len(tr.args))
	if d.Name() == "sqlite" {
		tr.args = append(tr.args, id.String())
	} else {
		tr.args = append(tr.args, id)
	}
	docPH := d.Placeholder(len(tr.args))
	return fmt.Sprintf("(period < %s OR (period = %s AND recorder != %s))",
		periodPH, periodPH2, docPH)
}

// firstArgMoment возвращает *MomentTime если первый аргумент VT —
// это &Param и paramValues[name] = *MomentTime. Иначе nil — fallback на
// обычное period <= ...
func (tr *translator) firstArgMoment(args []tok) momentTimeValue {
	if len(args) != 1 || args[0].kind != tParam {
		return nil
	}
	v := tr.paramValues[args[0].val]
	if mt, ok := v.(momentTimeValue); ok {
		return mt
	}
	return nil
}

// systemColAlias maps the PascalCase русский alias for register system columns
// (period / вид_движения / recorder / line_number) to the actual DB column name.
// Используется и в SELECT/WHERE верхнего уровня, и после точки (alias.Период).
func systemColAlias(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "период":
		return "period", true
	case "виддвижения":
		return "вид_движения", true
	case "регистратор":
		return "recorder", true
	case "номерстроки":
		return "line_number", true
	}
	return "", false
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

// pgCast returns a PostgreSQL explicit cast suffix for v.
func pgCast(v any) string {
	switch v := v.(type) {
	case time.Time:
		return "::timestamptz"
	case string:
		if isUUID(v) {
			return "::uuid"
		}
		return "::text"
	case float64, float32, decimal.Decimal:
		return "::numeric"
	case int, int32, int64, uint, uint32, uint64:
		return "::bigint"
	case bool:
		return "::boolean"
	}
	return ""
}
