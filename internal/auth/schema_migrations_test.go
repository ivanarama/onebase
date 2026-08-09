package auth_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// На чистой PostgreSQL-базе платформа не стартовала вовсе: auth-схема падала на
// «столбец "token_hash" отношения "_sessions" уже существует (SQLSTATE 42701)»
// (issue #672).
//
// Догоняющие ALTER TABLE ADD COLUMN, нужные базам, созданным до появления этих
// колонок, на свежей базе неизбежно натыкаются на уже созданную CREATE TABLE
// колонку. Это штатно и раньше гасилось — но проверкой ТЕКСТА ошибки драйвера
// («duplicate column» / «already exists»). На сервере с локализованными
// сообщениями текст другой, проверка не срабатывала, и запуск падал.
//
// Поэтому регрессия закрыта двумя тестами: структурным (в auth не должно быть
// классификации ошибок драйвера по тексту — он ловит причину и работает
// везде) и поведенческим на реально пустой схеме PostgreSQL.

// Классификация ошибок драйвера по человекочитаемому тексту зависит от локали
// сервера, поэтому в auth её быть не должно: есть storage.AddColumnIfMissing
// (PostgreSQL — ADD COLUMN IF NOT EXISTS, SQLite — проверка каталога) и разбор
// по SQLSTATE, как в storage/constraint_errors.go.
func TestAuthSchema_NoDriverErrorTextMatching(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// Слова, по которым узнают ошибку драйвера. Все они переводятся сервером.
	markers := []string{
		"duplicate column", "already exists", "does not exist",
		"no such column", "no such table", "duplicate key",
	}
	var violations []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val := strings.ToLower(lit.Value)
			for _, m := range markers {
				if strings.Contains(val, m) {
					violations = append(violations,
						fset.Position(lit.Pos()).String()+": "+lit.Value)
				}
			}
			return true
		})
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("ошибка драйвера распознаётся по тексту (%d) — на сервере с локализованными сообщениями это не сработает:\n  %s\n\n"+
			"Используйте storage.AddColumnIfMissing или разбор по SQLSTATE (образец — storage/constraint_errors.go).",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// Приёмка ровно по issue: на ПУСТОЙ базе PostgreSQL auth-схема разворачивается
// без ошибок, и повторный вызов ничего не ломает. Пустоту даёт эфемерная схема —
// в общей тестовой базе таблицы уже созданы соседними тестами, и дефект на ней
// не воспроизвёлся бы.
func TestAuthSchema_EnsureSchemaOnEmptyPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан — проверка чистой базы требует PostgreSQL")
	}
	ctx := context.Background()
	schema := storage.NewEphemeralSchemaName()
	db, err := storage.ConnectWithSchema(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("ConnectWithSchema: %v", err)
	}
	if err := db.CreateSchema(ctx, schema); err != nil {
		db.Close()
		t.Fatalf("CreateSchema: %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropSchemaCascade(context.Background(), schema); err != nil {
			t.Errorf("DropSchemaCascade(%s): %v", schema, err)
		}
		db.Close()
	})

	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema на пустой базе: %v", err)
	}
	// Повторный вызов — тот же путь, что при каждом старте сервера.
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("повторный EnsureSchema: %v", err)
	}
	// Колонки, на которых падал запуск, должны существовать.
	for _, c := range []struct{ table, col string }{
		{"_sessions", "token_hash"},
		{"_sessions", "public_id"},
		{"_users", "totp_secret"},
		{"_users", "lang"},
	} {
		var exists bool
		if err := db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns
			   WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2)`,
			c.table, c.col).Scan(&exists); err != nil {
			t.Fatalf("проверка %s.%s: %v", c.table, c.col, err)
		}
		if !exists {
			t.Errorf("колонка %s.%s не создана", c.table, c.col)
		}
	}
}

// Тот же прогон на сервере с локализованными сообщениями — воспроизведение
// ровно того окружения, где дефект и проявился. Пропускается, если сервер
// собран без каталогов переводов (тогда сообщения остаются английскими и
// прежний разбор по тексту сработал бы).
func TestAuthSchema_EnsureSchemaOnLocalizedPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	schema := storage.NewEphemeralSchemaName()
	db, err := storage.ConnectWithSchema(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("ConnectWithSchema: %v", err)
	}
	if err := db.CreateSchema(ctx, schema); err != nil {
		db.Close()
		t.Fatalf("CreateSchema: %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropSchemaCascade(context.Background(), schema); err != nil {
			t.Errorf("DropSchemaCascade(%s): %v", schema, err)
		}
		db.Close()
	})

	if _, err := db.Exec(ctx, `SET lc_messages TO 'ru_RU.UTF-8'`); err != nil {
		t.Skipf("сервер не принимает локаль ru_RU.UTF-8: %v", err)
	}
	// Проверяем, что сообщения действительно переведены: без каталогов
	// переводов PostgreSQL молча оставляет их английскими.
	if _, err := db.Exec(ctx, `CREATE TABLE _probe672 (a int)`); err != nil {
		t.Fatalf("проба: %v", err)
	}
	_, probeErr := db.Exec(ctx, `ALTER TABLE _probe672 ADD COLUMN a int`)
	if probeErr == nil {
		t.Fatal("проба: повторная колонка добавилась без ошибки")
	}
	if strings.Contains(strings.ToLower(probeErr.Error()), "already exists") {
		t.Skip("сервер собран без каталогов переводов — сообщения английские, окружение из #672 не воспроизводится")
	}
	if _, err := db.Exec(ctx, `DROP TABLE _probe672`); err != nil {
		t.Fatalf("уборка пробы: %v", err)
	}

	if err := auth.NewRepo(db).EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema на сервере с локализованными сообщениями: %v", err)
	}
}
