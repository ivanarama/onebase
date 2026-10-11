package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUpsertTablePartFKDiagnosisMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target, entity := fkDiagnosisEntities()
		tp := metadata.TablePart{Name: "Ссылки", Fields: entity.Fields[1:]}
		entity.TableParts = []metadata.TablePart{tp}
		if err := db.Migrate(ctx, []*metadata.Entity{target, entity}); err != nil {
			t.Fatal(err)
		}

		// Default migrations constrain only parent_id. Model an existing schema
		// with real reference constraints without changing the migration contract.
		if _, err := db.Exec(ctx, "DROP TABLE "+metadata.TablePartTableName(entity.Name, tp.Name)); err != nil {
			t.Fatal(err)
		}
		ddl := strings.TrimSuffix(storage.CreateTablePartSQL(db.Dialect(), entity, tp), "\n)")
		for _, f := range tp.Fields {
			ddl += ", FOREIGN KEY (" + metadata.ColumnName(f) + ") REFERENCES " + metadata.TableName(f.RefEntity) + "(id)"
		}
		if _, err := db.Exec(ctx, ddl+"\n)"); err != nil {
			t.Fatal(err)
		}

		known, parent := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, target.Name, known, map[string]any{"Наименование": "Известный"}, target); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, entity.Name, parent, map[string]any{"Наименование": "Исходный"}, entity); err != nil {
			t.Fatal(err)
		}
		original := []map[string]any{{"Бренд": known.String()}}
		for _, inTx := range []bool{false, true} {
			name := "without-transaction"
			if inTx {
				name = "inside-transaction"
			}
			t.Run(name, func(t *testing.T) {
				for _, tc := range []struct {
					name                     string
					allBroken, missingParent bool
				}{
					{name: "one-broken"}, {name: "all-broken", allBroken: true}, {name: "missing-parent", missingParent: true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						if err := db.UpsertTablePartRows(ctx, entity.Name, tp.Name, parent, original, tp); err != nil {
							t.Fatal(err)
						}
						writeCtx := ctx
						var tx storage.Tx
						if inTx {
							var err error
							tx, writeCtx, err = db.BeginTx(ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := tx.Rollback(ctx); err != nil {
									t.Errorf("rollback: %v", err)
								}
							}()
						}
						missing, second := uuid.New(), known
						if tc.allBroken {
							second = uuid.New()
						}
						writeParent := parent
						rows := []map[string]any{{"Бренд": known.String()}, {"Бренд": missing.String(), "Сегмент": second.String()}}
						rowNumber := "row 2"
						if tc.missingParent {
							writeParent = uuid.New()
							rows = []map[string]any{{"Бренд": known.String()}}
							rowNumber = "row 1"
						}
						err := db.UpsertTablePartRows(writeCtx, entity.Name, tp.Name, writeParent, rows, tp)
						if !errors.Is(err, storage.ErrForeignKeyViolation) {
							t.Fatalf("got %v, want ErrForeignKeyViolation", err)
						}
						msg := err.Error()
						if !strings.Contains(msg, entity.Name+"."+tp.Name+" "+rowNumber) {
							t.Errorf("missing table-part/row context: %s", msg)
						}
						if tc.missingParent {
							if strings.Contains(msg, "поле ") {
								t.Errorf("valid reference named broken: %s", msg)
							}
						} else {
							for _, want := range []string{"поле Бренд → " + target.Name, missing.String()} {
								if !strings.Contains(msg, want) {
									t.Errorf("missing %q: %s", want, msg)
								}
							}
							if tc.allBroken {
								for _, want := range []string{"поле Сегмент → " + target.Name, second.String()} {
									if !strings.Contains(msg, want) {
										t.Errorf("missing %q: %s", want, msg)
									}
								}
							} else if strings.Contains(msg, "Сегмент") || strings.Contains(msg, known.String()) {
								t.Errorf("valid reference named broken: %s", msg)
							}
						}
						if inTx {
							// A diagnostic SELECT after the failed INSERT must still work on PG.
							var one int
							if err := db.QueryRow(writeCtx, "SELECT 1").Scan(&one); err != nil || one != 1 {
								t.Fatalf("transaction aborted: value=%d err=%v", one, err)
							}
						}
					})
					if inTx {
						rows, err := db.GetTablePartRows(ctx, entity.Name, tp.Name, parent, tp)
						if err != nil {
							t.Fatal(err)
						}
						if len(rows) != 1 || rows[0]["Бренд"] != known.String() {
							t.Fatalf("rollback did not restore original rows: %#v", rows)
						}
					}
				}
			})
		}
		// Ordinary success still accepts known and empty references.
		if err := db.UpsertTablePartRows(ctx, entity.Name, tp.Name, parent, original, tp); err != nil {
			t.Fatal(err)
		}
		rows, err := db.GetTablePartRows(ctx, entity.Name, tp.Name, parent, tp)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0]["Бренд"] != known.String() || rows[0]["Сегмент"] != nil {
			t.Fatalf("successful write changed: %#v", rows)
		}
	})
}
