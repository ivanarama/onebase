package interpreter_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/storage"
)

// openTxTestDB connects using TEST_DSN env var; skips the test if unset.
func openTxTestDB(t *testing.T) (*storage.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		t.Skip("TEST_DSN not set — пропускаем интеграционный тест транзакций")
	}
	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(db.Close)

	_, err = db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _tx_test_items (
			id   SERIAL PRIMARY KEY,
			name TEXT NOT NULL
		)`)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Exec(context.Background(), `DROP TABLE IF EXISTS _tx_test_items`) //nolint:errcheck,gosec // G104: директива подавляет только перечисленные линтеры
	})
	_, err = db.Exec(ctx, `TRUNCATE _tx_test_items`)
	require.NoError(t, err)

	return db, ctx
}

func runTxProc(t *testing.T, db *storage.DB, state *interpreter.TxState, src string) error {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	// DSL-функция Создать(_, name) — вставляет строку через txState.Ctx()
	createFn := interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("Создать: нужны 2 аргумента")
		}
		name := fmt.Sprintf("%v", args[1])
		if _, execErr := db.Exec(state.Ctx(),
			`INSERT INTO _tx_test_items(name) VALUES ($1)`, name); execErr != nil {
			return nil, fmt.Errorf("Создать: %w", execErr)
		}
		return nil, nil
	})

	extra := interpreter.NewTxFunctions(state, db)
	extra["Создать"] = createFn

	interp := interpreter.New()
	return interp.Run(prog.Procedures[0], nil, extra)
}

func countTxItems(t *testing.T, db *storage.DB, ctx context.Context) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM _tx_test_items`).Scan(&n))
	return n
}

func TestTx_Commit(t *testing.T) {
	db, ctx := openTxTestDB(t)
	state := interpreter.NewTxState(ctx)

	src := `Процедура Тест()
		НачатьТранзакцию();
		Создать("items", "Имя1");
		Создать("items", "Имя2");
		ЗафиксироватьТранзакцию();
	КонецПроцедуры`
	require.NoError(t, runTxProc(t, db, state, src))
	assert.Equal(t, 2, countTxItems(t, db, ctx))
}

func TestTx_Rollback_OnException(t *testing.T) {
	db, ctx := openTxTestDB(t)
	state := interpreter.NewTxState(ctx)

	src := `Процедура Тест()
		НачатьТранзакцию();
		Попытка
			Создать("items", "Имя1");
			Error("rollback me");
			Создать("items", "Имя2");
		Исключение
			ОтменитьТранзакцию();
		КонецПопытки;
	КонецПроцедуры`
	require.NoError(t, runTxProc(t, db, state, src))
	assert.Equal(t, 0, countTxItems(t, db, ctx))
}

func TestTx_Nested_Savepoint(t *testing.T) {
	db, ctx := openTxTestDB(t)
	state := interpreter.NewTxState(ctx)

	// Внешняя транзакция коммитится, внутренняя откатывается через savepoint.
	src := `Процедура Тест()
		НачатьТранзакцию();
		Создать("items", "Внешняя");
		Попытка
			НачатьТранзакцию();
			Создать("items", "Внутренняя");
			Error("inner rollback");
		Исключение
			ОтменитьТранзакцию();
		КонецПопытки;
		ЗафиксироватьТранзакцию();
	КонецПроцедуры`
	require.NoError(t, runTxProc(t, db, state, src))
	assert.Equal(t, 1, countTxItems(t, db, ctx))
}

func TestTx_NoExplicit_AutoCommit(t *testing.T) {
	db, ctx := openTxTestDB(t)
	state := interpreter.NewTxState(ctx)

	src := `Процедура Тест()
		Создать("items", "А");
		Создать("items", "Б");
	КонецПроцедуры`
	require.NoError(t, runTxProc(t, db, state, src))
	assert.Equal(t, 2, countTxItems(t, db, ctx))
}

func TestTx_BorrowedOuterTransactionUsesSavepointSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tx.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE _tx_test_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	require.NoError(t, err)

	err = db.WithTx(ctx, func(txCtx context.Context) error {
		_, execErr := db.Exec(txCtx, `INSERT INTO _tx_test_items(name) VALUES (?)`, "outer")
		if execErr != nil {
			return execErr
		}
		state := interpreter.NewTxState(txCtx)
		funcs := interpreter.NewTxFunctions(state, db)
		if got := callTxBuiltinResult(t, funcs, "ТранзакцияАктивна"); got != true {
			return fmt.Errorf("borrowed transaction was not visible before DSL begin: %#v", got)
		}
		src := `Процедура Тест()
			НачатьТранзакцию();
			Создать("items", "inner");
			ОтменитьТранзакцию();
		КонецПроцедуры`
		if runErr := runTxProc(t, db, state, src); runErr != nil {
			return runErr
		}
		if got := callTxBuiltinResult(t, funcs, "TransactionActive"); got != true {
			return fmt.Errorf("borrowed transaction disappeared after savepoint rollback: %#v", got)
		}
		var count int
		if scanErr := db.QueryRow(txCtx, `SELECT COUNT(*) FROM _tx_test_items`).Scan(&count); scanErr != nil {
			return scanErr
		}
		if count != 1 {
			return fmt.Errorf("borrowed savepoint rollback left %d rows", count)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, countTxItems(t, db, ctx))
}
