package storage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func usersHeaderEntity() *metadata.Entity {
	return &metadata.Entity{Name: "UserHeader", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{ID: "name", Name: "Title", Type: metadata.FieldTypeString},
		{ID: "author", Name: "Author", Type: metadata.FieldType("reference:_users"), RefEntity: metadata.SystemUsersEntity},
	}}
}

func usersHeaderAccounts(t *testing.T, db *storage.DB) (*auth.Repo, string) {
	t.Helper()
	repo := auth.NewRepo(db)
	require.NoError(t, repo.EnsureSchema(t.Context()))
	// These tests exercise migration and deletion, not password authentication.
	// Public SQL seeds two accounts without spending time hashing unused passwords.
	owner := uuid.NewString()
	for i, id := range []string{uuid.NewString(), owner} {
		_, err := db.Exec(t.Context(), `INSERT INTO _users(id,login,password_hash,full_name,is_admin) VALUES (`+
			db.Dialect().Placeholder(1)+`,`+db.Dialect().Placeholder(2)+`,'unused','',`+db.Dialect().Placeholder(3)+`)`, id, fmt.Sprintf("account%d", i), i == 0)
		require.NoError(t, err)
	}
	return repo, owner
}

func usersHeaderProtection(t *testing.T, db *storage.DB, e *metadata.Entity, repo *auth.Repo, owner string) {
	t.Helper()
	ctx := t.Context()
	id := uuid.New()
	require.NoError(t, db.Upsert(ctx, e.Name, id, map[string]any{"Title": "kept", "Author": owner}, e))
	err := db.Upsert(ctx, e.Name, uuid.New(), map[string]any{"Title": "broken", "Author": uuid.NewString()}, e)
	require.ErrorIs(t, err, storage.ErrForeignKeyViolation)
	table, col := metadata.TableName(e.Name), metadata.ColumnName(e.Fields[1])
	_, err = db.Exec(ctx, `UPDATE "`+table+`" SET "`+col+`"=`+db.Dialect().Placeholder(1)+` WHERE id=`+db.Dialect().Placeholder(2), uuid.NewString(), id)
	require.True(t, storage.IsForeignKeyViolation(err), "invalid SQL update accepted: %v", err)
	_, err = db.Exec(ctx, `INSERT INTO "`+table+`" (id,"`+col+`") VALUES (`+db.Dialect().Placeholder(1)+`,`+db.Dialect().Placeholder(2)+`)`, uuid.New(), uuid.NewString())
	require.True(t, storage.IsForeignKeyViolation(err), "invalid SQL insert accepted: %v", err)
	require.ErrorIs(t, repo.Delete(ctx, owner), auth.ErrUserReferenced)
	_, err = db.Exec(ctx, `DELETE FROM _users WHERE id=`+db.Dialect().Placeholder(1), owner)
	require.True(t, storage.IsForeignKeyViolation(err), "direct user deletion accepted: %v", err)
	var got string
	require.NoError(t, db.QueryRow(ctx, `SELECT "`+col+`" FROM "`+table+`" WHERE id=`+db.Dialect().Placeholder(1), id).Scan(&got))
	require.Equal(t, owner, got)
	require.NoError(t, db.Upsert(ctx, e.Name, id, map[string]any{"Author": nil}, e))
	require.NoError(t, repo.Delete(ctx, owner))
}

func TestUsersReferenceHeaderFreshMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		repo, owner := usersHeaderAccounts(t, db)
		e := usersHeaderEntity()
		require.NoError(t, db.Migrate(t.Context(), []*metadata.Entity{e}))
		require.NoError(t, db.Migrate(t.Context(), []*metadata.Entity{e}))
		usersHeaderProtection(t, db, e, repo, owner)
	})
}

func usersHeaderPG(t *testing.T) *storage.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	schema := storage.NewEphemeralSchemaName()
	db, err := storage.ConnectWithSchema(t.Context(), dsn, schema)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	require.NoError(t, db.CreateSchema(t.Context(), schema))
	t.Cleanup(func() { require.NoError(t, db.DropSchemaCascade(context.Background(), schema)) })
	require.NoError(t, db.EnsureServiceSchema(t.Context()))
	return db
}

func TestUsersReferenceHeaderEvolutionPostgres(t *testing.T) {
	for _, mode := range []string{"added", "legacy", "retype", "rename", "without-id", "equivalent"} {
		t.Run(mode, func(t *testing.T) {
			db := usersHeaderPG(t)
			repo, owner := usersHeaderAccounts(t, db)
			ctx := t.Context()
			e := usersHeaderEntity()
			before := usersHeaderEntity()
			switch mode {
			case "added":
				before.Fields = before.Fields[:1]
			case "retype", "rename":
				before.Fields[1].Type, before.Fields[1].RefEntity = metadata.FieldTypeString, ""
				if mode == "rename" {
					before.Fields[1].Name = "Previous"
				}
			}
			if mode == "without-id" {
				for i := range e.Fields {
					e.Fields[i].ID, before.Fields[i].ID = "", ""
				}
			}
			require.NoError(t, db.Migrate(ctx, []*metadata.Entity{before}))
			table := metadata.TableName(e.Name)
			if mode == "legacy" || mode == "without-id" || mode == "equivalent" {
				_, err := db.Exec(ctx, `ALTER TABLE "`+table+`" DROP CONSTRAINT "`+table+`_author_id_fkey"`)
				require.NoError(t, err)
			}
			if mode == "equivalent" {
				_, err := db.Exec(ctx, `ALTER TABLE "`+table+`" ADD CONSTRAINT externally_named FOREIGN KEY(author_id) REFERENCES _users(id) ON DELETE RESTRICT`)
				require.NoError(t, err)
			}
			if mode != "added" {
				_, err := db.Exec(ctx, `INSERT INTO "`+table+`"(id,"`+metadata.ColumnName(before.Fields[1])+`") VALUES($1,$2)`, uuid.New(), owner)
				require.NoError(t, err)
			}
			indexWhere := " WHERE title IS NOT NULL"
			if mode == "retype" || mode == "rename" {
				indexWhere = ""
			}
			_, err := db.Exec(ctx, `CREATE INDEX external_header_index ON "`+table+`"(title)`+indexWhere)
			require.NoError(t, err)
			require.NoError(t, db.Migrate(ctx, []*metadata.Entity{e}))
			var oid string
			require.NoError(t, db.QueryRow(ctx, `SELECT oid::text FROM pg_constraint WHERE conrelid=$1::regclass AND contype='f'`, table).Scan(&oid))
			require.NoError(t, db.Migrate(ctx, []*metadata.Entity{e}))
			var again string
			require.NoError(t, db.QueryRow(ctx, `SELECT oid::text FROM pg_constraint WHERE conrelid=$1::regclass AND contype='f'`, table).Scan(&again))
			require.Equal(t, oid, again, "repeat migrate must retain the constraint")
			var present bool
			require.NoError(t, db.QueryRow(ctx, `SELECT to_regclass('external_header_index') IS NOT NULL`).Scan(&present))
			require.True(t, present)
			// Remove only this fixture's earlier row before exercising detach/delete.
			_, err = db.Exec(ctx, `DELETE FROM "`+table+`"`)
			require.NoError(t, err)
			usersHeaderProtection(t, db, e, repo, owner)
		})
	}
}

func TestUsersReferenceHeaderRefusalPostgres(t *testing.T) {
	for _, mode := range []string{"broken", "malformed", "incompatible", "unvalidated", "disabled", "late-ddl"} {
		t.Run(mode, func(t *testing.T) {
			db := usersHeaderPG(t)
			_, owner := usersHeaderAccounts(t, db)
			ctx := t.Context()
			before := usersHeaderEntity()
			before.Fields[1].Type, before.Fields[1].RefEntity = metadata.FieldTypeString, ""
			require.NoError(t, db.Migrate(ctx, []*metadata.Entity{before}))
			table := metadata.TableName(before.Name)
			value := owner
			switch mode {
			case "broken":
				value = uuid.NewString()
			case "malformed":
				value = "private-invalid-user-value"
			}
			id := uuid.New()
			_, err := db.Exec(ctx, `INSERT INTO "`+table+`"(id,author) VALUES($1,$2)`, id, value)
			require.NoError(t, err)
			e := usersHeaderEntity()
			if mode == "incompatible" || mode == "unvalidated" || mode == "disabled" {
				_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" RENAME COLUMN author TO author_id`)
				require.NoError(t, err)
				_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" ALTER COLUMN author_id TYPE uuid USING author_id::uuid`)
				require.NoError(t, err)
				suffix := ""
				if mode == "incompatible" {
					suffix = " ON DELETE CASCADE"
				}
				if mode == "unvalidated" {
					suffix = " NOT VALID"
				}
				_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" ADD CONSTRAINT wrong FOREIGN KEY(author_id) REFERENCES _users(id)`+suffix)
				require.NoError(t, err)
				if mode == "disabled" {
					_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" DISABLE TRIGGER ALL`)
					require.NoError(t, err)
				}
				// The legacy fixture's map must describe its actual physical column.
				_, err = db.Exec(ctx, `UPDATE _schema_fields SET column_name='author_id',field_type='reference:_users' WHERE table_name=$1 AND field_id='author'`, table)
				require.NoError(t, err)
			}
			if mode == "late-ddl" {
				name := storage.ForeignKeyName(table, "author_id", "_users")
				_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" ADD CONSTRAINT "`+name+`" CHECK (title IS NULL OR title <> 'blocked')`)
				require.NoError(t, err)
			}
			// This add must roll back together with any rename/retype/map changes.
			e.Fields = append(e.Fields, metadata.Field{ID: "new", Name: "NewField", Type: metadata.FieldTypeString})
			var schemaBefore, mapBefore string
			require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(attname||':'||atttypid::text,',' ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped`, table).Scan(&schemaBefore))
			require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(field_id||':'||column_name||':'||field_type,',' ORDER BY field_id) FROM _schema_fields WHERE table_name=$1`, table).Scan(&mapBefore))
			err = db.Migrate(ctx, []*metadata.Entity{e})
			require.Error(t, err)
			require.NotContains(t, err.Error(), value, "do not leak the referenced value")
			if mode == "broken" || mode == "malformed" {
				require.Contains(t, err.Error(), id.String())
				require.Contains(t, strings.ToLower(err.Error()), "userheader")
			}
			var schemaAfter, mapAfter string
			require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(attname||':'||atttypid::text,',' ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped`, table).Scan(&schemaAfter))
			require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(field_id||':'||column_name||':'||field_type,',' ORDER BY field_id) FROM _schema_fields WHERE table_name=$1`, table).Scan(&mapAfter))
			require.Equal(t, schemaBefore, schemaAfter)
			require.Equal(t, mapBefore, mapAfter)
			var rows int
			require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM "`+table+`" WHERE id=$1`, id).Scan(&rows))
			require.Equal(t, 1, rows)
			require.False(t, errors.Is(err, context.Canceled))
		})
	}
}

func TestUsersReferenceHeaderConcurrentWriterPostgres(t *testing.T) {
	for _, cancelMigration := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelMigration), func(t *testing.T) {
			db := usersHeaderPG(t)
			usersHeaderAccounts(t, db)
			before := usersHeaderEntity()
			before.Fields[1].Type, before.Fields[1].RefEntity = metadata.FieldTypeString, ""
			require.NoError(t, db.Migrate(t.Context(), []*metadata.Entity{before}))
			var schema string
			require.NoError(t, db.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&schema))
			conn, err := pgx.Connect(t.Context(), os.Getenv("TEST_DATABASE_URL"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close(context.Background())) })
			tx, err := conn.Begin(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
			_, err = tx.Exec(t.Context(), `LOCK TABLE `+pgx.Identifier{schema, "_users"}.Sanitize()+` IN SHARE MODE`)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- db.Migrate(ctx, []*metadata.Entity{usersHeaderEntity()}) }()
			// Observe an actual lock wait, rather than guessing timing with sleep.
			require.Eventually(t, func() bool {
				var waiting bool
				err := db.QueryRow(t.Context(), `SELECT EXISTS (
 SELECT 1 FROM pg_locks WHERE relation=$1::regclass AND mode='ShareRowExclusiveLock' AND NOT granted
)`, schema+"._users").Scan(&waiting)
				return err == nil && waiting
			}, 5*time.Second, 10*time.Millisecond)
			id, broken := uuid.New(), uuid.NewString()
			_, err = db.Exec(t.Context(), `INSERT INTO userheader(id,author) VALUES($1,$2)`, id, broken)
			require.NoError(t, err, "legacy writer must complete before the source is locked")
			if cancelMigration {
				cancel()
			} else {
				require.NoError(t, tx.Commit(t.Context()))
			}
			err = <-finished
			require.Error(t, err)
			if cancelMigration {
				require.ErrorIs(t, err, context.Canceled)
				require.NoError(t, tx.Commit(t.Context()))
			} else {
				require.Contains(t, err.Error(), id.String())
				require.NotContains(t, err.Error(), broken)
			}
			var got string
			require.NoError(t, db.QueryRow(t.Context(), `SELECT author FROM userheader WHERE id=$1`, id).Scan(&got))
			require.Equal(t, broken, got, "no automatic repair after refusal/cancellation")
			var renamed bool
			require.NoError(t, db.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='userheader' AND column_name='author_id')`).Scan(&renamed))
			require.False(t, renamed)
		})
	}
}

func TestUsersReferenceHeaderConcurrentMigrateAndDeletePostgres(t *testing.T) {
	for _, operation := range []string{"migrate", "delete"} {
		t.Run(operation, func(t *testing.T) {
			db := usersHeaderPG(t)
			_, owner := usersHeaderAccounts(t, db)
			e := usersHeaderEntity()
			require.NoError(t, db.Migrate(t.Context(), []*metadata.Entity{e}))
			_, err := db.Exec(t.Context(), `ALTER TABLE userheader DROP CONSTRAINT userheader_author_id_fkey`)
			require.NoError(t, err)
			require.NoError(t, db.Upsert(t.Context(), e.Name, uuid.New(), map[string]any{"Author": owner}, e))
			var schema string
			require.NoError(t, db.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&schema))
			other, err := storage.ConnectWithSchema(t.Context(), os.Getenv("TEST_DATABASE_URL"), schema)
			require.NoError(t, err)
			t.Cleanup(other.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start, first, second := make(chan struct{}), make(chan error, 1), make(chan error, 1)
			go func() { <-start; first <- db.Migrate(ctx, []*metadata.Entity{e}) }()
			go func() {
				<-start
				if operation == "migrate" {
					second <- other.Migrate(ctx, []*metadata.Entity{e})
				} else {
					second <- auth.NewRepo(other).Delete(ctx, owner)
				}
			}()
			close(start)
			migrateErr, otherErr := <-first, <-second
			if operation == "migrate" {
				require.NoError(t, migrateErr)
				require.NoError(t, otherErr)
				var count int
				require.NoError(t, db.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE conrelid='userheader'::regclass AND contype='f' AND convalidated`).Scan(&count))
				require.Equal(t, 1, count)
			} else if migrateErr == nil {
				require.ErrorIs(t, otherErr, auth.ErrUserReferenced)
			} else {
				require.NoError(t, otherErr)
				require.Contains(t, migrateErr.Error(), "битых ссылок")
			}
		})
	}
}

// A failed header migration must also undo the legacy preparation that precedes
// FK validation, including physical names and the persistent field map.
func TestUsersReferenceHeaderLegacyNormalizationRollbackPostgres(t *testing.T) {
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		for _, mode := range []string{"broken", "late-ddl"} {
			t.Run(string(kind)+"/"+mode, func(t *testing.T) {
				db := usersHeaderPG(t)
				_, owner := usersHeaderAccounts(t, db)
				ctx := t.Context()
				e := usersHeaderEntity()
				e.Kind = kind
				longField := metadata.Field{ID: "long", Name: strings.Repeat("Description", 8), Type: metadata.FieldTypeString}
				e.Fields = append(e.Fields, longField)
				require.NoError(t, db.Migrate(ctx, []*metadata.Entity{e}))
				table := metadata.TableName(e.Name)
				_, err := db.Exec(ctx, `ALTER TABLE "`+table+`" DROP CONSTRAINT "`+table+`_author_id_fkey"`)
				require.NoError(t, err)
				current, legacy := metadata.ColumnName(longField), strings.ToLower(longField.Name)[:63]
				_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" RENAME COLUMN "`+current+`" TO "`+legacy+`"`)
				require.NoError(t, err)
				_, err = db.Exec(ctx, `UPDATE _schema_fields SET column_name=$1 WHERE table_name=$2 AND field_id='long'`, legacy, table)
				require.NoError(t, err)
				value := owner
				if mode == "broken" {
					value = uuid.NewString()
				} else {
					name := storage.ForeignKeyName(table, "author_id", "_users")
					_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" ADD CONSTRAINT "`+name+`" CHECK (title IS NULL OR title <> 'blocked')`)
					require.NoError(t, err)
				}
				id := uuid.New()
				_, err = db.Exec(ctx, `INSERT INTO "`+table+`"(id,author_id,"`+legacy+`") VALUES($1,$2,'kept')`, id, value)
				require.NoError(t, err)
				snapshot := func() []string {
					var schema, fields, rows string
					require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(attname||':'||atttypid::text,',' ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped`, table).Scan(&schema))
					require.NoError(t, db.QueryRow(ctx, `SELECT string_agg(field_id||':'||column_name||':'||field_type,',' ORDER BY field_id) FROM _schema_fields WHERE table_name=$1`, table).Scan(&fields))
					require.NoError(t, db.QueryRow(ctx, `SELECT row_to_json(t)::text FROM "`+table+`" t WHERE id=$1`, id).Scan(&rows))
					return []string{schema, fields, rows}
				}
				before := snapshot()
				err = db.Migrate(ctx, []*metadata.Entity{e})
				require.Error(t, err)
				if mode == "broken" {
					require.Contains(t, err.Error(), "битых ссылок на _users: 1")
				}
				require.Equal(t, before, snapshot(), "refusal must preserve names, field map and rows")
				// Explicit repair permits normalization and installs the real FK.
				if mode == "broken" {
					_, err = db.Exec(ctx, `UPDATE "`+table+`" SET author_id=$1 WHERE id=$2`, owner, id)
				} else {
					_, err = db.Exec(ctx, `ALTER TABLE "`+table+`" DROP CONSTRAINT "`+storage.ForeignKeyName(table, "author_id", "_users")+`"`)
				}
				require.NoError(t, err)
				require.NoError(t, db.Migrate(ctx, []*metadata.Entity{e}))
				require.NoError(t, db.Migrate(ctx, []*metadata.Entity{e}))
				var kept string
				require.NoError(t, db.QueryRow(ctx, `SELECT "`+current+`" FROM "`+table+`" WHERE id=$1`, id).Scan(&kept))
				require.Equal(t, "kept", kept)
				_, err = db.Exec(ctx, `UPDATE "`+table+`" SET author_id=$1 WHERE id=$2`, uuid.NewString(), id)
				require.True(t, storage.IsForeignKeyViolation(err), "repaired migration must install FK: %v", err)
			})
		}
	}
}
