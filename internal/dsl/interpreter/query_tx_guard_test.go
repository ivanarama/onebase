package interpreter_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// runTxQueryModule гоняет через публичный Run модуль с транзакцией и запросом.
// Фабрика запросов и транзакционные функции инжектируются так же, как это
// делает рантайм (dslvars + NewTxFunctions).
func runTxQueryModule(t *testing.T, db *storage.DB, factory func([]any) any, txState *interpreter.TxState, withTx bool) error {
	t.Helper()
	src := `Процедура Работа()
	Если ВходитьВТранзакцию Тогда
		НачатьТранзакцию();
	КонецЕсли;
	Запрос = Новый Запрос;
	Запрос.Текст = "ВЫБРАТЬ 1 AS Код";
	Рез = Запрос.Выполнить();
КонецПроцедуры`
	l := lexer.New(src, "txguard.os")
	prog, err := parser.New(l).ParseProgram()
	require.NoError(t, err)

	interp := interpreter.New()
	extra := map[string]any{
		"__factory_Запрос":   factory,
		"__factory_Query":    factory,
		"ВходитьВТранзакцию": withTx,
	}
	if txState != nil {
		for k, v := range interpreter.NewTxFunctions(txState, db) {
			extra[k] = v
		}
	}
	obj := runtime.NewObject("T", metadata.KindCatalog)
	runErr := interp.Run(prog.Procedures[0], obj, extra)
	if txState != nil {
		// Штатная граница исполнения откатывает брошенную транзакцию.
		require.NoError(t, txState.RollbackOpen(context.Background()))
		require.False(t, txState.HasOpen(), "транзакция не должна переживать границу исполнения")
	}
	return runErr
}

// #1272: запрос с контекстом, снятым до НачатьТранзакцию, обязан быстро дать
// управляемую ошибку вместо фатального ожидания собственного соединения
// SQLite. Транзакция после ошибки откатывается штатной границей исполнения.
func TestQueryTxGuard_DeadlockBecomesUserError(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "guard.db"))
	require.NoError(t, err)
	defer db.Close()

	txState := interpreter.NewTxState(ctx)
	factory := interpreter.NewQueryFactoryWithTxState(ctx, db, &stubReg{}, txState)
	runErr := runTxQueryModule(t, db, factory, txState, true)
	require.Error(t, runErr)
	require.Contains(t, runErr.Error(), "полученным до НачатьТранзакцию")
}

// Запрос вне транзакции работает как раньше: нового таймаута нет.
func TestQueryTxGuard_NoTransactionUnaffected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "guard2.db"))
	require.NoError(t, err)
	defer db.Close()

	txState := interpreter.NewTxState(ctx)
	factory := interpreter.NewQueryFactoryWithTxState(ctx, db, &stubReg{}, txState)
	runErr := runTxQueryModule(t, db, factory, txState, false)
	require.NoError(t, runErr)
}

// Живая фабрика (CtxSource = TxState) внутри транзакции читает без ошибки —
// страховка не срабатывает на ней ложью: контекст снимается в момент
// Выполнить и уже содержит транзакцию.
func TestQueryTxGuard_LiveSourceInsideTransactionUnaffected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "guard3.db"))
	require.NoError(t, err)
	defer db.Close()

	txState := interpreter.NewTxState(ctx)
	factory := interpreter.NewQueryFactoryGuardedSource(txState, db, &stubReg{}, nil, nil)
	src := `Процедура Работа()
	НачатьТранзакцию();
	Запрос = Новый Запрос;
	Запрос.Текст = "ВЫБРАТЬ 1 AS Код";
	Рез = Запрос.Выполнить();
	ЗафиксироватьТранзакцию();
КонецПроцедуры`
	l := lexer.New(src, "txguard3.os")
	prog, err := parser.New(l).ParseProgram()
	require.NoError(t, err)
	interp := interpreter.New()
	extra := map[string]any{
		"__factory_Запрос": factory,
		"__factory_Query":  factory,
	}
	for k, v := range interpreter.NewTxFunctions(txState, db) {
		extra[k] = v
	}
	obj := runtime.NewObject("T", metadata.KindCatalog)
	require.NoError(t, interp.Run(prog.Procedures[0], obj, extra))
	require.False(t, txState.HasOpen())
}
