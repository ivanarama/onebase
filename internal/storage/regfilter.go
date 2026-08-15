package storage

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// RegFilter — отбор для списков регистров: точные значения измерений и период
// от/до включительно (issue #45). Dims сохраняет строковый HTTP-контракт;
// DimValues нужен типизированным внутренним вызовам (DSL), чтобы ссылка не
// превращалась через fmt.Sprintf в display-name, а дата/число не теряли тип.
type RegFilter struct {
	Dims      map[string]string // имя измерения → строка из HTTP/query
	DimValues map[string]any    // имя измерения → типизированное значение
	From      *time.Time
	To        *time.Time
	RowFilter *Predicate // additional SQL-side row-level access predicate
}

// IsEmpty сообщает, задан ли хоть один критерий отбора.
func (f RegFilter) IsEmpty() bool {
	return len(f.Dims) == 0 && len(f.DimValues) == 0 && f.From == nil && f.To == nil
}

// dimWhereClause строит условия WHERE по измерениям регистра и периоду.
// Принимает только измерения, фактически принадлежащие dims (защита от
// инъекции имён колонок). Значения подставляются через плейсхолдеры.
// includeFrom/includeTo управляют включением границ периода (для остатков
// From игнорируется). startIdx — номер первого плейсхолдера (с 1).
func dimWhereClause(d Dialect, dims []metadata.Field, f RegFilter, startIdx int, includeFrom, includeTo bool) (string, []any) {
	var conds []string
	var args []any
	idx := startIdx

	for _, fld := range dims {
		val, ok := f.DimValues[fld.Name]
		if !ok {
			var text string
			text, ok = f.Dims[fld.Name]
			val = text
		}
		if !ok || val == nil {
			continue
		}
		if text, isString := val.(string); isString && text == "" {
			continue
		}
		col := metadata.ColumnName(fld)
		arg := normalizeRegArg(d, val, false)
		// Для ссылочного измерения колонка хранит UUID — оборачиваем idArg,
		// чтобы PG получил uuid.UUID, а SQLite — строку (как при записи).
		if fld.RefEntity != "" {
			var id uuid.UUID
			var err error
			switch value := val.(type) {
			case uuid.UUID:
				id = value
			case *uuid.UUID:
				if value == nil {
					err = fmt.Errorf("nil UUID")
				} else {
					id = *value
				}
			case refUUIDGetter:
				id, err = uuid.Parse(value.GetRefUUID())
			case string:
				id, err = uuid.Parse(value)
			default:
				err = fmt.Errorf("unsupported reference filter %T", val)
			}
			if err != nil {
				// Значение не UUID (например ручной ?Измерение=мусор в URL) —
				// ссылочная колонка хранит UUID, совпадений быть не может.
				// Подставляем заведомо ложное условие (пустой результат на обоих
				// диалектах), а не сырую строку: на PostgreSQL `col(uuid) = 'мусор'`
				// упал бы с 500 (invalid input syntax for uuid).
				conds = append(conds, "1=0")
				continue
			}
			arg = idArg(d, id)
		}
		conds = append(conds, fmt.Sprintf("%s = %s", col, d.Placeholder(idx)))
		args = append(args, arg)
		idx++
	}

	if includeFrom && f.From != nil {
		conds = append(conds, fmt.Sprintf("period >= %s", d.Placeholder(idx)))
		args = append(args, *f.From)
		idx++
	}
	if includeTo && f.To != nil {
		conds = append(conds, fmt.Sprintf("period <= %s", d.Placeholder(idx)))
		args = append(args, *f.To)
	}

	if len(conds) == 0 {
		return "", nil
	}
	return strings.Join(conds, " AND "), args
}
