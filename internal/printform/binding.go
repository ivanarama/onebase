package printform

import (
	"regexp"
	"strings"
)

// binding.go — резолвер выражений печатных форм (план 64, этап 3). Перенесён из
// renderer.go (resolveExpr/interpolate) и расширен поддержкой Итог.<ТЧ>.<Поле>.
// Используется и legacy-рендерером (renderer.go делегирует сюда), и декларативным
// движком (declarative.go) — единый язык выражений.
//
// Грамматика выражения:
//   @row                       — номер текущей строки (1-based в строковом контексте)
//   Поле                       — поле текущей строки или документа
//   Поле.ПодПоле               — поле ссылки (через ctx.Refs)
//   Константы.Имя              — глобальная константа
//   Итог.<ТЧ>.<Поле>           — сумма числовой колонки <Поле> табличной части <ТЧ>
//   <выражение> | <формат>     — форматирование (см. ApplyFormat: number:2, date, …)

var reInterp = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// ResolveExpr вычисляет выражение expr против контекста. row — текущая строка
// (может быть nil вне табличного контекста); rowNum — её 1-based номер (для @row).
// Форматирование (часть после |) здесь НЕ применяется — это делает ResolveValue.
func ResolveExpr(expr string, ctx *RenderContext, row map[string]any, rowNum int) any {
	expr = strings.TrimSpace(expr)
	if expr == "@row" {
		return rowNum
	}

	// Итог.<ТЧ>.<Поле> — сумма числовой колонки табличной части.
	if rest, ok := cutPrefixFold(expr, "Итог."); ok {
		idx := strings.Index(rest, ".")
		if idx == -1 {
			return nil
		}
		tpName := rest[:idx]
		field := rest[idx+1:]
		return sumColumn(ctx, tpName, field)
	}

	// Константы.Имя
	if key, ok := cutPrefixFold(expr, "Константы."); ok {
		if ctx != nil && ctx.Constants != nil {
			return ctx.Constants[key]
		}
		return nil
	}

	// Поле.ПодПоле — резолв ссылки.
	if i := strings.Index(expr, "."); i != -1 {
		fieldName := expr[:i]
		subField := expr[i+1:]
		// В Excel-шаблонах пользователи естественно пишут полное имя
		// {{Контрагент.Наименование}}, даже когда печатается сам Контрагент.
		// Такой корневой квалификатор означает поле текущей записи, а не ссылку.
		if ctx != nil && strings.EqualFold(fieldName, ctx.EntityName) {
			if v, ok := lookupMapFold(ctx.Document, subField); ok {
				return resolveRefDisplay(v, ctx)
			}
			return nil
		}
		// сначала ищем в текущей строке.
		if row != nil {
			if refVal, ok := row[fieldName]; ok {
				if name := refSubValue(refVal, subField, ctx); name != nil {
					return name
				}
			}
		}
		// затем на уровне документа.
		if ctx != nil && ctx.Document != nil {
			if docVal, ok := ctx.Document[fieldName]; ok {
				if name := refSubValue(docVal, subField, ctx); name != nil {
					return name
				}
			}
		}
		return nil
	}

	// Простое поле: строка → документ.
	if row != nil {
		if v, ok := row[expr]; ok {
			return resolveRefDisplay(v, ctx)
		}
	}
	if ctx != nil && ctx.Document != nil {
		if v, ok := ctx.Document[expr]; ok {
			return resolveRefDisplay(v, ctx)
		}
	}
	return nil
}

func lookupMapFold(values map[string]any, key string) (any, bool) {
	if v, ok := values[key]; ok {
		return v, true
	}
	for name, v := range values {
		if strings.EqualFold(name, key) {
			return v, true
		}
	}
	return nil, false
}

// ResolveValue вычисляет «выражение | формат» и применяет форматтер, возвращая
// строку. Используется декларативным движком для параметров областей.
func ResolveValue(exprWithFmt string, ctx *RenderContext, row map[string]any, rowNum int) string {
	expr, fmtSpec := splitExprFormat(exprWithFmt)
	val := ResolveExpr(expr, ctx, row, rowNum)
	return ApplyFormat(val, fmtSpec)
}

// InterpolateText заменяет {{выражение|формат}} в тексте на вычисленные значения
// (без HTML-экранирования — оно выполняется рендерером sheet при выводе).
// Применяется только в декларативном пути (text-ячейки макета); DSL-путь не трогается.
func InterpolateText(text string, ctx *RenderContext, row map[string]any, rowNum int) string {
	return reInterp.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[2 : len(match)-2]
		return ResolveValue(inner, ctx, row, rowNum)
	})
}

// splitExprFormat делит «выражение | формат» на части (формат может отсутствовать).
func splitExprFormat(s string) (expr, fmtSpec string) {
	parts := strings.SplitN(s, "|", 2)
	expr = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		fmtSpec = strings.TrimSpace(parts[1])
	}
	return expr, fmtSpec
}

// cutPrefixFold убирает регистронезависимый префикс, возвращая остаток и ok.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// sumColumn суммирует числовую колонку field табличной части tpName.
// Результат мемоизируется в ctx.sumCache по ключу (ТЧ, поле) — повторные вызовы
// (например Итог внутри repeat-строки) возвращают кэш без повторного прохода.
func sumColumn(ctx *RenderContext, tpName, field string) float64 {
	if ctx == nil {
		return 0
	}
	cacheKey := strings.ToLower(tpName) + "\x00" + field
	if ctx.sumCache != nil {
		if v, ok := ctx.sumCache[cacheKey]; ok {
			return v
		}
	}
	rows := lookupTablePart(ctx, tpName)
	total := 0.0
	for _, r := range rows {
		if v, ok := r[field]; ok {
			if f, ok2 := toFloat(v); ok2 {
				total += f
			}
		}
	}
	if ctx.sumCache == nil {
		ctx.sumCache = make(map[string]float64)
	}
	ctx.sumCache[cacheKey] = total
	return total
}

// lookupTablePart находит табличную часть по имени (регистронезависимо).
func lookupTablePart(ctx *RenderContext, name string) []map[string]any {
	if ctx == nil || ctx.TableParts == nil {
		return nil
	}
	if rows, ok := ctx.TableParts[name]; ok {
		return rows
	}
	for k, rows := range ctx.TableParts {
		if strings.EqualFold(k, name) {
			return rows
		}
	}
	return nil
}

// refSubValue возвращает значение subField ссылки refVal (UUID → ctx.Refs), либо nil.
func refSubValue(refVal any, subField string, ctx *RenderContext) any {
	if ctx == nil || ctx.Refs == nil {
		return nil
	}
	refID, ok := refVal.(string)
	if !ok {
		return nil
	}
	refData, ok := ctx.Refs[refID]
	if !ok {
		return nil
	}
	if v, ok := refData[subField]; ok {
		return v
	}
	return nil
}

// resolveRefDisplay: если v — UUID из ctx.Refs, возвращает его наименование.
func resolveRefDisplay(v any, ctx *RenderContext) any {
	if ctx == nil || ctx.Refs == nil {
		return v
	}
	id, ok := v.(string)
	if !ok {
		return v
	}
	refData, ok := ctx.Refs[id]
	if !ok {
		return v
	}
	if name, ok := refData["наименование"]; ok {
		return name
	}
	return v
}
