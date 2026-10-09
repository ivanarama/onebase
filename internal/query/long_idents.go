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

// addSourceColumnLabels дополняет карту физическими именами длинных полей
// источников запроса. Колонка, выбранная без псевдонима, приходит из базы под
// своим физическим именем — уже коротким (его дал metadata.ColumnName), и
// shortenLongIdents её не видит: в SQL длинного слова нет. Без этого метка
// такой колонки зависела бы от того, ставит ли компилятор псевдоним, — то есть
// от детали, которая меняется вместе с ним.
func addSourceColumnLabels(res *Result, opts CompileOpts) {
	add := func(fields []metadata.Field) {
		for _, f := range fields {
			logical := metadata.LogicalColumnName(f)
			if len(logical) <= metadata.MaxSQLIdentBytes {
				continue
			}
			if res.LongIdents == nil {
				res.LongIdents = make(map[string]string)
			}
			res.LongIdents[metadata.SQLIdent(logical)] = logical
		}
	}
	for _, src := range res.Sources {
		name, _, _ := strings.Cut(src.Name, ".")
		for _, e := range opts.Entities {
			if strings.EqualFold(e.Name, name) {
				add(e.Fields)
				for _, tp := range e.TableParts {
					add(tp.Fields)
				}
			}
		}
		for _, r := range opts.Registers {
			if strings.EqualFold(r.Name, name) {
				add(r.Dimensions)
				add(r.Resources)
				add(r.Attributes)
			}
		}
		for _, r := range opts.InfoRegs {
			if strings.EqualFold(r.Name, name) {
				add(r.Dimensions)
				add(r.Resources)
			}
		}
		for _, r := range opts.AccountRegs {
			if strings.EqualFold(r.Name, name) {
				add(r.Resources)
			}
		}
	}
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
