package query_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Регрессия #430: bare-Ссылка относится к основному источнику текущего
// SELECT и при явном JOIN должна быть квалифицирована его таблицей/алиасом.
func TestBareReference_WithExplicitJoin_ExecuteSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "reference-explicit-join.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	a := &metadata.Entity{
		Name:   "A",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	b := &metadata.Entity{
		Name:   "B",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	c := &metadata.Entity{
		Name:   "C",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	entities := []*metadata.Entity{a, b, c}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	aID := uuid.New()
	bID := uuid.New()
	cID := uuid.New()
	for _, item := range []struct {
		entity *metadata.Entity
		id     uuid.UUID
		name   string
	}{
		{a, aID, "A"},
		{b, bID, "B"},
		{c, cID, "C"},
	} {
		if err := db.Upsert(ctx, item.entity.Name, item.id,
			map[string]any{"наименование": item.name}, item.entity); err != nil {
			t.Fatalf("insert %s: %v", item.entity.Name, err)
		}
	}

	tests := []struct {
		name      string
		src       string
		wantSQL   string
		wantLinks []string
	}{
		{
			name:      "main source table",
			src:       `SELECT Reference FROM Catalog.A JOIN Catalog.B AS B ON 1 = 1`,
			wantSQL:   "SELECT a.id FROM",
			wantLinks: []string{aID.String()},
		},
		{
			name:      "main source alias",
			src:       `SELECT Reference FROM Catalog.A AS Main JOIN Catalog.B AS B ON 1 = 1`,
			wantSQL:   "SELECT main.id FROM",
			wantLinks: []string{aID.String()},
		},
		{
			name:      "derived main source alias",
			src:       `SELECT Reference FROM (SELECT Reference FROM Catalog.A) AS Main JOIN Catalog.B AS B ON 1 = 1`,
			wantSQL:   "SELECT main.id FROM",
			wantLinks: []string{aID.String()},
		},
		{
			name: "nested select has its own main source",
			src: `SELECT Reference,
				(SELECT Reference FROM Catalog.C AS InnerC JOIN Catalog.B AS InnerB ON 1 = 1) AS Nested
				FROM Catalog.A AS OuterA JOIN Catalog.B AS OuterB ON 1 = 1`,
			wantSQL:   "SELECT outera.id,(SELECT innerc.id FROM",
			wantLinks: []string{aID.String(), cID.String()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := query.Compile(tt.src, query.CompileOpts{
				Entities: entities,
				Dialect:  storage.SQLiteDialect{},
			})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if !strings.Contains(res.SQL, tt.wantSQL) {
				t.Fatalf("bare Reference is not qualified by the SELECT's main source:\n%s", res.SQL)
			}

			dest := make([]any, len(tt.wantLinks))
			values := make([]string, len(tt.wantLinks))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(dest...); err != nil {
				t.Fatalf("execute %q\nSQL: %s\nerror: %v", tt.src, res.SQL, err)
			}
			for i := range values {
				if values[i] != tt.wantLinks[i] {
					t.Fatalf("column %d = %q, want %q", i, values[i], tt.wantLinks[i])
				}
			}
		})
	}

	t.Run("virtual main source is not replaced by joined source", func(t *testing.T) {
		reg := &metadata.Register{
			Name:       "R",
			Dimensions: []metadata.Field{{Name: "D"}},
			Resources:  []metadata.Field{{Name: "X"}},
		}
		res, err := query.Compile(
			`SELECT Reference FROM AccumulationRegister.R.Balances() AS Bal JOIN Catalog.B AS B ON 1 = 1`,
			query.CompileOpts{
				Registers: []*metadata.Register{reg},
				Entities:  entities,
				Dialect:   storage.SQLiteDialect{},
			},
		)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !strings.Contains(res.SQL, "SELECT bal.id FROM") {
			t.Fatalf("joined source replaced the virtual main source:\n%s", res.SQL)
		}
	})
}
