package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestLongReferenceOutputLabels_Matrix(t *testing.T) {
	names := []string{"Связь", "НейтральноеДлинноеПолеСсылкиДляПроверкиРезультата"}
	// Exercise the byte limit both before and after the three-byte _id suffix,
	// including odd byte lengths that mix Cyrillic and ASCII characters.
	for n := 60; n <= 66; n++ {
		name := strings.Repeat("Я", n/2)
		if n%2 != 0 {
			name += "X"
		}
		names = append(names, name)
	}
	for _, name := range names {
		t.Run(fmt.Sprintf("bytes-%d", len(name)), func(t *testing.T) {
			target := &metadata.Entity{Name: "Назначение", Kind: metadata.KindCatalog}
			owner := &metadata.Entity{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: name, Type: "reference:Назначение", RefEntity: target.Name}}}
			entities := []*metadata.Entity{target, owner}
			require.NoError(t, metadata.Validate(entities, nil))
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				require.NoError(t, db.Migrate(ctx, entities))
				targetID := uuid.New()
				require.NoError(t, db.Upsert(ctx, target.Name, targetID, nil, target))
				require.NoError(t, db.Upsert(ctx, owner.Name, uuid.New(), map[string]any{name: targetID}, owner))
				for _, tc := range []struct{ name, selection, key string }{
					{"qualified", "К." + name, strings.ToLower(name) + "_id"},
					{"parenthesized", "((К." + name + "))", strings.ToLower(name) + "_id"},
					{"alias", "К." + name + " КАК Проверка", "проверка"},
					{"union-first-reference", "К." + name + " ИЗ Справочник.Владелец КАК К ОБЪЕДИНИТЬ ВЫБРАТЬ Д." + name + " КАК Иное", strings.ToLower(name) + "_id"},
					{"derived-reference", "П." + name + " ИЗ (ВЫБРАТЬ К." + name + " ИЗ Справочник.Владелец КАК К) КАК П", strings.ToLower(name)},
					{"long-alias", "К." + name + " КАК НейтральныйДлинныйПсевдонимВыбраннойСсылки", strings.ToLower("НейтральныйДлинныйПсевдонимВыбраннойСсылки")},
				} {
					t.Run(tc.name, func(t *testing.T) {
						text := "ВЫБРАТЬ " + tc.selection
						if tc.name != "derived-reference" {
							alias := "К"
							if tc.name == "union-first-reference" {
								alias = "Д"
							}
							text += " ИЗ Справочник.Владелец КАК " + alias
						}
						res, err := query.Compile(text, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
						require.NoError(t, err)
						for range 2 {
							for _, limit := range []bool{false, true} {
								var rows []map[string]any
								var cols []string
								if limit {
									var truncated bool
									rows, cols, truncated, err = query.RunLimit(ctx, db, &res, 1)
									require.False(t, truncated)
								} else {
									rows, cols, err = query.Run(ctx, db, &res)
								}
								require.NoError(t, err, "SQL: %s", res.SQL)
								require.Equal(t, []string{tc.key}, cols)
								require.Len(t, rows, 1)
								require.Len(t, rows[0], 1)
								require.Equal(t, targetID.String(), fmt.Sprint(rows[0][tc.key]))
							}
						}
					})
				}
			})
		})
	}
}
