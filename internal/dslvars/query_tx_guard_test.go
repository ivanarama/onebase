package dslvars

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Проверяем подключение страховки через общую карту хоста, а не только
// прямой конструктор фабрики: статический запрос в DSL-транзакции должен
// возвращать управляемую ошибку до обращения к занятому SQLite-соединению.
func TestCommon_QueryTxGuard(t *testing.T) {
	for _, tc := range []struct {
		name      string
		withState bool
		beginTx   bool
		wantGuard bool
	}{
		{name: "guarded_transaction", withState: true, beginTx: true, wantGuard: true},
		{name: "guarded_without_transaction", withState: true},
		{name: "legacy_without_transaction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, reg, baseCtx := constantsFixture(t)
			// Deadline ограничивает тест, если при регрессии запрос всё же
			// попытается взять второе соединение. Это не таймаут защиты.
			ctx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
			defer cancel()
			txState := interpreter.NewTxState(ctx)
			common := Common{Ctx: ctx, Reg: reg, Store: db}
			if tc.withState {
				common.TxState = txState
			}
			vars := common.Build()
			for k, v := range interpreter.NewTxFunctions(txState, db) {
				vars[k] = v
			}
			begin := ""
			if tc.beginTx {
				begin = "НачатьТранзакцию();"
			}
			prog, err := parser.New(lexer.New(fmt.Sprintf(`Функция Работа()
%s
Запрос = Новый Запрос;
Запрос.Текст = "ВЫБРАТЬ 7 AS Код";
Возврат Запрос.Выполнить();
КонецФункции`, begin), "common-txguard.os")).ParseProgram()
			if err != nil {
				t.Fatal(err)
			}
			var result any
			runErr := interpreter.New().RunWithResult(prog.Procedures[0], runtime.NewObject("T", metadata.KindCatalog), &result, vars)
			if err := txState.RollbackOpen(context.Background()); err != nil {
				t.Fatal(err)
			}
			if txState.HasOpen() {
				t.Fatal("transaction survived execution cleanup")
			}
			if tc.wantGuard {
				if runErr == nil || !strings.Contains(runErr.Error(), "полученным до НачатьТранзакцию") {
					t.Fatalf("want controlled transaction-context error, got %v", runErr)
				}
				return
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			rows, ok := result.(*interpreter.Array)
			if !ok || len(rows.Iterate()) != 1 {
				t.Fatalf("want one query row, got %T(%v)", result, result)
			}
		})
	}
}
