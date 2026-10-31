package storage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestList_HierarchyIndexedLimitedSQL(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "HierarchyIndexed", Kind: metadata.KindCatalog, Hierarchical: true, Fields: []metadata.Field{{Name: "Code", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		table := metadata.TableName(ent.Name)
		col := metadata.ColumnName(ent.Fields[0])
		if _, err := db.Exec(ctx, "CREATE INDEX hierarchy_code_idx ON "+table+" ("+col+")"); err != nil {
			t.Fatal(err)
		}
		// Enough rows that LIMIT is meaningful; this test has no time threshold.
		for i := 511; i >= 0; i-- {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Code": fmt.Sprintf("%04d", i), "is_folder": i%2 == 1}, ent); err != nil {
				t.Fatal(err)
			}
		}
		capture := storage.InstallListSQLCaptureForTest(t, db)
		for _, tc := range []struct {
			name          string
			params        storage.ListParams
			metadataOrder bool
			want          []string
			foldersFirst  bool
		}{
			{"exclude", storage.ListParams{ExcludeFolders: true, Limit: 3}, false, []string{"0000", "0002", "0004"}, false},
			{"only folders", storage.ListParams{OnlyFolders: true, Limit: 3}, false, []string{"0001", "0003", "0005"}, false},
			{"mixed", storage.ListParams{Limit: 3}, false, []string{"0001", "0003", "0005"}, true},
			{"metadata exclude", storage.ListParams{ExcludeFolders: true, Limit: 3}, true, []string{"0000", "0002", "0004"}, false},
			{"metadata only folders", storage.ListParams{OnlyFolders: true, Limit: 3}, true, []string{"0001", "0003", "0005"}, false},
			{"metadata mixed", storage.ListParams{Limit: 3}, true, []string{"0001", "0003", "0005"}, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ent.OrderBy = nil
				if tc.metadataOrder {
					ent.OrderBy = []string{"Code"}
				}
				before := len(capture.Queries())
				rows, err := db.List(ctx, ent.Name, ent, tc.params)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != len(tc.want) {
					t.Fatalf("List count=%d, want %d", len(rows), len(tc.want))
				}
				for i, want := range tc.want {
					if rows[i]["Code"] != want {
						t.Errorf("row %d=%v, want %s", i, rows[i]["Code"], want)
					}
				}
				var sqlText string
				var args []any
				for _, q := range capture.Queries()[before:] {
					if strings.HasPrefix(q.SQL, "SELECT ") && strings.Contains(q.SQL, " FROM "+table+" ") {
						if sqlText != "" {
							t.Fatal("unexpected multiple List statements")
						}
						sqlText = q.SQL
						args = q.Args
					}
				}
				if sqlText == "" {
					t.Fatal("did not observe executed List SELECT")
				}
				_, order, ok := strings.Cut(sqlText, " ORDER BY ")
				if !ok {
					t.Fatalf("no ORDER BY: %s", sqlText)
				}
				if strings.Contains(order, "is_folder") != tc.foldersFirst {
					t.Errorf("redundant/missing folder sort: %s", order)
				}
				if !strings.Contains(order, col) || !strings.HasSuffix(sqlText, " LIMIT 3 OFFSET 0") {
					t.Errorf("missing indexed key or LIMIT: %s", sqlText)
				}
				// Explain the exact observed SQL with its real bound values. Assert
				// structural properties, not a whole optimizer/version-specific plan.
				if !tc.metadataOrder && !tc.foldersFirst {
					if db.Dialect().Name() == "sqlite" {
						plan, err := db.QueryAll(ctx, "EXPLAIN QUERY PLAN "+sqlText, args...)
						if err != nil {
							t.Fatal(err)
						}
						var details string
						for _, row := range plan {
							details += " " + fmt.Sprint(row["detail"])
						}
						if !strings.Contains(details, "hierarchy_code_idx") || strings.Contains(strings.ToUpper(details), "TEMP B-TREE") {
							t.Errorf("List lost index order: %s", details)
						}
					} else {
						// SQL assertions above are the stable optimization contract on PG;
						// the optimizer may legitimately select a different scan by size.
						plan, err := db.QueryAll(ctx, "EXPLAIN (FORMAT JSON) "+sqlText, args...)
						if err != nil || len(plan) != 1 {
							t.Fatalf("EXPLAIN: rows=%v err=%v", plan, err)
						}
					}
				}
			})
		}
	})
}
