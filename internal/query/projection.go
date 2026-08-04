package query

import (
	"strconv"
	"strings"
)

// ProjectionColumn — один элемент списка выборки в порядке результирующих
// колонок. Используется полевым маскированием отчётов/виджетов/AI (план 88E):
// колонка, которая является простой ссылкой на поле, маскируется, а не приводит
// к отказу всего запроса.
type ProjectionColumn struct {
	// Output — ожидаемое имя результирующей колонки в нижнем регистре: алиас
	// «КАК ...», иначе последний идентификатор ссылки на поле. Для выражений и
	// для «*» пусто.
	Output string
	// Fields — логические поля-источники, чьё маскирование должно примениться к
	// этой колонке. Для ссылочного измерения сюда попадает и само поле, и
	// отображаемый реквизит связанной сущности (Наименование/Номер).
	Fields []string
	// Star — элемент выборки является «*».
	Star bool
}

// ProjectionPlan — разбор списка выборки для полевого маскирования (план 88E).
type ProjectionPlan struct {
	// Columns — элементы списка выборки в порядке результирующих колонок.
	Columns []ProjectionColumn
	// Simple — запрос состоит ровно из одного SELECT без ОБЪЕДИНИТЬ и вложенных
	// подзапросов, поэтому Columns однозначно соответствуют колонкам результата
	// и маскировать проекцию безопасно. Иначе действует прежний fail-closed
	// отказ по ProjectionFields.
	Simple bool
	// UnmaskableFields — логические поля, прочитанные вне простых элементов
	// выборки: в ГДЕ/ИМЕЯ/СГРУППИРОВАТЬ/УПОРЯДОЧИТЬ, в условиях соединения, в
	// аргументах виртуальных таблиц и внутри выражений/агрегатов списка выборки.
	// Маска на выходе такое поле не защищает: значение участвует в отборе (даёт
	// оракул перебора) или сворачивается в агрегат, поэтому запрос по
	// защищённому полю в этих позициях отклоняется целиком.
	//
	// Имена собираются КАК НАПИСАНЫ, поэтому среди них бывают и выходные алиасы
	// колонок («... Телефон КАК Т ... ГДЕ Т = …»): сверять их с именами полей
	// одного к одному нельзя, разрешать алиасы обязан потребитель плана.
	//
	// При Simple == false сюда попадают ВСЕ идентификаторы запроса: разобрать
	// такой запрос поколоночно нельзя, а отбор по защищённому полю обязан
	// отклоняться и внутри подзапроса.
	UnmaskableFields []string
	// UnmaskableOrdinals — номера колонок, использованные в СГРУППИРОВАТЬ и
	// УПОРЯДОЧИТЬ вместо имени («УПОРЯДОЧИТЬ ПО 1»). Ссылка по номеру сортирует
	// по значению колонки ровно так же, как ссылка по имени, и точно так же
	// раскрывает порядок скрытых значений.
	UnmaskableOrdinals []int
}

// analyzeProjection разбирает список выборки запроса. Работает по исходному
// потоку токенов — до перезаписи скалярных функций, как и projectionFieldNames.
func analyzeProjection(tokens []tok) ProjectionPlan {
	start, ok := singleSelectStart(tokens)
	if !ok {
		// Подзапрос или ОБЪЕДИНИТЬ: соответствие «элемент выборки → колонка
		// результата» неоднозначно, маскировать нечего сопоставить. Но отказ по
		// отбору обязан работать и здесь: иначе фиктивный подзапрос
		// («... И Наименование В (ВЫБРАТЬ ...)») снимал бы отказ по ГДЕ,
		// потому что запасной путь смотрит только список выборки.
		// Собираем все идентификаторы: перебор намеренно избыточен — лишнее имя
		// лишь ужесточает проверку, пропущенное открывает утечку.
		return ProjectionPlan{UnmaskableFields: allIdentifiers(tokens)}
	}
	end := topLevelFrom(tokens, start)
	plan := ProjectionPlan{Simple: true}
	unmask := newFieldSet()
	for _, item := range splitProjectionItems(tokens[start+1 : end]) {
		col, used := parseProjectionItem(item)
		plan.Columns = append(plan.Columns, col)
		unmask.addAll(used)
	}
	tail, ordinals := collectUnmaskableTail(tokens, end)
	unmask.addAll(tail)
	plan.UnmaskableFields = unmask.list()
	plan.UnmaskableOrdinals = ordinals
	return plan
}

// allIdentifiers — все идентификаторы потока, кроме ключевых слов и имён
// вызываемых функций.
func allIdentifiers(tokens []tok) []string {
	set := newFieldSet()
	set.addAll(identifiersIn(tokens))
	return set.list()
}

// singleSelectStart возвращает позицию единственного SELECT запроса. Второй
// SELECT (подзапрос или ОБЪЕДИНИТЬ) делает разбор непригодным.
func singleSelectStart(tokens []tok) (int, bool) {
	start, count := -1, 0
	for i, t := range tokens {
		if t.kind != tIdent {
			continue
		}
		if kw, isKW := sqlKW(t.val); isKW && kw == "SELECT" {
			count++
			if start < 0 {
				start = i
			}
		}
	}
	return start, start >= 0 && count == 1
}

// topLevelFrom находит ИЗ на нулевой глубине после SELECT — конец списка
// выборки. Без ИЗ (например `ВЫБРАТЬ &Параметр`) — конец потока.
func topLevelFrom(tokens []tok, start int) int {
	depth := 0
	for i := start + 1; i < len(tokens); i++ {
		switch tokens[i].kind {
		case tLParen:
			depth++
		case tRParen:
			if depth > 0 {
				depth--
			}
		case tIdent:
			if depth != 0 {
				continue
			}
			if kw, isKW := sqlKW(tokens[i].val); isKW && kw == "FROM" {
				return i
			}
		}
	}
	return len(tokens)
}

// splitProjectionItems делит список выборки по запятым нулевой глубины,
// отбрасывая ведущее РАЗЛИЧНЫЕ.
func splitProjectionItems(tokens []tok) [][]tok {
	var items [][]tok
	depth, from := 0, 0
	flush := func(to int) {
		item := trimProjectionItem(tokens[from:to])
		if len(item) > 0 {
			items = append(items, item)
		}
	}
	for i, t := range tokens {
		switch t.kind {
		case tLParen:
			depth++
		case tRParen:
			if depth > 0 {
				depth--
			}
		case tComma:
			if depth == 0 {
				flush(i)
				from = i + 1
			}
		}
	}
	flush(len(tokens))
	return items
}

// trimProjectionItem снимает модификаторы выборки (РАЗЛИЧНЫЕ), не относящиеся к
// самому выражению колонки.
func trimProjectionItem(item []tok) []tok {
	for len(item) > 0 && item[0].kind == tIdent {
		if kw, isKW := sqlKW(item[0].val); isKW && kw == "DISTINCT" {
			item = item[1:]
			continue
		}
		break
	}
	return item
}

// parseProjectionItem разбирает один элемент выборки: возвращает колонку и
// список логических полей, которые из-за выражения/агрегата не могут быть
// защищены маской на выходе.
func parseProjectionItem(item []tok) (ProjectionColumn, []string) {
	// Алиас вывода — только «КАК» нулевой глубины: внутри скобок то же слово
	// принадлежит выражению (ВЫРАЗИТЬ(Поле КАК Строка)), а не имени колонки.
	alias := ""
	depth := 0
	for i := 0; i+1 < len(item); i++ {
		switch item[i].kind {
		case tLParen:
			depth++
			continue
		case tRParen:
			if depth > 0 {
				depth--
			}
			continue
		case tIdent:
		default:
			continue
		}
		if depth != 0 {
			continue
		}
		if kw, isKW := sqlKW(item[i].val); isKW && kw == "AS" {
			if item[i+1].kind == tIdent {
				alias = strings.ToLower(item[i+1].val)
			}
			item = item[:i]
			break
		}
	}
	// Проекция «звёздочка» — и голая «*», и квалифицированная «К.*». Отличается
	// от умножения тем, что звёздочка ЗАМЫКАЕТ элемент выборки: «Цена * 2» на
	// ней не заканчивается. Признак намеренно широкий: принять за «*» лишнее —
	// значит промаскировать колонки по именам, то есть ужесточить; не принять
	// «К.*» — значит отдать строки как есть, что и было утечкой (элемент
	// [ident, dot, star] не проходил simpleFieldRef, уходил в ветку выражения
	// с пустыми Fields и до проверок не доживал).
	if n := len(item); n > 0 && item[n-1].kind == tStar {
		return ProjectionColumn{Star: true}, nil
	}
	if field, ok := simpleFieldRef(item); ok {
		output := alias
		if output == "" {
			output = strings.ToLower(field)
		}
		return ProjectionColumn{Output: output, Fields: []string{field}}, nil
	}
	// Выражение или агрегат: значение колонки — производная от исходных полей,
	// маска на выходе их не защищает (СУММА(Оклад), ПОДСТРОКА(Телефон, 1, 3)).
	return ProjectionColumn{Output: alias}, identifiersIn(item)
}

// simpleFieldRef распознаёт элемент вида `Поле` или `Квалификатор.Поле`
// (в т.ч. разыменование ссылки `Клиент.Наименование`) и возвращает последний
// идентификатор — логическое имя выбранного поля.
func simpleFieldRef(item []tok) (string, bool) {
	if len(item) == 0 || len(item)%2 == 0 {
		return "", false
	}
	for i, t := range item {
		if i%2 == 1 {
			if t.kind != tDot {
				return "", false
			}
			continue
		}
		if t.kind != tIdent {
			return "", false
		}
		if _, isKW := sqlKW(t.val); isKW {
			return "", false
		}
	}
	return item[len(item)-1].val, true
}

// identifiersIn — все идентификаторы выражения, кроме ключевых слов и имён
// вызываемых функций. Перебор намеренно избыточен: лишнее имя лишь ужесточает
// проверку, пропущенное поле — открывает утечку.
func identifiersIn(item []tok) []string {
	var out []string
	for i, t := range item {
		if t.kind != tIdent {
			continue
		}
		if _, isKW := sqlKW(t.val); isKW {
			continue
		}
		if i+1 < len(item) && item[i+1].kind == tLParen {
			continue // имя функции
		}
		out = append(out, t.val)
	}
	return out
}

// collectUnmaskableTail собирает поля, прочитанные после списка выборки: в
// ГДЕ/ИМЕЯ/СГРУППИРОВАТЬ/УПОРЯДОЧИТЬ, в условиях соединения (ПО) и внутри
// скобок секции ИЗ (аргументы виртуальных таблиц). Имена таблиц и алиасы самой
// секции ИЗ не собираются: они не поля.
// Вторым значением возвращает номера колонок, использованные вместо имени в
// СГРУППИРОВАТЬ/УПОРЯДОЧИТЬ.
func collectUnmaskableTail(tokens []tok, from int) ([]string, []int) {
	section := sectionFrom
	inJoinCond := false
	depth := 0
	var out []string
	var ordinals []int
	for i := from; i < len(tokens); i++ {
		t := tokens[i]
		switch t.kind {
		case tLParen:
			depth++
			continue
		case tRParen:
			if depth > 0 {
				depth--
			}
			continue
		case tNum:
			// «УПОРЯДОЧИТЬ ПО 1» сортирует по значению первой колонки так же,
			// как ссылка по имени. В остальных секциях число — литерал сравнения.
			if depth == 0 && (section == sectionOrderBy || section == sectionGroupBy) {
				if n, err := strconv.Atoi(t.val); err == nil && n > 0 {
					ordinals = append(ordinals, n)
				}
			}
			continue
		case tIdent:
		default:
			continue
		}
		if kw, isKW := sqlKW(t.val); isKW {
			if depth == 0 {
				switch kw {
				case "FROM":
					section, inJoinCond = sectionFrom, false
				case "WHERE":
					section, inJoinCond = sectionWhere, false
				case "GROUP":
					section, inJoinCond = sectionGroupBy, false
				case "HAVING":
					section, inJoinCond = sectionHaving, false
				case "ORDER":
					section, inJoinCond = sectionOrderBy, false
				case "JOIN":
					inJoinCond = false
				case "ON":
					if section == sectionFrom {
						inJoinCond = true
					}
				}
			}
			continue
		}
		if i+1 < len(tokens) && tokens[i+1].kind == tLParen {
			continue // имя функции
		}
		switch section {
		case sectionWhere, sectionGroupBy, sectionHaving, sectionOrderBy:
			out = append(out, t.val)
		case sectionFrom:
			if inJoinCond || depth > 0 {
				out = append(out, t.val)
			}
		}
	}
	return out, ordinals
}

// expandProjectionRefDims доводит план до фактических значений колонок: элемент
// выборки, названный ссылочным измерением, отдаёт не идентификатор, а
// Наименование/Номер связанной сущности — маска связанной сущности обязана
// примениться к этой колонке.
func expandProjectionRefDims(plan ProjectionPlan, dims []refDimInfo) ProjectionPlan {
	if !plan.Simple || len(dims) == 0 {
		return plan
	}
	for i, col := range plan.Columns {
		for _, dim := range dims {
			if !containsFold(col.Fields, dim.fieldName) {
				continue
			}
			display := "Наименование"
			if dim.refIsDoc {
				display = "Номер"
			}
			if !containsFold(col.Fields, display) {
				plan.Columns[i].Fields = append(plan.Columns[i].Fields, display)
			}
		}
	}
	return plan
}

func containsFold(list []string, name string) bool {
	for _, v := range list {
		if strings.EqualFold(v, name) {
			return true
		}
	}
	return false
}

// fieldSet — упорядоченное множество имён полей без учёта регистра.
type fieldSet struct {
	seen map[string]bool
	out  []string
}

func newFieldSet() *fieldSet { return &fieldSet{seen: map[string]bool{}} }

func (s *fieldSet) addAll(names []string) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || s.seen[key] {
			continue
		}
		s.seen[key] = true
		s.out = append(s.out, name)
	}
}

func (s *fieldSet) list() []string { return s.out }
