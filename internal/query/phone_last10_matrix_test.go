package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestPhoneLast10IndexesThroughQueryMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		tasks := &metadata.Entity{Name: "TaskPhones", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Phone", Type: metadata.FieldTypeString},
			{Name: "Alternate", Type: metadata.FieldTypeString},
		}}

		claimPath := filepath.Join(t.TempDir(), "ClaimPhones.yaml")
		require.NoError(t, os.WriteFile(claimPath, []byte(`name: ClaimPhones
fields:
  - {name: Label, type: string}
  - {name: Mobile, type: string}
  - {name: Home, type: string}
  - {name: Work, type: string}
  - {name: Closed, type: bool}
indexes:
  - {fields: [Mobile], normalizer: phone_last10}
  - {fields: [Home], normalizer: phone_last10}
  - {fields: [Work], normalizer: phone_last10}
`), 0600))
		claims, err := metadata.LoadFile(claimPath, metadata.KindDocument)
		require.NoError(t, err)
		phoneIndexes := claims.Indexes
		require.Len(t, phoneIndexes, 3)
		claims.Indexes = nil
		entities := []*metadata.Entity{tasks, claims}
		require.NoError(t, db.Migrate(ctx, entities))

		valid, mixed, invalid := uuid.New(), uuid.New(), uuid.New()
		for _, task := range []struct {
			id               uuid.UUID
			phone, alternate string
		}{
			{valid, "+7 (901) 222-01-00", "8 (902) 333-44-00"},
			{mixed, "abc9012220100", "9023334400"},
			{invalid, "abc9012220100", "19023334400"},
		} {
			require.NoError(t, db.Upsert(ctx, tasks.Name, task.id, map[string]any{"Phone": task.phone, "Alternate": task.alternate}, tasks))
		}
		for _, candidate := range []struct {
			label, mobile, home, work string
			closed                    bool
		}{
			{"mobile", "8 (901) 222-01-00", "", "", false},
			{"home", "", "901.222.01.00", "", false},
			{"work", "", "", "9023334400", false},
			{"both", "9012220100", "+79023334400", "", false},
			{"invalid", "abc9012220100", "19023334400", "", false},
			{"closed", "9012220100", "", "", true},
		} {
			require.NoError(t, db.Upsert(ctx, claims.Name, uuid.New(), map[string]any{
				"Label": candidate.label, "Mobile": candidate.mobile, "Home": candidate.home, "Work": candidate.work, "Closed": candidate.closed,
			}, claims))
		}

		idExpr, phoneExpr := "printf('00000000-0000-4000-8000-%012d', n)", "printf('8 (905) %06d0', n)"
		if db.IsPostgres() {
			idExpr = "('00000000-0000-4000-8000-' || lpad(n::text,12,'0'))::uuid"
			phoneExpr = "'8 (905) ' || lpad(n::text,6,'0') || '0'"
		}
		table := metadata.TableName(claims.Name)
		_, err = db.Exec(ctx, "WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<100000) "+
			"INSERT INTO "+table+"(id,mobile,home,work,closed) SELECT "+idExpr+","+phoneExpr+","+phoneExpr+","+phoneExpr+",FALSE FROM seq")
		require.NoError(t, err)

		_, err = db.Exec(ctx, "CREATE INDEX claim_label_external ON "+table+" (label)")
		require.NoError(t, err)
		claims.Indexes = phoneIndexes
		require.NoError(t, db.Migrate(ctx, entities))
		require.NoError(t, db.Migrate(ctx, entities))
		_, err = db.Exec(ctx, "ANALYZE "+table)
		require.NoError(t, err)

		src := `ВЫБРАТЬ З.Label КАК Метка ИЗ Документ.ClaimPhones КАК З
			ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.TaskPhones КАК Т ПО Т.Ссылка = &Задача
				И (НОРМАЛИЗОВАТЬТЕЛЕФОН(Т.Phone) ЕСТЬ НЕ NULL ИЛИ PHONE_LAST10(Т.Alternate) ЕСТЬ НЕ NULL)
			ГДЕ З.Closed = ЛОЖЬ И (
				PHONE_LAST10(З.Mobile) В (PHONE_LAST10(Т.Phone), PHONE_LAST10(Т.Alternate))
				ИЛИ PHONE_LAST10(З.Home) В (PHONE_LAST10(Т.Phone), PHONE_LAST10(Т.Alternate))
				ИЛИ PHONE_LAST10(З.Work) В (PHONE_LAST10(Т.Phone), PHONE_LAST10(Т.Alternate)))`
		for _, test := range []struct {
			id   uuid.UUID
			want []string
		}{{valid, []string{"mobile", "home", "work", "both"}}, {mixed, []string{"work", "both"}}, {invalid, nil}} {
			compiled, err := query.Compile(src, query.CompileOpts{
				Entities: entities, Dialect: db.Dialect(), Params: map[string]any{"Задача": test.id},
			})
			require.NoError(t, err)
			start := time.Now()
			rows, _, err := query.Run(ctx, db, &compiled)
			require.NoError(t, err)
			var got []string
			for _, row := range rows {
				require.Len(t, row, 1)
				got = append(got, row["метка"].(string))
			}
			require.ElementsMatch(t, test.want, got)
			t.Logf("indexed lookup: %v", time.Since(start))
			if test.id != valid {
				continue
			}

			explain := "EXPLAIN QUERY PLAN "
			if db.IsPostgres() {
				explain = "EXPLAIN (COSTS OFF) "
			}
			planRows, err := db.Query(ctx, explain+compiled.SQL, compiled.Args...)
			require.NoError(t, err)
			var plan []string
			for planRows.Next() {
				var detail string
				if db.IsSQLite() {
					var node, parent, unused int
					err = planRows.Scan(&node, &parent, &unused, &detail)
				} else {
					err = planRows.Scan(&detail)
				}
				require.NoError(t, err)
				plan = append(plan, detail)
			}
			require.NoError(t, planRows.Err())
			planRows.Close()
			for _, spec := range claims.Indexes {
				name := spec.StableName(table, []string{strings.ToLower(spec.Fields[0])})
				require.Contains(t, strings.Join(plan, "\n"), name)
			}
		}

		claims.Indexes = nil
		require.NoError(t, db.Migrate(ctx, entities))
		actions, _, err := db.PlanEntityIndexes(ctx, entities)
		require.NoError(t, err)
		var externalKept bool
		for _, action := range actions {
			require.False(t, action.Index.Opaque)
			if action.Index.Name == "claim_label_external" {
				externalKept = action.Ownership == "external" && action.Action == "keep"
			}
		}
		require.True(t, externalKept)
	})
}

func TestPhoneLast10RestrictedToPredicates(t *testing.T) {
	claim := &metadata.Entity{Name: "Claim", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Phone", Type: metadata.FieldTypeString},
		{Name: "Counter", Type: metadata.FieldTypeNumber},
	}}
	opts := query.CompileOpts{Entities: []*metadata.Entity{claim}, Dialect: storage.SQLiteDialect{}}
	for _, tc := range []struct {
		text, message string
	}{
		{"ВЫБРАТЬ PHONE_LAST10(З.Phone) КАК Номер ИЗ Документ.Claim КАК З", "допускается в условии"},
		{"ВЫБРАТЬ З.Ссылка ИЗ Документ.Claim КАК З ГДЕ PHONE_LAST10(З.Counter) ЕСТЬ НЕ NULL", "ожидает строковое поле"},
		{"ВЫБРАТЬ З.Ссылка ИЗ Документ.Claim КАК З ГДЕ PHONE_LAST10(З.Phone, З.Phone) ЕСТЬ НЕ NULL", "ожидает строковое поле"},
	} {
		_, err := query.Compile(tc.text, opts)
		require.ErrorContains(t, err, tc.message)
	}
}
