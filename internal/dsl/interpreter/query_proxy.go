package interpreter

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// QueryDB is the minimal storage interface needed by queryProxy.
type QueryDB interface {
	QueryAll(ctx context.Context, sql string, args ...any) ([]map[string]any, error)
	Dialect() storage.Dialect
}

// QueryRegistry is the minimal registry interface needed by queryProxy.
type QueryRegistry interface {
	Registers() []*metadata.Register
	InfoRegisters() []*metadata.InfoRegister
	AccountRegisters() []*metadata.AccountRegister
	Entities() []*metadata.Entity
}

// queryProxy реализует DSL-объект Новый Запрос.
// Поддерживает свойство Текст (Get/Set) и методы УстановитьПараметр / Выполнить.
type queryProxy struct {
	text     string
	params   map[string]any
	db       QueryDB
	reg      QueryRegistry
	ctx      context.Context
	compiler QueryCompiler
	guard    QueryGuard
}

type QueryCompiler func(ctx context.Context, text string, params map[string]any) (query.Result, error)

// QueryGuard проверяет и правит строки результата до того, как они попадут в
// DSL: полевое маскирование ПДн (план 88E). Ошибка означает, что запрос читать
// нельзя, — строки в модуль не отдаются.
type QueryGuard func(ctx context.Context, res query.Result, rows []map[string]any) error

// NewQueryProxy создаёт фабрику для инъекции через extraVars.
// Использование: extraVars["__factory_Запрос"] = interpreter.NewQueryFactory(ctx, db, reg)
func NewQueryFactory(ctx context.Context, db QueryDB, reg QueryRegistry) func(args []any) any {
	return NewQueryFactoryWithCompiler(ctx, db, reg, nil)
}

// NewQueryFactoryWithCompiler creates a query factory that delegates compilation
// to the host runtime. UI uses this to inject row-level access filters; callers
// that pass nil keep the legacy direct query.Compile behavior.
func NewQueryFactoryWithCompiler(ctx context.Context, db QueryDB, reg QueryRegistry, compiler QueryCompiler) func(args []any) any {
	return NewQueryFactoryGuarded(ctx, db, reg, compiler, nil)
}

// NewQueryFactoryGuarded additionally attaches a host guard over the result
// rows — UI uses it for field-level masking (план 88E), so a processing cannot
// read protected values that the same user would only see masked in a report.
func NewQueryFactoryGuarded(ctx context.Context, db QueryDB, reg QueryRegistry, compiler QueryCompiler, guard QueryGuard) func(args []any) any {
	return func(args []any) any {
		return &queryProxy{
			params:   make(map[string]any),
			db:       db,
			reg:      reg,
			ctx:      ctx,
			compiler: compiler,
			guard:    guard,
		}
	}
}

// ─── This interface ───────────────────────────────────────────────────────────

func (q *queryProxy) Get(field string) any {
	switch field {
	case "текст", "text":
		return q.text
	}
	return nil
}

func (q *queryProxy) Set(field string, val any) {
	switch field {
	case "текст", "text":
		q.text = fmt.Sprintf("%v", val)
	}
}

// ─── MethodCallable interface ─────────────────────────────────────────────────

func (q *queryProxy) CallMethod(name string, args []any) any {
	switch name {
	case "установитьпараметр", "setparameter":
		if len(args) >= 2 {
			key := fmt.Sprintf("%v", args[0])
			q.params[key] = args[1]
		}
		return nil
	case "выполнить", "execute":
		return q.execute()
	}
	panic(userError{Msg: "Объект Запрос не имеет метода " + name})
}

// unwrapArrayParams converts DSL params for query compilation:
// - *Array → []any (each item unwrapped)
// - any reference-like value implementing GetRefUUID → UUID string
// This ensures pgx receives plain Go types, not interpreter-specific wrappers.
func unwrapArrayParams(params map[string]any) map[string]any {
	result := make(map[string]any, len(params))
	for k, v := range params {
		switch val := v.(type) {
		case *Array:
			items := make([]any, len(val.items))
			for i, item := range val.items {
				items[i] = unwrapRef(item)
			}
			result[k] = items
		default:
			result[k] = unwrapRef(v)
		}
	}
	return result
}

func unwrapRef(v any) any {
	if ref, ok := v.(interface{ GetRefUUID() string }); ok {
		return ref.GetRefUUID()
	}
	return v
}

func (q *queryProxy) execute() *Array {
	if strings.TrimSpace(q.text) == "" {
		panic(userError{Msg: "Запрос.Текст не задан"})
	}
	params := unwrapArrayParams(q.params)
	var res query.Result
	var err error
	if q.compiler != nil {
		res, err = q.compiler(q.ctx, q.text, params)
	} else {
		res, err = query.Compile(q.text, query.CompileOpts{
			Params:      params,
			Registers:   q.reg.Registers(),
			Entities:    q.reg.Entities(),
			InfoRegs:    q.reg.InfoRegisters(),
			AccountRegs: q.reg.AccountRegisters(),
			Dialect:     q.db.Dialect(),
		})
	}
	if err != nil {
		panic(userError{Msg: "Ошибка запроса: " + err.Error()})
	}
	rows, err := q.db.QueryAll(q.ctx, res.SQL, res.Args...)
	if err != nil {
		panic(userError{Msg: "Ошибка выполнения SQL: " + err.Error() + "\nSQL: " + res.SQL})
	}
	if q.guard != nil {
		if err := q.guard(q.ctx, res, rows); err != nil {
			panic(userError{Msg: "Запрос: " + err.Error()})
		}
	}
	normalizeBoolColumns(res.BoolColumns, rows)
	arr := &Array{}
	for _, row := range rows {
		arr.items = append(arr.items, newQueryResultRow(row))
	}
	return arr
}

// normalizeBoolColumns приводит колонки булевых полей к типу Булево. Диалекты
// отдают их по-разному: PostgreSQL — bool, SQLite хранит булево в INTEGER и
// отдаёт int64. Без приведения одно и то же поле вело себя в модуле по-разному
// в зависимости от СУБД — вплоть до выбора не той ветки (issue #704).
//
// Значение NULL остаётся Неопределено: «не заполнено» — не то же самое, что
// Ложь, и подменять одно другим здесь нельзя.
func normalizeBoolColumns(cols []string, rows []map[string]any) {
	if len(cols) == 0 {
		return
	}
	for _, row := range rows {
		for _, col := range cols {
			v, ok := row[col]
			if !ok || v == nil {
				continue
			}
			if b, converted := toBoolValue(v); converted {
				row[col] = b
			}
		}
	}
}

// toBoolValue приводит значение булевой колонки к bool. Неизвестные типы
// оставляем как есть: молча превратить незнакомое значение в Ложь опаснее, чем
// отдать его в модуль в исходном виде.
func toBoolValue(v any) (bool, bool) {
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

func newQueryResultRow(row map[string]any) *Struct {
	s := NewStructFromMap(row)
	id, hasID := s.vals["id"]
	if !hasID {
		return s
	}

	// The query compiler intentionally maps the reserved output names
	// Ссылка/Reference/Ref to the SQL alias "id". Keep that SQL contract while
	// making the materialized value available through the DSL names that
	// produced it.
	for _, alias := range []string{"ссылка", "reference", "ref"} {
		if _, exists := s.vals[alias]; !exists {
			s.Set(alias, id)
		}
	}
	return s
}
