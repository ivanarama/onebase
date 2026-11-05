package dbtest

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5"
)

// Connect directly: storage.Connect would change the shared database's cast.
// Schema isolation alone cannot isolate pg_cast, which is database-wide.
func strictPostgresDSN(t *testing.T, ctx context.Context, dsn string) string {
	t.Helper()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("strict PostgreSQL admin connection: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	var superuser bool
	if err := admin.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&superuser); err != nil {
		t.Fatal(err)
	}
	if !superuser {
		// An ordinary role cannot create or replace the built-in uuid cast.
		var hasCast bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_cast WHERE castsource = 'uuid'::regtype AND casttarget = 'text'::regtype AND castcontext = 'i')`).Scan(&hasCast); err != nil {
			t.Fatal(err)
		}
		if hasCast {
			t.Fatal("strict PostgreSQL matrix requires an ordinary role in a database without the implicit uuid-to-text cast, or a superuser to provision a private database")
		}
		return dsn
	}

	name := storage.NewEphemeralSchemaName() + "_strict"
	role := name
	password := uuid.NewString()
	if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION PASSWORD '"+password+"'"); err != nil {
		t.Fatalf("create strict PostgreSQL role: %v", err)
	}
	// Registered before CREATE DATABASE so even partial setup removes the role.
	// The matrix's pool/schema cleanup is registered later and therefore runs first.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop strict PostgreSQL database: %v", err)
		}
		if _, err := admin.Exec(cleanupCtx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Errorf("drop strict PostgreSQL role: %v", err)
		}
	})
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER "+pgx.Identifier{role}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatalf("create strict PostgreSQL database: %v", err)
	}

	// Preserve transport/TLS/options for both supported pgx DSN forms. Names and
	// password are generated ASCII identifiers/UUIDs, never external SQL text.
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(role, password)
		u.Path = "/" + name
		u.RawPath = ""
		q := u.Query()
		// Query parameters override URL credentials/database in pgx.
		q.Del("user")
		q.Del("password")
		q.Del("dbname")
		q.Del("database")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " dbname=" + name + " user=" + role + " password=" + password
}
