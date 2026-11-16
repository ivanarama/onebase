package access_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

// The ambiguity guard sees physical source occurrences; the row masker sees
// restored logical output names. Neither phase may lose a long reference.
func TestQueryMaskLongReferenceOccurrencesMatrix(t *testing.T) {
	const field = "ЗащищённаяДлиннаяСсылкаДляПроверкиПолевойМаски"
	target := &metadata.Entity{Name: "НазначениеМаски", Kind: metadata.KindCatalog}
	owner := &metadata.Entity{Name: "ВладелецМаски", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: field, Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name}}}
	entities := []*metadata.Entity{target, owner}
	require.NoError(t, metadata.Validate(entities, nil))
	lookup := func(_, name string) *metadata.Entity {
		for _, e := range entities {
			if strings.EqualFold(e.Name, name) {
				return e
			}
		}
		return nil
	}
	user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{owner.Name: {"read"}, target.Name: {"read"}}, FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{owner.Name: {field: {Read: "hide"}}}}}}}}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		require.NoError(t, db.Migrate(ctx, entities))
		id := uuid.New()
		require.NoError(t, db.Upsert(ctx, target.Name, id, nil, target))
		require.NoError(t, db.Upsert(ctx, owner.Name, id, map[string]any{field: id}, owner))
		from := " ИЗ Справочник." + owner.Name + " КАК А ЛЕВОЕ СОЕДИНЕНИЕ Справочник." + owner.Name + " КАК Б ПО А.Ссылка=Б.Ссылка"
		for _, tc := range []struct {
			name, selection string
			denied          bool
			keys            []string
		}{
			{"selected-wildcard", "А.*", false, []string{strings.ToLower(field) + "_id"}},
			{"aliased-references", "А." + field + " КАК Первая, Б." + field + " КАК Вторая", false, []string{"первая", "вторая"}},
			{"star-and-reference", "А.*, А." + field, true, nil},
			{"duplicate-stars", "А.*, Б.*", true, nil},
			{"duplicate-alias", "А." + field + " КАК Дубль, Б." + field + " КАК Дубль", true, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile("ВЫБРАТЬ "+tc.selection+from, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
				require.NoError(t, err)
				owners := 0
				for _, src := range res.SourceOccurrences {
					if src.Name == owner.Name {
						owners++
					}
				}
				require.Equal(t, 2, owners, "self-JOIN occurrences must survive translation")
				plan := access.QueryMaskPlanFor(user, res, lookup)
				if tc.denied {
					require.NotEmpty(t, plan.Denied)
					return
				}
				require.Empty(t, plan.Denied)
				for _, limited := range []bool{false, true} {
					var rows []map[string]any
					if limited {
						rows, _, _, err = query.RunLimit(ctx, db, &res, 10)
					} else {
						rows, _, err = query.Run(ctx, db, &res)
					}
					require.NoError(t, err)
					require.Len(t, rows, 1)
					tracked, err := plan.ApplyTracked(rows)
					require.NoError(t, err)
					require.Len(t, tracked, 1)
					require.Len(t, tracked[0], len(tc.keys))
					for _, key := range tc.keys {
						require.Contains(t, rows[0], key)
						require.Nil(t, rows[0][key])
						require.Contains(t, tracked[0], key)
					}
					require.NotContains(t, rows[0], metadata.ColumnName(owner.Fields[0]))
				}
			})
		}
	})
}
