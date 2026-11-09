package query

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Длинные идентификаторы в готовом SQL (#1946).
//
// Имена таблиц и колонок уже короткие: их дают функции metadata (TableName,
// ColumnName…), которые сами применяют metadata.SQLIdent. Но в запросе есть и
// имена, которых в метаданных нет: псевдонимы `КАК`, колонки виртуальных таблиц
// и вложенных запросов, алиасы источников. PostgreSQL обрезает длинное имя до
// 63 байт и в них — два разных псевдонима с общим началом становились одной
// колонкой, а метка результата приходила обрезанной, и `Выборка.<имя>` не
// находило значение. Поэтому весь SQL проходит здесь одной точкой: каждый
// идентификатор длиннее предела заменяется тем же SQLIdent, что и в DDL, — имя
// колонки, псевдоним и ссылка на него из внешнего запроса совпадают по
// построению. Метки результата потребитель возвращает к полному имени через
// RestoreLongLabels.

// shortenLongIdents заменяет в sql идентификаторы длиннее
// metadata.MaxSQLIdentBytes на metadata.SQLIdent и возвращает карту
// «короткое → полное» (nil, если заменять нечего). Содержимое строковых
// литералов в одинарных кавычках не трогается: это данные, а не имена.
func shortenLongIdents(sql string) (string, map[string]string) {
	if !hasLongRun(sql) {
		return sql, nil
	}
	var (
		sb     strings.Builder
		labels map[string]string
	)
	sb.Grow(len(sql))
	for i := 0; i < len(sql); {
		c := sql[i]
		if c == '\'' {
			j := skipQuoted(sql, i)
			sb.WriteString(sql[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(sql[i:])
		if !isIdentStart(r) || (i > 0 && isIdentPartBefore(sql, i)) {
			sb.WriteString(sql[i : i+size])
			i += size
			continue
		}
		j := i + size
		for j < len(sql) {
			r2, s2 := utf8.DecodeRuneInString(sql[j:])
			if !isIdentPart(r2) {
				break
			}
			j += s2
		}
		word := sql[i:j]
		if len(word) > metadata.MaxSQLIdentBytes {
			// В SQL идентификаторы платформы — в нижнем регистре; ключевые слова
			// такой длины не бывают. Нормализуем регистр, чтобы `Алиас` и
			// `алиас` (PostgreSQL сам приводит неэкранированные имена) дали одно
			// короткое имя.
			full := strings.ToLower(word)
			short := metadata.SQLIdent(full)
			if labels == nil {
				labels = make(map[string]string)
			}
			labels[short] = full
			word = short
		}
		sb.WriteString(word)
		i = j
	}
	return sb.String(), labels
}

// hasLongRun — быстрая проверка без аллокаций: есть ли вообще в строке подряд
// идущие символы идентификатора длиннее предела. Почти все запросы её не
// проходят, и для них shortenLongIdents ничего не копирует.
func hasLongRun(sql string) bool {
	run := 0
	for i := 0; i < len(sql); {
		r, size := utf8.DecodeRuneInString(sql[i:])
		if isIdentPart(r) {
			run += size
			if run > metadata.MaxSQLIdentBytes {
				return true
			}
		} else {
			run = 0
		}
		i += size
	}
	return false
}

func skipQuoted(sql string, i int) int {
	j := i + 1
	for j < len(sql) {
		if sql[j] == '\'' {
			if j+1 < len(sql) && sql[j+1] == '\'' {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return j
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }

func isIdentPart(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// isIdentPartBefore — стоит ли перед позицией i символ идентификатора (тогда
// i — середина слова, например после цифры, и начинать слово здесь нельзя).
func isIdentPartBefore(sql string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(sql[:i])
	return isIdentPart(r) || r == '$'
}

// longOutputLabels describes only the exported SELECT columns. SQL shortening
// also visits table names, source aliases, predicates and nested SELECTs; none
// of those names authorizes renaming an unrelated result column.
func longOutputLabels(tokens []tok, sql string, ctx sourceContext) (map[string]string, bool) {
	if !hasLongRun(sql) && !hasLongSourceColumns(ctx) {
		return nil, false
	}
	items := selectOutputItems(tokens)
	sqlItems := selectOutputItems(tokenize(sql))
	if len(items) != len(sqlItems) {
		return nil, true
	}
	bindings := map[string]string{}
	ambiguous := false
	add := func(physical, logical string) {
		physical = metadata.SQLIdent(physical)
		if previous, exists := bindings[physical]; exists && previous != logical {
			ambiguous = true
		}
		bindings[physical] = logical
	}
	for i, item := range items {
		col := outputLabelColumn(item)
		out := outputLabelColumn(sqlItems[i])
		if col.Star {
			for _, label := range wildcardOutputLabels(item, tokens, ctx, 0, map[int]bool{}) {
				add(label.physical, label.logical)
			}
			continue
		}
		// The emitted output alias (including aliases added for references and
		// numeric casts) is authoritative. For a bare physical long field, the
		// source projection supplies the full logical name missing from SQL.
		if out.Alias != "" {
			add(metadata.SQLIdent(out.Alias), out.Alias)
		} else if out.Output != "" && len(out.Path) == 0 {
			// Expressions expose their explicit AS name as Output, without a
			// field Path/Alias. It still names this selected result column;
			// identifiers inside the expression must not supply output labels.
			add(metadata.SQLIdent(out.Output), out.Output)
		} else if len(out.Path) > 0 {
			physical := lowerFast(out.Path[len(out.Path)-1])
			logical := physical
			if len(col.Output)+len("_id") > metadata.MaxSQLIdentBytes {
				logical = col.Output
				// SQL may still contain the full reference column here; the
				// final shortening pass runs after output labels are collected.
				if metadata.SQLIdent(logical+"_id") == metadata.SQLIdent(physical) {
					logical += "_id"
				}
			}
			add(metadata.SQLIdent(physical), logical)
		}
	}
	if ambiguous {
		// Duplicate physical output names with different logical owners cannot
		// be mapped safely after the driver has built a row map. Keep masking
		// on its existing fail-closed path for an ambiguous projection.
		return nil, true
	}
	var labels map[string]string
	for physical, logical := range bindings {
		if physical != logical && len(logical) > metadata.MaxSQLIdentBytes {
			if labels == nil {
				labels = map[string]string{}
			}
			labels[physical] = logical
		}
	}
	return labels, false
}

func hasLongSourceColumns(ctx sourceContext) bool {
	for _, scope := range ctx.scopes {
		for _, columns := range scope.wildcardColumns {
			for logical, physical := range columns {
				if len(logical) > metadata.MaxSQLIdentBytes ||
					(len(logical)+3 > metadata.MaxSQLIdentBytes && metadata.SQLIdent(logical+"_id") == physical) {
					return true
				}
			}
		}
	}
	return false
}

func outputLabelColumn(item []tok) ProjectionColumn {
	start, end := trimProjectionParentheses(item, 0, len(item))
	col, _ := parseProjectionItem(item[start:end])
	return col
}

type outputLabel struct{ physical, logical string }

func selectOutputItems(tokens []tok) [][]tok {
	for i, t := range tokens {
		if kw, ok := sqlKW(t.val); t.kind == tIdent && ok && kw == "SELECT" {
			return splitProjectionItems(tokens[selectProjectionStart(tokens, i):topLevelFrom(tokens, i)])
		}
	}
	return nil
}

// wildcardOutputLabels follows the selected qualifier, including projections
// forwarded by a derived table. A JOIN source that is not selected contributes
// no labels. Reference columns in a physical wildcard retain their _id suffix.
func wildcardOutputLabels(item, tokens []tok, ctx sourceContext, scopeID int, seen map[int]bool) []outputLabel {
	var labels []outputLabel
	if scopeID < 0 || scopeID >= len(ctx.scopes) || seen[scopeID] {
		return labels
	}
	seen[scopeID] = true
	defer delete(seen, scopeID)
	scope := ctx.scopes[scopeID]
	qualifiers := []string{}
	if len(item) == 3 && item[0].kind == tIdent && item[1].kind == tDot && item[2].kind == tStar {
		qualifiers = append(qualifiers, lowerFast(item[0].val))
	} else if len(item) == 1 && item[0].kind == tStar {
		for qualifier := range scope.wildcardColumns {
			qualifiers = append(qualifiers, qualifier)
		}
		for qualifier := range scope.derivedAliases {
			qualifiers = append(qualifiers, qualifier)
		}
	} else {
		return labels
	}
	for _, qualifier := range qualifiers {
		for logical, physical := range scope.wildcardColumns[qualifier] {
			if metadata.SQLIdent(logical+"_id") == physical {
				logical += "_id"
			}
			labels = append(labels, outputLabel{metadata.SQLIdent(physical), logical})
		}
		child, derived := scope.derivedAliases[qualifier]
		if !derived {
			continue
		}
		child = ctx.scopes[child].unionFirst
		for pos, t := range tokens {
			id, ok := ctx.scopeIDAt(pos)
			kw, isKW := sqlKW(t.val)
			if !ok || id != child || t.kind != tIdent || !isKW || kw != "SELECT" {
				continue
			}
			for _, projected := range splitProjectionItems(tokens[selectProjectionStart(tokens, pos):topLevelFrom(tokens, pos)]) {
				col := outputLabelColumn(projected)
				if col.Star {
					labels = append(labels, wildcardOutputLabels(projected, tokens, ctx, child, seen)...)
				} else if col.Output != "" {
					physical := col.Output
					if col.Alias == "" {
						physical = ctx.derivedOutputColumn(tokens, child, col.Output, map[int]bool{})
					}
					labels = append(labels, outputLabel{metadata.SQLIdent(physical), col.Output})
				}
			}
			break
		}
	}
	return labels
}

// RestoreLongLabels возвращает меткам результата полные имена: колонка,
// которую компилятор сократил (см. shortenLongIdents), приходит из базы под
// коротким именем, а потребитель — DSL, отчёт, виджет — ищет её по полному,
// как на SQLite до #1946. Звать сразу после выполнения, до маскирования и
// приведения типов: они смотрят на имена колонок. Повторный вызов безвреден.
func RestoreLongLabels(res *Result, rows []map[string]any, cols []string) {
	if res == nil || len(res.LongIdents) == 0 {
		return
	}
	for i, c := range cols {
		if full, ok := res.LongIdents[c]; ok {
			cols[i] = full
		}
	}
	if len(rows) == 0 {
		return
	}
	// У всех строк результата один набор колонок: пары, которые есть в первой
	// строке, — те, что надо переименовывать во всех.
	type pair struct{ short, full string }
	var present []pair
	for short, full := range res.LongIdents {
		if _, ok := rows[0][short]; ok && short != full {
			present = append(present, pair{short, full})
		}
	}
	for _, row := range rows {
		for _, p := range present {
			if v, ok := row[p.short]; ok {
				delete(row, p.short)
				row[p.full] = v
			}
		}
	}
}
