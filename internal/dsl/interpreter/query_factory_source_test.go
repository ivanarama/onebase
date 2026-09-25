package interpreter_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// mutableSource — изменяемый CtxSource: фабрика и объект создаются на контексте
// A, а Выполнить() обязан наблюдать тот контекст, который жив в источнике в
// момент вызова (план 161, срез 1).
type mutableSource struct {
	mu  sync.Mutex
	ctx context.Context
}

func (s *mutableSource) Ctx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

func (s *mutableSource) switchTo(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
}

// ctxTag — ключ маркера в тестовых контекстах: ctxA и ctxB несут разные
// значения, поэтому сравнение по Value различает их, а не проходит вакуумно
// на структурном равенстве двух свежих cancelCtx.
type ctxTag struct{}

func taggedCtx(tag string) context.Context {
	return context.WithValue(context.Background(), ctxTag{}, tag)
}

// ctxSpyDB — spy QueryDB: фиксирует контекст каждого вызова.
type ctxSpyDB struct {
	t    *testing.T
	mu   sync.Mutex
	ctxs []context.Context
}

func (s *ctxSpyDB) QueryAll(ctx context.Context, _ string, _ ...any) ([]map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxs = append(s.ctxs, ctx)
	return []map[string]any{{"Код": "A-1"}}, nil
}

func (s *ctxSpyDB) Dialect() storage.Dialect { return nil }

func (s *ctxSpyDB) only() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(s.t, s.ctxs, 1)
	return s.ctxs[0]
}

type callSpy struct {
	t    *testing.T
	mu   sync.Mutex
	ctxs []context.Context
}

func (s *callSpy) add(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxs = append(s.ctxs, ctx)
}

func (s *callSpy) only() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(s.t, s.ctxs, 1)
	return s.ctxs[0]
}

// queryObject — This-интерфейс объекта Новый Запрос без привязки к пакету
// interpreter: тест работает через публичное поведение (Set/CallMethod).
type queryObject interface {
	Set(field string, val any)
	CallMethod(name string, args []any) any
}

func runQuery(t *testing.T, q queryObject) *interpreter.Array {
	t.Helper()
	q.Set("текст", "ВЫБРАТЬ 1 AS Код")
	res, ok := q.CallMethod("выполнить", nil).(*interpreter.Array)
	require.True(t, ok, "Выполнить() вернул не массив")
	return res
}

// Живой источник: фабрика и объект созданы на A, до Выполнить() источник
// переключён на B — compiler, SQL и guard обязаны наблюдать B.
func TestQueryFactorySourceObservesLiveContext(t *testing.T) {
	ctxA, cancelA := context.WithCancel(taggedCtx("A"))
	defer cancelA()
	ctxB, cancelB := context.WithCancel(taggedCtx("B"))
	defer cancelB()

	src := &mutableSource{ctx: ctxA}
	db := &ctxSpyDB{t: t}
	compilerCtx, guardCtx := &callSpy{t: t}, &callSpy{t: t}
	compiler := interpreter.QueryCompiler(func(ctx context.Context, _ string, _ map[string]any) (query.Result, error) {
		compilerCtx.add(ctx)
		return query.Result{SQL: "SELECT 1 AS Код"}, nil
	})
	guard := interpreter.QueryGuard(func(ctx context.Context, _ query.Result, _ []map[string]any) (interpreter.GuardedColumns, error) {
		guardCtx.add(ctx)
		return nil, nil
	})

	factory := interpreter.NewQueryFactoryGuardedSource(src, db, &stubReg{}, compiler, guard)
	q, ok := factory(nil).(queryObject)
	require.True(t, ok, "фабрика вернула не объект Запроса")

	// Заявленный сценарий: источник переключён на B ДО Выполнить().
	src.switchTo(ctxB)

	res := runQuery(t, q)
	require.Equal(t, 1, len(res.Iterate()), "строка результата потеряна")
	assert.Equal(t, "B", db.only().Value(ctxTag{}), "SQL выполнился не на живом контексте источника")
	assert.Equal(t, "B", compilerCtx.only().Value(ctxTag{}), "compiler видел контекст сборки, а не Выполнить()")
	assert.Equal(t, "B", guardCtx.only().Value(ctxTag{}), "guard видел контекст сборки, а не Выполнить()")
}

// Новый convenience-конструктор сходится в ту же реализацию: без compiler
// запрос компилируется напрямую, и контекст так же берётся в момент
// Выполнить().
func TestQueryFactorySourceConvenienceObservesLiveContext(t *testing.T) {
	ctxA, cancelA := context.WithCancel(taggedCtx("A"))
	defer cancelA()
	ctxB, cancelB := context.WithCancel(taggedCtx("B"))
	defer cancelB()

	src := &mutableSource{ctx: ctxA}
	db := &ctxSpyDB{t: t}
	factory := interpreter.NewQueryFactorySource(src, db, &stubReg{})
	q, ok := factory(nil).(queryObject)
	require.True(t, ok, "фабрика вернула не объект Запроса")

	src.switchTo(ctxB)

	res := runQuery(t, q)
	require.Equal(t, 1, len(res.Iterate()), "строка результата потеряна")
	assert.Equal(t, "B", db.only().Value(ctxTag{}))
}

// Статический конструктор остаётся статическим: контекст фиксируется при
// сборке фабрики/объекта и не переключается после.
func TestQueryFactoryStaticStaysOnConstructionContext(t *testing.T) {
	ctxA, cancelA := context.WithCancel(taggedCtx("A"))
	defer cancelA()

	db := &ctxSpyDB{t: t}
	compilerCtx := &callSpy{t: t}
	compiler := interpreter.QueryCompiler(func(ctx context.Context, _ string, _ map[string]any) (query.Result, error) {
		compilerCtx.add(ctx)
		return query.Result{SQL: "SELECT 1 AS Код"}, nil
	})
	factory := interpreter.NewQueryFactoryGuarded(ctxA, db, &stubReg{}, compiler, nil)
	q, ok := factory(nil).(queryObject)
	require.True(t, ok)

	res := runQuery(t, q)
	require.Equal(t, 1, len(res.Iterate()), "строка результата потеряна")
	assert.Equal(t, "A", db.only().Value(ctxTag{}))
	assert.Equal(t, "A", compilerCtx.only().Value(ctxTag{}))
}
