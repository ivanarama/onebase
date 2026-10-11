package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

func userReferenceProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: user-reference-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Сотрудники.yaml", `name: Сотрудники
fields:
  - name: Наименование
    type: string
  - name: УчетнаяЗапись
    type: reference:_users
`)
	return dir
}

// Reopen exactly the matrix database through the registered CLI handlers.
// PostgreSQL uses only the ephemeral schema, so a public._users table cannot
// hide missing auth bootstrap or receive test data.
func userReferenceCLICommand(t *testing.T, db *storage.DB, dir string, testRunner bool) *cobra.Command {
	t.Helper()
	cmd := migrateCmdFor(t, dir, db.SQLitePath(), nil)
	if testRunner {
		cmd = &cobra.Command{}
		addBaseFlags(cmd)
		cmd.Flags().String("run", "", "")
		cmd.Flags().String("isolation", "transaction", "")
		cmd.Flags().String("format", "pretty", "")
		cmd.Flags().String("out", "", "")
		for flag, value := range map[string]string{"project": dir, "sqlite": db.SQLitePath()} {
			if err := cmd.Flags().Set(flag, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if db.Dialect().Name() == "postgres" {
		var schema string
		if err := db.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		dsn := os.Getenv("TEST_DATABASE_URL")
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			dsn = u.String()
		} else {
			dsn += " search_path=" + schema
		}
		if err := cmd.Flags().Set("sqlite", ""); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Flags().Set("db", dsn); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

func userReferenceTableCount(t *testing.T, db *storage.DB) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('_users', '_sessions', 'сотрудники')"
	if db.Dialect().Name() == "postgres" {
		q = "SELECT COUNT(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename IN ('_users', '_sessions', 'сотрудники')"
	}
	var count int
	if err := db.QueryRow(context.Background(), q).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestMigrateUserReferencesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := userReferenceProject(t)
		if count := userReferenceTableCount(t, db); count != 0 {
			t.Fatalf("fresh fixture has %d auth/entity tables", count)
		}
		migrate := func() {
			t.Helper()
			if _, err := captureStdout(t, func() error { return migrateCmd.RunE(userReferenceCLICommand(t, db, dir, false), nil) }); err != nil {
				t.Fatalf("onebase migrate: %v", err)
			}
		}
		migrate()
		ctx := context.Background()
		userID := uuid.NewString()
		d := db.Dialect()
		q := fmt.Sprintf("INSERT INTO _users (id, login, password_hash) VALUES (%s, %s, %s)", d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		if _, err := db.Exec(ctx, q, userID, "reference-test", []byte("unused")); err != nil {
			t.Fatalf("create user in migrated schema: %v", err)
		}
		proj, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer proj.Close()
		ent := proj.Entities[0]
		id := uuid.New()
		if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Иван", "УчетнаяЗапись": userID}, ent); err != nil {
			t.Fatalf("write valid reference: %v", err)
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Несуществующий", "УчетнаяЗапись": uuid.NewString()}, ent); err == nil {
			t.Fatal("migration did not enforce the user foreign key")
		}
		migrate()
		row, err := db.GetByID(ctx, ent.Name, id, ent)
		if err != nil {
			t.Fatal(err)
		}
		if reference := row["УчетнаяЗапись"]; reference != userID {
			t.Fatalf("repeat migration changed user reference: %v", reference)
		}
		var count int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM _users").Scan(&count); err != nil || count != 1 {
			t.Fatalf("repeat migration changed users: count=%d err=%v", count, err)
		}
	})
}

func TestTestUserReferencesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := userReferenceProject(t)
		writeProcrunFixture(t, dir, "processors/Проверка.yaml", "name: Проверка\nkind: test\n")
		writeProcrunFixture(t, dir, "src/Проверка.proc.os", `Процедура Выполнить()
    Сотрудник = Справочники.Сотрудники.Создать();
    Сотрудник.Наименование = "Без учетной записи";
    Сотрудник.Записать();
    Утверждать.Заполнено(Сотрудник.Ссылка, "Запись с пустой ссылкой на пользователя");
КонецПроцедуры
`)
		if count := userReferenceTableCount(t, db); count != 0 {
			t.Fatalf("fresh fixture has %d auth/entity tables", count)
		}
		for i := 0; i < 2; i++ {
			if err := testCmd.RunE(userReferenceCLICommand(t, db, dir, true), nil); err != nil {
				t.Fatalf("onebase test, run %d: %v", i+1, err)
			}
		}
		if count := userReferenceTableCount(t, db); count != 3 {
			t.Fatalf("test command created %d auth/entity tables, want 3", count)
		}
	})
}

func TestMigrateUserReferencesDryRunMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := userReferenceProject(t)
		cmd := userReferenceCLICommand(t, db, dir, false)
		if err := cmd.Flags().Set("dry-run", "true"); err != nil {
			t.Fatal(err)
		}
		if count := userReferenceTableCount(t, db); count != 0 {
			t.Fatalf("fresh fixture has %d auth/entity tables", count)
		}
		if _, err := captureStdout(t, func() error { return migrateCmd.RunE(cmd, nil) }); err != nil {
			t.Fatalf("onebase migrate --dry-run: %v", err)
		}
		if count := userReferenceTableCount(t, db); count != 0 {
			t.Fatalf("dry-run created %d auth/entity tables", count)
		}
	})
}
