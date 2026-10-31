package query_test

import (
	"context"
	"os"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5"
)

func TestStrictPostgresMatrixIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	shared, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shared.Close(ctx) }()
	var superuser bool
	if err := shared.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&superuser); err != nil {
		t.Fatal(err)
	}
	if !superuser {
		t.Skip("database/role lifecycle assertion requires CI superuser")
	}
	const castCount = `SELECT count(*) FROM pg_cast WHERE castsource = 'uuid'::regtype AND casttarget = 'text'::regtype AND castcontext = 'i'`
	var before int
	if err := shared.QueryRow(ctx, castCount).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var database, role string
	t.Run("matrix", func(t *testing.T) {
		dbtest.ForEachDialectWithoutUUIDTextCast(t, func(t *testing.T, db *storage.DB) {
			if db.Dialect().Name() != "postgres" {
				return
			}
			rows, err := db.QueryAll(ctx, `SELECT current_database() AS database, current_user AS role, rolsuper FROM pg_roles WHERE rolname = current_user`)
			if err != nil {
				t.Fatal(err)
			}
			database = rows[0]["database"].(string)
			role = rows[0]["role"].(string)
			if database == shared.Config().Database || role == shared.Config().User || rows[0]["rolsuper"] != false {
				t.Fatal("strict matrix must use a private database and ordinary role")
			}
			// Execute SQL, so an accidental compatibility cast cannot make this
			// test green merely because the role is no longer a superuser.
			if _, err := db.QueryAll(ctx, `SELECT '00000000-0000-0000-0000-000000000000'::uuid = '00000000-0000-0000-0000-000000000000'::text`); err == nil {
				t.Fatal("strict database permits implicit uuid-to-text comparison")
			}
		})
	})
	if database == "" || role == "" {
		t.Fatal("PostgreSQL matrix did not run")
	}
	var after int
	if err := shared.QueryRow(ctx, castCount).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("strict matrix changed the shared database's compatibility cast")
	}
	var remains bool
	if err := shared.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1) OR EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $2)`, database, role).Scan(&remains); err != nil {
		t.Fatal(err)
	}
	if remains {
		t.Fatal("strict database/role survived matrix cleanup")
	}
}
