package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Regression for both blockers of the sixth review of #1459. The same public
// compiler and runner used by reports must protect joined _users references.
func TestJoinedUsersRefSecurityMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		doc := usersRefEntity()
		base := joinedRefEntities()[0]
		doc.Fields = append(doc.Fields, metadata.Field{Name: "Код", Type: metadata.FieldTypeString})
		ents := []*metadata.Entity{base, doc}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		ids := map[string]string{}
		for i, fullName := range []string{"Директор", ""} {
			login := fmt.Sprintf("joined-user-%d", i)
			user, err := repo.Create(ctx, login, "test-password-123456", fullName, true)
			if err != nil {
				t.Fatal(err)
			}
			ids[login] = user.ID
			// A nonempty synthetic secret proves that star really excludes values,
			// rather than merely observing the default empty authentication columns.
			if _, err := db.Exec(ctx, "UPDATE _users SET totp_secret = "+db.Dialect().Placeholder(1)+" WHERE id = "+db.Dialect().Placeholder(2), "synthetic-joined-secret", user.ID); err != nil {
				t.Fatal(err)
			}
			code := fmt.Sprint(i)
			if err := db.Upsert(ctx, base.Name, uuid.New(), map[string]any{"Код": code, "Наименование": "База"}, base); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Код": code, "Номер": code, "Автор": user.ID}, doc); err != nil {
				t.Fatal(err)
			}
		}
		reg := &metadata.Register{Name: "ОстаткиПробы", Dimensions: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		const joined = ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Документ.Заявка КАК З ПО З.Код = Б.Код`
		const derivedFirst = ` ИЗ (ВЫБРАТЬ Код ИЗ Справочник.Базы) КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Документ.Заявка КАК З ПО З.Код = Б.Код`
		const aliasCollision = joined + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Базы КАК ref_з_автор ПО ref_з_автор.Код = Б.Код`
		const followingVT = joined + ` ЛЕВОЕ СОЕДИНЕНИЕ РегистрНакопления.ОстаткиПробы.Остатки() КАК Р ПО Р.Код = Б.Код И З.Автор.Логин <> ""`
		opts := query.CompileOpts{Entities: ents, Registers: []*metadata.Register{reg}, Dialect: db.Dialect()}
		run := func(t *testing.T, text string) ([]map[string]any, []string) {
			t.Helper()
			r, err := query.Compile(text, opts)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			rows, cols, err := query.Run(ctx, db, &r)
			if err != nil {
				t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
			}
			return rows, cols
		}
		for _, variant := range []struct{ name, text string }{
			{"derived-first", `ВЫБРАТЬ З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование КАК Имя, З.Автор.Ссылка КАК Ид` + derivedFirst + ` УПОРЯДОЧИТЬ ПО Логин`},
			{"correlated-joined", `ВЫБРАТЬ З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование КАК Имя, З.Автор.Ссылка КАК Ид` + joined + ` ГДЕ Б.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Код = З.Код И З.Автор.Логин <> "") УПОРЯДОЧИТЬ ПО Логин`},
			{"following-vt-on", `ВЫБРАТЬ З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование КАК Имя, З.Автор.Ссылка КАК Ид` + followingVT + ` УПОРЯДОЧИТЬ ПО Логин`},
			{"outer", `ВЫБРАТЬ З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование КАК Имя, З.Автор.Ссылка КАК Ид` + joined + ` УПОРЯДОЧИТЬ ПО Логин`},
			{"allocated-alias", `ВЫБРАТЬ З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование КАК Имя, З.Автор.Ссылка КАК Ид` + aliasCollision + ` УПОРЯДОЧИТЬ ПО Логин`},
			{"derived", `ВЫБРАТЬ Д.Логин, Д.ПолноеИмя, Д.Имя, Д.Ид ИЗ (ВЫБРАТЬ З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование КАК Имя, З.Автор.Ссылка КАК Ид` + joined + `) КАК Д УПОРЯДОЧИТЬ ПО Д.Логин`},
			{"nested-alias", `ВЫБРАТЬ З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование КАК Имя, З.Автор.Ссылка КАК Ид` + joined + ` ГДЕ З.Код В (ВЫБРАТЬ З.Код` + joined + ` ГДЕ З.Автор.Логин <> "") УПОРЯДОЧИТЬ ПО Логин`},
		} {
			t.Run(variant.name, func(t *testing.T) {
				rows, _ := run(t, variant.text)
				if len(rows) != 2 {
					t.Fatalf("rows: %v", rows)
				}
				for i, row := range rows {
					login := fmt.Sprintf("joined-user-%d", i)
					fullName, name := "Директор", "Директор"
					if i == 1 {
						fullName, name = "", login
					}
					if row["логин"] != login || row["полноеимя"] != fullName || row["имя"] != name || usersRefID(row["ид"]) != ids[login] {
						t.Errorf("unexpected attributes: %v", row)
					}
				}
			})
		}
		for _, col := range []string{"password_hash", "totp_secret", "is_admin", "auth_subject", "full_name", "*"} {
			for _, text := range []string{
				`ВЫБРАТЬ З.Автор.` + col + ` КАК Х` + joined,
				`ВЫБРАТЬ З.Автор.` + col + ` КАК Х` + followingVT,
				`ВЫБРАТЬ ref_з_автор.` + col + ` КАК Х` + followingVT,
				`ВЫБРАТЬ З.Автор.` + col + ` КАК Х` + derivedFirst,
				`ВЫБРАТЬ Б.Код` + joined + ` ГДЕ Б.Код В (ВЫБРАТЬ З.Автор.` + col + ` ИЗ Справочник.Базы КАК Б2)`,
				`ВЫБРАТЬ Д.Х ИЗ (ВЫБРАТЬ З.Автор.` + col + ` КАК Х` + joined + `) КАК Д`,
				`ВЫБРАТЬ Б.Код` + joined + ` ГДЕ Б.Код В (ВЫБРАТЬ З.Автор.` + col + joined + `)`,
				`ВЫБРАТЬ ref_з_автор.` + col + ` КАК Х` + joined + ` ГДЕ З.Автор.Ссылка НЕ ЕСТЬ ПУСТО`,
				`ВЫБРАТЬ ref_join_1.` + col + ` КАК Х` + aliasCollision + ` ГДЕ З.Автор.Ссылка НЕ ЕСТЬ ПУСТО`,
				`ВЫБРАТЬ Б.Код` + joined + ` ГДЕ З.Автор.Ссылка НЕ ЕСТЬ ПУСТО И Б.Код В (ВЫБРАТЬ ref_з_автор.` + col + ` ИЗ Справочник.Базы КАК Б2)`,
				`ВЫБРАТЬ З.Номер` + joined + ` УПОРЯДОЧИТЬ ПО ref_з_автор.` + col + ``,
			} {
				t.Run("reject/"+col, func(t *testing.T) {
					r, err := query.Compile(text, opts)
					if err == nil {
						t.Fatalf("protected path accepted: %s\nSQL: %s", text, r.SQL)
					}
					if !strings.Contains(err.Error(), "недоступно") {
						t.Fatalf("wrong rejection: %v", err)
					}
				})
			}
		}
		for _, text := range []string{
			`ВЫБРАТЬ *` + joined + ` ГДЕ З.Автор.Ссылка НЕ ЕСТЬ ПУСТО`,
			`ВЫБРАТЬ *` + followingVT,
			`ВЫБРАТЬ *` + joined + ` ГДЕ З.Автор.Логин <> ""`,
			`ВЫБРАТЬ *` + aliasCollision + ` ГДЕ З.Автор.Логин <> ""`,
			`ВЫБРАТЬ *` + derivedFirst + ` ГДЕ З.Автор.Логин <> ""`,
			`ВЫБРАТЬ *` + joined + ` ГДЕ Б.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Код = З.Код И З.Автор.Логин <> "")`,
			`ВЫБРАТЬ * ИЗ (ВЫБРАТЬ *` + joined + ` ГДЕ З.Автор.Ссылка НЕ ЕСТЬ ПУСТО) КАК Д`,
		} {
			t.Run("star", func(t *testing.T) {
				rows, cols := run(t, text)
				if len(rows) != 2 {
					t.Fatalf("rows: %v", rows)
				}
				for _, col := range cols {
					switch col {
					case "password_hash", "totp_secret", "is_admin", "auth_subject":
						t.Errorf("authentication column %s exposed", col)
					}
				}
				for _, row := range rows {
					if row["login"] == nil || row["full_name"] == nil {
						t.Errorf("safe user attributes lost: %v", row)
					}
					for col, v := range row {
						if fmt.Sprint(v) == "synthetic-joined-secret" {
							t.Errorf("secret exposed through %s", col)
						}
					}
				}
			})
		}
	})
}

func TestJoinedRefOwnOnNestedSelectMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := joinedRefEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		seedJoinedRef(t, ctx, db, ents)
		const joined = ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО `
		for _, on := range []string{
			`П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2) И П.ГлавныйУзел.Наименование = Б.Наименование`,
			`П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Код = "GL") И П.ГлавныйУзел.Наименование = Б.Наименование`,
			`П.Код В (ВЫБРАТЬ Б2.Код ИЗ (ВЫБРАТЬ Код ИЗ Справочник.Базы) КАК Б2) И Планы.ГлавныйУзел.Наименование = Б.Наименование`,
		} {
			t.Run("reject-own-on", func(t *testing.T) {
				for _, text := range []string{`ВЫБРАТЬ П.Наименование` + joined + on, `ВЫБРАТЬ Д.Наименование ИЗ (ВЫБРАТЬ П.Наименование` + joined + on + `) КАК Д`} {
					r, err := query.Compile(text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
					if err == nil {
						t.Fatalf("own ON accepted after nested SELECT\nSQL: %s", r.SQL)
					}
					if !strings.Contains(err.Error(), "внутри ПО не поддерживается") {
						t.Fatal(err)
					}
				}
			})
		}
		// A reference from an earlier JOIN remains available in the next ON, and
		// the current joined reference remains available in WHERE after a subquery.
		for _, text := range []string{
			`ВЫБРАТЬ П.Наименование` + joined + `П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2) ГДЕ П.ГлавныйУзел.Наименование = Б.Наименование`,
			`ВЫБРАТЬ П.Наименование` + joined + `П.Код = Б.Код ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Базы КАК Б3 ПО Б3.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2) И Б3.Ссылка = П.ГлавныйУзел.Ссылка`,
		} {
			t.Run("allowed-after-on", func(t *testing.T) {
				r, err := query.Compile(text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
				}
				if len(rows) != 1 || rows[0]["наименование"] != "План-1" {
					t.Fatalf("rows: %v", rows)
				}
			})
		}
	})
}
