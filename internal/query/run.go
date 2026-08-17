package query

// Выполнение скомпилированного запроса вместе с приведением булевых колонок
// (#962, находка Н4).
//
// Приведение появилось в #704, но жило в одном потребителе — DSL. Все
// остальные (отчёты, их экспорт, виджеты, REST v2, `onebase query`,
// ИИ-инструмент) звали storage.RunQuery* напрямую и получали сырое значение
// драйвера: PostgreSQL отдаёт bool, SQLite хранит булево в INTEGER и отдаёт
// int64.
//
// Расхождение видно не только в выводе. Правило условного оформления
// «услуга = Истина» на PostgreSQL срабатывает, а на SQLite молча нет; ключи
// группировок расходятся («/false» против «/0»); `onebase query --json` отдаёт
// разные типы. То есть пресет отчёта, настроенный на одном движке, на другом
// ведёт себя иначе — без единой ошибки.
//
// Поэтому здесь не «ещё одна функция нормализации», а единственный способ
// выполнить скомпилированный запрос: приведение внутри, забыть его нельзя.
// Разослать вызовы по восьми потребителям значило бы повторить ровно ту
// ошибку, из-за которой находка и появилась.

import (
	"context"

	"github.com/ivantit66/onebase/internal/storage"
)

// queryRunner — то, что умеет исполнять SQL. Интерфейс, а не *storage.DB,
// чтобы вызывающий мог подставить обёртку с ограничениями или тестовый двойник.
type queryRunner interface {
	RunQuery(ctx context.Context, sql string, args []any) ([]map[string]any, []string, error)
	RunQueryLimit(ctx context.Context, sql string, args []any, maxRows int) ([]map[string]any, []string, bool, error)
}

// Run исполняет скомпилированный запрос и приводит булевы колонки к bool.
func Run(ctx context.Context, db queryRunner, res *Result) ([]map[string]any, []string, error) {
	rows, cols, err := db.RunQuery(ctx, res.SQL, res.Args)
	if err != nil {
		return rows, cols, err
	}
	NormalizeBoolColumns(res.BoolColumns, rows)
	return rows, cols, nil
}

// RunLimit — то же с ограничением числа строк; третьим значением возвращается
// признак усечения.
func RunLimit(ctx context.Context, db queryRunner, res *Result, maxRows int) ([]map[string]any, []string, bool, error) {
	rows, cols, truncated, err := db.RunQueryLimit(ctx, res.SQL, res.Args, maxRows)
	if err != nil {
		return rows, cols, truncated, err
	}
	NormalizeBoolColumns(res.BoolColumns, rows)
	return rows, cols, truncated, nil
}

// NormalizeBoolColumns приводит значения перечисленных колонок к bool.
//
// NULL остаётся nil: «не заполнено» — не то же самое, что Ложь, и подменять
// одно другим здесь нельзя. Незнакомый тип оставляем как есть — молча
// превратить его в Ложь опаснее, чем отдать в исходном виде.
func NormalizeBoolColumns(cols []string, rows []map[string]any) {
	if len(cols) == 0 {
		return
	}
	for _, row := range rows {
		for _, col := range cols {
			v, ok := row[col]
			if !ok || v == nil {
				continue
			}
			if b, converted := ToBoolValue(v); converted {
				row[col] = b
			}
		}
	}
}

// ToBoolValue приводит значение булевой колонки к bool. Второе значение —
// признак того, что приведение состоялось.
func ToBoolValue(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case int64:
		return t != 0, true
	case int:
		return t != 0, true
	case int32:
		return t != 0, true
	case float64:
		return t != 0, true
	}
	return false, false
}

// компиляция проверяет, что *storage.DB подходит под queryRunner.
var _ queryRunner = (*storage.DB)(nil)
