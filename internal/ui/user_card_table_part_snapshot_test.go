package ui

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"modernc.org/sqlite"
)

// These probes live only in test views. No production hook, extra SELECT or
// sleep is added to the reader. The function is reached after a real statement
// snapshot has been acquired and before that statement reads the table part.
type tableCardSnapshotGate struct {
	ctx     context.Context
	entered chan struct{}
	release chan struct{}
	once    atomic.Bool
}

var tableCardSQLiteGates sync.Map
var registerTableCardSnapshotProbe = sync.OnceValue(func() error {
	return sqlite.RegisterScalarFunction("onebase_card_snapshot_probe", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		v, ok := tableCardSQLiteGates.Load(args[1])
		if !ok {
			return nil, fmt.Errorf("missing card snapshot test gate")
		}
		gate := v.(*tableCardSnapshotGate)
		if gate.once.CompareAndSwap(false, true) {
			close(gate.entered)
			select {
			case <-gate.release:
			case <-gate.ctx.Done():
				return nil, gate.ctx.Err()
			}
		}
		return args[0], nil
	})
})

func TestUserCardTablePartPublicSnapshotMatrix(t *testing.T) {
	if err := registerTableCardSnapshotProbe(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"standalone", "read-snapshot", "dsl-read-committed"} {
		for _, change := range []string{"account-and-rows", "deletion-and-rows"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					testTableCardSnapshot(t, db, mode, change)
				})
			})
		}
	}
}

func testTableCardSnapshot(t *testing.T, db *storage.DB, mode, change string) {
	t.Helper()
	f := newTableCardFixture(t, db)
	f.saveBranches(t, "A")
	writer := tableCardConcurrentDB(t, db)
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	key := uuid.NewString()
	gate := &tableCardSnapshotGate{ctx: ctx, entered: make(chan struct{}), release: make(chan struct{})}
	tableCardSQLiteGates.Store(key, gate)
	defer tableCardSQLiteGates.Delete(key)
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate.release) }) }
	defer unblock()

	// A dedicated connection owns only this test's advisory lock. Other tests
	// and the writer use distinct connections; no shared service is changed.
	var owner *pgx.Conn
	var lockKey int64
	if db.IsPostgres() {
		var err error
		owner, err = pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := owner.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		u := uuid.New()
		lockKey = int64(binary.BigEndian.Uint32(u[:4])) + 1
		if _, err := owner.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := owner.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey); err != nil {
				t.Error(err)
			}
		}()
		if _, err := db.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION onebase_card_snapshot_probe(v uuid) RETURNS uuid
 LANGUAGE plpgsql VOLATILE AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN v; END $$`, lockKey)); err != nil {
			t.Fatal(err)
		}
	} else {
		var journal string
		if err := db.QueryRow(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
			t.Fatalf("concurrent SQLite proof requires WAL: %q %v", journal, err)
		}
	}

	// Replace only the test catalog with a projection of its original table.
	// The account column calls the barrier; all stored bytes and column types
	// remain real. The dependent table part still refers to the backing table.
	table := metadata.TableName(f.card.Name)
	rows, err := db.Query(ctx, "SELECT * FROM "+table+" LIMIT 0")
	if err != nil {
		t.Fatal(err)
	}
	columns := rows.FieldNames()
	rows.Close()
	account := metadata.ColumnName(f.card.Fields[1])
	projection := make([]string, len(columns))
	for i, column := range columns {
		quoted := pgx.Identifier{column}.Sanitize()
		projection[i] = quoted
		if column == account {
			probe := "onebase_card_snapshot_probe(" + quoted + ")"
			if db.IsSQLite() {
				probe = "onebase_card_snapshot_probe(" + quoted + ", '" + key + "')"
			}
			projection[i] = probe + " AS " + quoted
		}
	}
	if _, err := db.Exec(ctx, "ALTER TABLE "+table+" RENAME TO onebase_card_snapshot_backing"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "CREATE VIEW "+table+" AS SELECT "+strings.Join(projection, ", ")+" FROM onebase_card_snapshot_backing"); err != nil {
		t.Fatal(err)
	}
	secondUser := uuid.NewString()
	if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO _users(id,login,password_hash) VALUES(%s,%s,%s)", db.Dialect().Placeholder(1), db.Dialect().Placeholder(2), db.Dialect().Placeholder(3)), secondUser, "second-user", "unused"); err != nil {
		t.Fatal(err)
	}
	begin, finish := "", ""
	if mode == "dsl-read-committed" {
		begin, finish = "НачатьТранзакцию();", "ЗафиксироватьТранзакцию();"
	}
	prog := mustParse(t, fmt.Sprintf(`Функция Проверка() Экспорт
 З=Новый Запрос; З.Текст="ВЫБРАТЬ Номер ИЗ Документ.Ордер ГДЕ Номер = 'VISIBLE'";
 %s ПроверитьРежим();
 А=З.Выполнить().Количество(); Б=З.Выполнить().Количество();
 З.Текст="ВЫБРАТЬ Номер ИЗ Документ.Ордер ГДЕ Номер = 'HIDDEN'";
 В=З.Выполнить().Количество(); %s
 Возврат А*100+Б*10+В;
КонецФункции`, begin, finish))
	type outcome struct {
		value any
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		var result any
		execute := func(execCtx context.Context) error {
			vars, tx := f.s.buildDSLVarsTx(auth.ContextWithUser(execCtx, f.user), runtime.NewMovementsCollector("test", uuid.Nil))
			defer interpreter.RollbackTxExecution(tx)
			vars["проверитьрежим"] = interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
				if mode == "dsl-read-committed" {
					if !storage.HasTx(tx.Ctx()) {
						return nil, fmt.Errorf("DSL did not open a real transaction")
					}
					if db.IsPostgres() {
						var level string
						if err := db.QueryRow(tx.Ctx(), "SHOW transaction_isolation").Scan(&level); err != nil {
							return nil, err
						}
						if level != "read committed" {
							return nil, fmt.Errorf("DSL isolation %q", level)
						}
					}
				}
				return nil, nil
			})
			return f.s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars)
		}
		var err error
		if mode == "read-snapshot" {
			err = db.WithReadSnapshot(ctx, execute)
		} else {
			err = execute(ctx)
		}
		done <- outcome{result, err}
	}()

	if owner == nil {
		select {
		case <-gate.entered:
		case result := <-done:
			t.Fatalf("reader missed barrier: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	} else {
		// pg_locks is an observed synchronization barrier, not an assumed delay.
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			// objid stores the low 32 bits of the bigint advisory lock key.
			if err := owner.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=($1::bigint & 4294967295)::oid AND NOT granted)", lockKey).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case result := <-done:
				t.Fatalf("reader missed PG barrier: %+v", result)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
	}
	// One real concurrent commit changes both sides of the authorization input.
	err = writer.WithTx(ctx, func(writeCtx context.Context) error {
		column, value := account, any(secondUser)
		if change == "deletion-and-rows" {
			column, value = "deletion_mark", true
		}
		if _, err := writer.Exec(writeCtx, fmt.Sprintf("UPDATE onebase_card_snapshot_backing SET %s=%s WHERE id=%s", column, writer.Dialect().Placeholder(1), writer.Dialect().Placeholder(2)), value, f.cardID.String()); err != nil {
			return err
		}
		return writer.UpsertTablePartRows(writeCtx, f.card.Name, "Филиалы", f.cardID, []map[string]any{{"Филиал": "B"}}, f.card.TableParts[0])
	})
	if err != nil {
		t.Fatalf("concurrent writer must really commit: %v", err)
	}
	if owner != nil {
		if _, err := owner.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey); err != nil {
			t.Fatal(err)
		}
	}
	unblock()
	select {
	case result := <-done:
		want := int64(100)
		if mode == "read-snapshot" || (mode == "dsl-read-committed" && db.IsSQLite()) {
			want = 110
		}
		value, ok := result.value.(decimal.Decimal)
		if result.err != nil || !ok || !value.Equal(decimal.NewFromInt(want)) {
			t.Fatalf("single-statement access: got=%v want=%v err=%v", result.value, want, result.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(0) {
		t.Fatalf("fresh operation reused old rights: %v", got)
	}
}

func tableCardConcurrentDB(t *testing.T, db *storage.DB) *storage.DB {
	t.Helper()
	if db.IsPostgres() {
		// The active read transaction already owns a pool connection; calls
		// without its context acquire a different connection from this pool.
		return db
	}
	// SQLite's ordinary pool has exactly one connection. A genuine concurrent
	// writer/observer must open the same WAL database through a separate pool.
	peer, err := storage.ConnectSQLite(context.Background(), db.SQLitePath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Close)
	return peer
}
