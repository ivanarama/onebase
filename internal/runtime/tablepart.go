package runtime

import (
	"fmt"
	"strings"
)

// Табличная часть внутри обработчика (issue #842).
//
// До этого `this.Товары` отдавалась сырым срезом `[]map[string]any`, у которого
// методов нет вовсе: `Для Каждого … Из this.Товары` работал, а
// `this.Товары.Количество()` — нет. Поломка выглядела выборочной и жила долго,
// потому что вызов несуществующего метода молча возвращал «Неопределено»:
// `Если СтрокиТовары.Количество() = 0` было просто Ложь, и проверка ничего не
// делала. После #718 отказ стал слышен — и вскрыл, что заполнение демо-базы
// торговли (`examples/trade`) падает на первой же реализации.
//
// Обёртка НЕ копирует строки: методы работают прямо с
// `Object.TablePartRows[имя]`, а итерация отдаёт те же самые `map`, которые
// потом запишет entityservice. Копия молча теряла бы правки строк — модуль
// проведения пишет в них (`Строка.Сумма = Строка.Количество * Строка.Цена`).
//
// Тот же контракт, что у `tpProxy` на пути `Документы.X.Создать()`: у одной
// табличной части не должно быть двух наборов методов в зависимости от того,
// откуда до неё добрались.

// tablePartMethods — то, что понимает табличная часть. Список отдаётся
// интерпретатору через MethodLister: опечатка в имени метода обязана быть
// слышной, а поднимать ошибку сам пакет не может — зависимость runtime от
// интерпретатора замкнула бы импорты (его тесты уже импортируют runtime).
var tablePartMethods = []string{
	"Количество", "Добавить", "Очистить", "Получить", "Удалить", "Итог",
	"Count", "Add", "Clear", "Get", "Delete", "Total",
}

// TablePart — доступ к строкам табличной части объекта из DSL.
type TablePart struct {
	obj  *Object
	name string // ключ в Object.TablePartRows (как объявлен в метаданных)
}

// Get/Set: у самой табличной части членов нет — обращаются к строкам.
func (t *TablePart) Get(string) any   { return nil }
func (t *TablePart) Set(string, any)  {}
func (t *TablePart) TypeName() string { return "ТабличнаяЧасть" }
func (t *TablePart) String() string {
	return fmt.Sprintf("ТабличнаяЧасть[%d]", len(t.rows()))
}
func (t *TablePart) rows() []map[string]any {
	if t == nil || t.obj == nil {
		return nil
	}
	return t.obj.TablePartRows[t.name]
}

// IterateRows — контракт цикла «Для Каждого Строка Из this.Товары». Отдаёт те
// же map, что лежат в объекте: правки строк в цикле обязаны доезжать до записи.
func (t *TablePart) IterateRows() []map[string]any { return t.rows() }

// Index/Iterate — обращение по номеру строки и общий обход коллекции.
func (t *TablePart) Index(i int) any {
	rows := t.rows()
	if i < 0 || i >= len(rows) {
		return nil
	}
	return &tablePartRow{m: rows[i]}
}

func (t *TablePart) Iterate() []any {
	rows := t.rows()
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, &tablePartRow{m: r})
	}
	return out
}

// KnownMethods реализует interpreter.MethodLister.
func (t *TablePart) KnownMethods() (string, []string) {
	return "ТабличнаяЧасть", tablePartMethods
}

func (t *TablePart) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "количество", "count":
		return float64(len(t.rows()))
	case "добавить", "add":
		row := map[string]any{}
		t.obj.TablePartRows[t.name] = append(t.obj.TablePartRows[t.name], row)
		return &tablePartRow{m: row}
	case "очистить", "clear":
		t.obj.TablePartRows[t.name] = nil
	case "получить", "get":
		if len(args) > 0 {
			return t.Index(intArg(args[0]))
		}
	case "удалить", "delete":
		if len(args) > 0 {
			idx := intArg(args[0])
			rows := t.rows()
			if idx >= 0 && idx < len(rows) {
				t.obj.TablePartRows[t.name] = append(rows[:idx], rows[idx+1:]...)
			}
		}
	case "итог", "total":
		// Итог("Сумма") — сумма колонки по всем строкам: считать её вручную в
		// цикле приходится в каждом втором модуле проведения.
		if len(args) > 0 {
			return t.total(fmt.Sprintf("%v", args[0]))
		}
	}
	return nil
}

func (t *TablePart) total(column string) float64 {
	return SumRowsColumn(t.rows(), column)
}

// SumRowsColumn — сумма колонки по строкам табличной части. Экспортирована,
// потому что реализаций доступа к ТЧ три (обработчик, запись через
// Документы.X.Создать(), события формы), и «Итог» у них обязан считаться
// одинаково: разошедшиеся копии одной формулы — это то, что план 117C вычищал
// из автонумерации.
func SumRowsColumn(rows []map[string]any, column string) float64 {
	low := strings.ToLower(strings.TrimSpace(column))
	sum := 0.0
	for _, row := range rows {
		for k, v := range row {
			if strings.ToLower(k) != low {
				continue
			}
			if f, ok := toFloatValue(v); ok {
				sum += f
			}
			break
		}
	}
	return sum
}

// TablePartMethods — общий список методов табличной части для всех трёх точек
// доступа. Отдаётся интерпретатору через MethodLister.
func TablePartMethods() []string { return tablePartMethods }

// RowIndexArg приводит аргумент-номер строки к int (-1 — не число).
func RowIndexArg(v any) int { return intArg(v) }

// tablePartRow — строка табличной части. Ведёт себя как MapThis интерпретатора
// (член → ключ карты без учёта регистра), но живёт в runtime, чтобы пакет не
// зависел от интерпретатора: обход в цикле всё равно отдаёт сырые map, которые
// интерпретатор оборачивает сам.
type tablePartRow struct{ m map[string]any }

func (r *tablePartRow) Get(name string) any {
	low := strings.ToLower(name)
	for k, v := range r.m {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

func (r *tablePartRow) Set(name string, v any) {
	low := strings.ToLower(name)
	for k := range r.m {
		if strings.ToLower(k) == low {
			r.m[k] = v
			return
		}
	}
	r.m[low] = v
}

func intArg(v any) int {
	if f, ok := toFloatValue(v); ok {
		return int(f)
	}
	return -1
}

func toFloatValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%g", &f); err == nil {
			return f, true
		}
	}
	if s, ok := v.(interface{ String() string }); ok {
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(s.String()), "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}
