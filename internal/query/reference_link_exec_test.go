package query_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Регрессия #424: bare-Ссылка должна квалифицироваться основной таблицей,
// когда reference-поле добавляет авто-JOIN. Иначе SQLite видит несколько id и
// отклоняет запрос с `ambiguous column name: id`.
func TestBareReference_WithReferenceJoin_ExecuteSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "reference-link.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	participant := &metadata.Entity{
		Name:   "Участник",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	project := &metadata.Entity{
		Name:   "Проект",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	expense := &metadata.Entity{
		Name: "Расход",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
			{Name: "Период", Type: metadata.FieldTypeDate},
			{Name: "ОплаченоУчастником", Type: "reference:Участник", RefEntity: "Участник"},
			{Name: "Проект", Type: "reference:Проект", RefEntity: "Проект"},
		},
	}
	entities := []*metadata.Entity{participant, project, expense}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	participantID := uuid.New()
	projectID := uuid.New()
	expenseID := uuid.New()
	if err := db.Upsert(ctx, participant.Name, participantID, map[string]any{
		"наименование": "Иван",
	}, participant); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	if err := db.Upsert(ctx, project.Name, projectID, map[string]any{
		"наименование": "Казначейство",
	}, project); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := db.Upsert(ctx, expense.Name, expenseID, map[string]any{
		"сумма":              100,
		"период":             time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		"оплаченоучастником": participantID.String(),
		"проект":             projectID.String(),
	}, expense); err != nil {
		t.Fatalf("insert expense: %v", err)
	}
	if err := db.SetPosted(ctx, expense.Name, expenseID, true); err != nil {
		t.Fatalf("post expense: %v", err)
	}

	tests := []struct {
		name        string
		src         string
		withDisplay bool
		wantLinkSQL string
	}{
		{
			name:        "reference field in projection",
			src:         `ВЫБРАТЬ Ссылка, ОплаченоУчастником ИЗ Документ.Расход ГДЕ posted = 1`,
			withDisplay: true,
			wantLinkSQL: "расход.id",
		},
		{
			name:        "reference field in condition",
			src:         `ВЫБРАТЬ Ссылка ИЗ Документ.Расход ГДЕ posted = 1 И (Проект = Проект)`,
			wantLinkSQL: "расход.id",
		},
		{
			name:        "aliased source",
			src:         `ВЫБРАТЬ Ссылка, ОплаченоУчастником ИЗ Документ.Расход КАК Р ГДЕ Р.posted = 1`,
			withDisplay: true,
			wantLinkSQL: "р.id",
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

			var link string
			if tt.withDisplay {
				var display string
				err = db.QueryRow(ctx, res.SQL, res.Args...).Scan(&link, &display)
				if err == nil && display != "Иван" {
					t.Fatalf("display = %q, want Иван", display)
				}
			} else {
				err = db.QueryRow(ctx, res.SQL, res.Args...).Scan(&link)
			}
			if err != nil {
				t.Fatalf("execute %q\nSQL: %s\nerror: %v", tt.src, res.SQL, err)
			}
			if link != expenseID.String() {
				t.Fatalf("link = %q, want %q", link, expenseID)
			}
			if !strings.Contains(res.SQL, tt.wantLinkSQL) {
				t.Fatalf("bare Ссылка is not qualified by the source table:\n%s", res.SQL)
			}
		})
	}

	t.Run("nested select uses its own source", func(t *testing.T) {
		src := `ВЫБРАТЬ Ссылка ИЗ Документ.Расход КАК Р ГДЕ ОплаченоУчастником В (ВЫБРАТЬ Ссылка ИЗ Справочник.Участник)`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var link string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&link); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if link != expenseID.String() {
			t.Fatalf("link = %q, want %q", link, expenseID)
		}
		if !strings.Contains(res.SQL, "(SELECT участник.id FROM участник)") {
			t.Fatalf("nested Ссылка is not qualified by its own source:\n%s", res.SQL)
		}
	})

	t.Run("outer link after nested source uses outer source", func(t *testing.T) {
		src := `ВЫБРАТЬ Ссылка, (ВЫБРАТЬ ОплаченоУчастником ИЗ Документ.Расход) КАК Участник ИЗ Справочник.Проект`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var link, display string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&link, &display); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if link != projectID.String() {
			t.Fatalf("link = %q, want %q", link, projectID)
		}
		if display != "Иван" {
			t.Fatalf("display = %q, want Иван", display)
		}
		if !strings.HasPrefix(res.SQL, "SELECT проект.id,") {
			t.Fatalf("outer Ссылка is not qualified by its own source:\n%s", res.SQL)
		}
	})

	t.Run("reference word remains a valid output alias", func(t *testing.T) {
		src := `ВЫБРАТЬ Сумма КАК Ссылка, Р.Ссылка, Проект ИЗ Документ.Расход КАК Р УПОРЯДОЧИТЬ ПО Ссылка`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var amount any
		var link, display string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&amount, &link, &display); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if link != expenseID.String() {
			t.Fatalf("link = %q, want %q", link, expenseID)
		}
		if display != "Казначейство" {
			t.Fatalf("display = %q, want Казначейство", display)
		}
		if !strings.Contains(res.SQL, "AS id") || !strings.Contains(res.SQL, "р.id") ||
			!strings.Contains(res.SQL, "ORDER BY id") {
			t.Fatalf("Ссылка used as an output alias generated invalid SQL:\n%s", res.SQL)
		}
	})

	t.Run("reference output alias is usable in having", func(t *testing.T) {
		src := `SELECT SUM(Сумма) AS Reference, Проект
			FROM Document.Расход
			GROUP BY Проект
			HAVING Reference > 50`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var total float64
		var display string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&total, &display); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if total != 100 || display != "Казначейство" {
			t.Fatalf("row = (%v, %q), want (100, Казначейство)", total, display)
		}
		if !strings.Contains(res.SQL, "HAVING(SUM(CAST(расход.сумма AS NUMERIC))) > 50") {
			t.Fatalf("HAVING did not expand the reserved output alias:\n%s", res.SQL)
		}

		pgRes, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.PgDialect{},
		})
		if err != nil {
			t.Fatalf("compile PostgreSQL: %v", err)
		}
		if !strings.Contains(pgRes.SQL, "HAVING(SUM(расход.сумма)) > 50") ||
			strings.Contains(pgRes.SQL, "HAVING id") {
			t.Fatalf("PostgreSQL HAVING still references the SELECT alias:\n%s", pgRes.SQL)
		}
	})

	t.Run("reference output alias is usable in group by", func(t *testing.T) {
		src := `SELECT Сумма AS Reference, Проект
			FROM Document.Расход
			GROUP BY Reference, Проект`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var amount float64
		var display string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&amount, &display); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if amount != 100 || display != "Казначейство" {
			t.Fatalf("row = (%v, %q), want (100, Казначейство)", amount, display)
		}
		if !strings.Contains(res.SQL, "GROUP BY(расход.сумма),") {
			t.Fatalf("GROUP BY did not expand the reserved output alias:\n%s", res.SQL)
		}
	})

	t.Run("select all modifier is not copied into group by", func(t *testing.T) {
		src := `SELECT ALL Сумма AS Reference, Проект
			FROM Document.Расход
			GROUP BY Reference, Проект`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if strings.Contains(res.SQL, "GROUP BY(ALL ") ||
			!strings.Contains(res.SQL, "GROUP BY(расход.сумма),") {
			t.Fatalf("SELECT ALL leaked into the grouped expression:\n%s", res.SQL)
		}

		var amount float64
		var display string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&amount, &display); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
	})

	t.Run("output alias does not leak into references", func(t *testing.T) {
		src := `ВЫБРАТЬ Сумма КАК Ссылка, Р.Ссылка, (ВЫБРАТЬ Ссылка ИЗ Справочник.Участник) КАК Вложенная ИЗ Документ.Расход КАК Р`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var amount any
		var expenseLink, nestedLink string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&amount, &expenseLink, &nestedLink); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if expenseLink != expenseID.String() || nestedLink != participantID.String() {
			t.Fatalf("links = (%q, %q), want (%q, %q)", expenseLink, nestedLink, expenseID, participantID)
		}
		if !strings.Contains(res.SQL, "р.id") || !strings.Contains(res.SQL, "(SELECT id FROM участник)") {
			t.Fatalf("output alias leaked into qualified or nested references:\n%s", res.SQL)
		}
	})

	t.Run("derived table remains the main source", func(t *testing.T) {
		src := `ВЫБРАТЬ Ссылка ИЗ (ВЫБРАТЬ Р.Ссылка, Р.Проект ИЗ Документ.Расход КАК Р) КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Участник КАК У ПО 1 = 0`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var link string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&link); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if link != expenseID.String() {
			t.Fatalf("link = %q, want %q", link, expenseID)
		}
		if !strings.HasPrefix(res.SQL, "SELECT п.id FROM") {
			t.Fatalf("derived table was not preserved as the main source:\n%s", res.SQL)
		}
	})

	t.Run("derived register preserves system columns", func(t *testing.T) {
		reg := &metadata.Register{Name: "Остатки"}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("migrate register: %v", err)
		}
		period := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Документ", uuid.New(),
			[]map[string]any{{}}, reg, &period); err != nil {
			t.Fatalf("write movement: %v", err)
		}

		src := `ВЫБРАТЬ П.Период ИЗ (ВЫБРАТЬ Период ИЗ РегистрНакопления.Остатки КАК В) КАК П ЛЕВОЕ СОЕДИНЕНИЕ РегистрНакопления.Остатки КАК Р ПО 1 = 0`
		res, err := query.Compile(src, query.CompileOpts{
			Registers: []*metadata.Register{reg},
			Dialect:   storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var got any
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&got); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if !strings.HasPrefix(res.SQL, "SELECT п.period FROM") {
			t.Fatalf("derived register lost system-column semantics:\n%s", res.SQL)
		}

		entityFieldSrc := `ВЫБРАТЬ П.Период ИЗ (ВЫБРАТЬ Д.Период ИЗ РегистрНакопления.Остатки КАК Р ЛЕВОЕ СОЕДИНЕНИЕ Документ.Расход КАК Д ПО 1 = 0) КАК П`
		entityFieldRes, err := query.Compile(entityFieldSrc, query.CompileOpts{
			Registers: []*metadata.Register{reg},
			Entities:  entities,
			Dialect:   storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile entity field: %v", err)
		}
		if err := db.QueryRow(ctx, entityFieldRes.SQL, entityFieldRes.Args...).Scan(&got); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", entityFieldSrc, entityFieldRes.SQL, err)
		}
		if !strings.HasPrefix(entityFieldRes.SQL, "SELECT п.период FROM") {
			t.Fatalf("derived entity field inherited register semantics:\n%s", entityFieldRes.SQL)
		}
	})

	t.Run("union order uses the output column", func(t *testing.T) {
		src := `ВЫБРАТЬ Р.Ссылка ИЗ Документ.Расход КАК Р ГДЕ Проект = Проект ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ У.Ссылка ИЗ Справочник.Участник КАК У УПОРЯДОЧИТЬ ПО Ссылка`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.PgDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !strings.HasSuffix(res.SQL, "ORDER BY id") {
			t.Fatalf("UNION-level ORDER BY must use the output column:\n%s", res.SQL)
		}
	})

	t.Run("parenthesized union order uses the output column", func(t *testing.T) {
		src := `ВЫБРАТЬ Р.Ссылка ИЗ Документ.Расход КАК Р ГДЕ Проект = Проект ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ У.Ссылка ИЗ Справочник.Участник КАК У УПОРЯДОЧИТЬ ПО (Ссылка)`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.PgDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !strings.HasSuffix(res.SQL, "ORDER BY(id)") {
			t.Fatalf("parenthesized UNION-level ORDER BY must use the output column:\n%s", res.SQL)
		}
	})

	t.Run("reserved alias survives a derived table", func(t *testing.T) {
		src := `ВЫБРАТЬ П.Ссылка ИЗ (ВЫБРАТЬ Р.Ссылка КАК Ссылка ИЗ Документ.Расход КАК Р) КАК П`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}

		var link string
		if err := db.QueryRow(ctx, res.SQL, res.Args...).Scan(&link); err != nil {
			t.Fatalf("execute %q\nSQL: %s\nerror: %v", src, res.SQL, err)
		}
		if link != expenseID.String() {
			t.Fatalf("link = %q, want %q", link, expenseID)
		}
		if !strings.Contains(res.SQL, "р.id AS id") || !strings.HasPrefix(res.SQL, "SELECT п.id FROM") {
			t.Fatalf("reserved alias changed across the derived-table boundary:\n%s", res.SQL)
		}
	})

	t.Run("scalar subquery alias is visible to order", func(t *testing.T) {
		src := `ВЫБРАТЬ (ВЫБРАТЬ Сумма ИЗ Документ.Расход) КАК Ссылка ИЗ Справочник.Проект УПОРЯДОЧИТЬ ПО Ссылка`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.SQLiteDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !strings.Contains(res.SQL, "AS id") || !strings.HasSuffix(res.SQL, "ORDER BY id") {
			t.Fatalf("scalar-subquery alias was not recorded in the outer scope:\n%s", res.SQL)
		}
	})

	t.Run("postgres scalar function alias is visible to order", func(t *testing.T) {
		src := `ВЫБРАТЬ Год(Период) КАК Ссылка, Проект ИЗ Документ.Расход УПОРЯДОЧИТЬ ПО Ссылка`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.PgDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !strings.Contains(res.SQL, "AS id") || !strings.HasSuffix(res.SQL, "ORDER BY id") {
			t.Fatalf("EXTRACT's internal FROM hid the reserved output alias:\n%s", res.SQL)
		}
	})

	t.Run("later union alias does not rename the output", func(t *testing.T) {
		src := `ВЫБРАТЬ Р.Ссылка ИЗ Документ.Расход КАК Р ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Наименование КАК Ссылка ИЗ Справочник.Участник УПОРЯДОЧИТЬ ПО Ссылка`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.PgDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !strings.Contains(res.SQL, "наименование AS id") || !strings.HasSuffix(res.SQL, "ORDER BY id") {
			t.Fatalf("later UNION arm changed the compound output name:\n%s", res.SQL)
		}
	})

	t.Run("english group recognizes the reserved alias", func(t *testing.T) {
		src := `SELECT Сумма AS Reference, Проект FROM Document.Расход GROUP BY Reference, Проект`
		res, err := query.Compile(src, query.CompileOpts{
			Entities: entities,
			Dialect:  storage.PgDialect{},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !strings.Contains(res.SQL, "AS id") || !strings.Contains(res.SQL, "GROUP BY(расход.сумма),") {
			t.Fatalf("English GROUP BY did not expand the reserved alias:\n%s", res.SQL)
		}
	})
}
