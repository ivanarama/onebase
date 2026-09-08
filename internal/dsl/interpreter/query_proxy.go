package interpreter

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/typedempty"
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
	ctxSrc   CtxSource
	compiler QueryCompiler
	guard    QueryGuard
}

type QueryCompiler func(ctx context.Context, text string, params map[string]any) (query.Result, error)

// GuardedColumns records fields changed by a host security guard for each row.
// Later normalization must not reinterpret a masked nil/empty string as the
// canonical empty value of the underlying metadata type.
type GuardedColumns []map[string]struct{}

func (g GuardedColumns) contains(row int, column string) bool {
	if row < 0 || row >= len(g) {
		return false
	}
	for guarded := range g[row] {
		if strings.EqualFold(guarded, column) {
			return true
		}
	}
	return false
}

// QueryGuard проверяет и правит строки результата до того, как они попадут в
// DSL: полевое маскирование ПДн (план 88E). Ошибка означает, что запрос читать
// нельзя, — строки в модуль не отдаются.
type QueryGuard func(ctx context.Context, res query.Result, rows []map[string]any) (GuardedColumns, error)

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

// NewQueryFactoryGuardedSource is the transaction-aware counterpart of
// NewQueryFactoryGuarded. Objects Новый Запрос are created before a DSL
// НачатьТранзакцию(), so retaining the context value from factory construction
// would send Выполнить() through a second connection. A live CtxSource keeps
// reads on the same transaction as catalog/document writes.
func NewQueryFactoryGuardedSource(ctxSrc CtxSource, db QueryDB, reg QueryRegistry, compiler QueryCompiler, guard QueryGuard) func(args []any) any {
	return func(args []any) any {
		return &queryProxy{
			params:   make(map[string]any),
			db:       db,
			reg:      reg,
			ctxSrc:   ctxSrc,
			compiler: compiler,
			guard:    guard,
		}
	}
}

func (q *queryProxy) context() context.Context {
	if q.ctxSrc != nil {
		return q.ctxSrc.Ctx()
	}
	return q.ctx
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
	ctx := q.context()
	var res query.Result
	var err error
	if q.compiler != nil {
		res, err = q.compiler(ctx, q.text, params)
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
	rows, err := q.db.QueryAll(ctx, res.SQL, res.Args...)
	if err != nil {
		panic(userError{Msg: "Ошибка выполнения SQL: " + err.Error() + "\nSQL: " + res.SQL})
	}
	var guarded GuardedColumns
	if q.guard != nil {
		guarded, err = q.guard(ctx, res, rows)
		if err != nil {
			panic(userError{Msg: "Запрос: " + err.Error()})
		}
	}
	query.NormalizeColumns(&res, rows)
	q.wrapRefColumns(res, rows)
	q.materializeTypedColumns(res, rows, guarded)
	arr := &Array{}
	for _, row := range rows {
		arr.items = append(arr.items, newQueryResultRow(row))
	}
	return arr
}

// materializeTypedColumns is deliberately DSL-only and runs after the host
// guard. Generic query.Run consumers keep SQL NULL unchanged, while modules see
// the canonical empty value of a proven direct projection.
func (q *queryProxy) materializeTypedColumns(res query.Result, rows []map[string]any, guarded GuardedColumns) {
	if len(res.TypedColumns) == 0 || len(rows) == 0 {
		return
	}
	entities := map[string]*metadata.Entity{}
	if q.reg != nil {
		for _, entity := range q.reg.Entities() {
			if entity != nil {
				entities[strings.ToLower(entity.Name)] = entity
			}
		}
	}
	for rowIndex, row := range rows {
		for column, desc := range res.TypedColumns {
			if guarded.contains(rowIndex, column) {
				continue
			}
			raw, exists := row[column]
			if !exists {
				// A hidden field is removed by the guard; do not recreate it as a
				// plausible empty value after the security boundary.
				continue
			}
			if desc.RefEntity != "" {
				if raw != nil {
					continue // non-empty references were wrapped by wrapRefColumns
				}
				entity := entities[strings.ToLower(desc.RefEntity)]
				row[column] = &Ref{Type: desc.RefEntity, Kind: refKindFromMetadata(entity)}
				continue
			}
			row[column] = typedempty.Normalize(desc, raw, nil)
		}
	}
}

func refKindFromMetadata(entity *metadata.Entity) metadata.Kind {
	if entity == nil {
		return ""
	}
	return entity.Kind
}

// wrapRefColumns оборачивает колонки-идентификаторы результата в *Ref, чтобы
// ссылка из запроса была ссылкой, а не строкой UUID (#1150).
//
// До этого `ТипЗнч(Выборка.Ссылка)` отвечал «Строка», хотя руководство обещает
// квалифицированное имя типа (`ДокументСсылка.Приход`), и написанный по нему
// `Если ТипЗнч(Выборка.Ссылка) = Тип("ДокументСсылка.Приход")` молча не
// срабатывал — без ошибки и без признака.
//
// Наименование ссылки остаётся UUID'ом — тем же значением, что колонка отдавала
// раньше. Это сознательная плата за совместимость: `Строка(Выборка.Ссылка)`,
// печать и сравнение со строкой продолжают видеть ровно то же, что видели до
// правки, а вытащить представление по каждой ссылке значило бы запрос на строку.
// Тот же приём уже применяет Документы.X.НайтиПоИдентификатору().
//
// Сравнение со строкой UUID при этом продолжает работать: оператор «=» сводит
// значения через refKey, который у *Ref берёт UUID. Это часть контракта правки,
// а не совпадение, — иначе конфигурации, сравнивающие колонку со строкой,
// сломались бы молча.
//
// Вызывается ПОСЛЕ стража ПДн: он правит сырые строки результата и обязан видеть
// значения в том виде, в каком их отдала БД.
func (q *queryProxy) wrapRefColumns(res query.Result, rows []map[string]any) {
	if len(res.RefColumns) == 0 || len(rows) == 0 || q.reg == nil {
		return
	}
	entities := map[string]*metadata.Entity{}
	for _, e := range q.reg.Entities() {
		if e != nil {
			entities[strings.ToLower(e.Name)] = e
		}
	}
	for col, entName := range res.RefColumns {
		ent := entities[strings.ToLower(entName)]
		if ent == nil {
			continue // сущности нет в реестре — оставляем значение как есть
		}
		for _, row := range rows {
			s, ok := row[col].(string)
			if !ok || !isRefUUIDValue(s) {
				continue
			}
			row[col] = &Ref{UUID: s, Name: s, Type: ent.Name, Kind: ent.Kind}
		}
	}
}

// isRefUUIDValue — значение колонки действительно похоже на идентификатор
// ссылки. Проверка нужна, потому что до обёртки строку успевает потрогать страж
// ПДн: подменённое маской значение ссылкой не является, и делать из него *Ref
// значило бы выдать маску за существующий объект.
func isRefUUIDValue(s string) bool {
	if s == "" {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
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
