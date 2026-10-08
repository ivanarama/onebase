package query_test

// Ссылка на учётную запись входа (type: reference:_users, #1646) в языке
// запросов. Системная таблица _users сознательно не регистрируется источником
// прав, поэтому через точку читается только объявленное: Ссылка, Наименование,
// Логин и ПолноеИмя. Раньше любое имя после точки уходило в SQL дословно, и
// «Автор.password_hash» отдавал хеш пароля любому запросу — отчёту, виджету,
// ИИ-помощнику не-администратора.

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

func usersRefEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Автор", Type: "reference:_users", RefEntity: metadata.SystemUsersEntity},
		},
	}
}

func TestUsersRefNavigationReadsDeclaredAttributes(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}
		ivanov, err := repo.Create(ctx, "ivanov", "пароль-123456", "Иванов И.И.", true)
		if err != nil {
			t.Fatalf("Create ivanov: %v", err)
		}
		// Без полного имени представление учётки — логин.
		petrov, err := repo.Create(ctx, "petrov", "пароль-654321", "", false)
		if err != nil {
			t.Fatalf("Create petrov: %v", err)
		}
		заявка := usersRefEntity()
		if err := db.Migrate(ctx, []*metadata.Entity{заявка}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		for номер, автор := range map[string]string{"0001": ivanov.ID, "0002": petrov.ID} {
			if err := db.Upsert(ctx, заявка.Name, uuid.New(), map[string]any{"Номер": номер, "Автор": автор}, заявка); err != nil {
				t.Fatalf("Upsert %s: %v", номер, err)
			}
		}

		compiled, err := query.Compile(`
			ВЫБРАТЬ Номер, Автор.Логин, Автор.ПолноеИмя, Автор.Наименование КАК Имя, Автор.Ссылка КАК Ид
			ИЗ Документ.Заявка
			ГДЕ Автор.Логин <> ""
			УПОРЯДОЧИТЬ ПО Номер`, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{заявка}})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		rows, _, err := query.Run(ctx, db, &compiled)
		if err != nil {
			t.Fatalf("выполнение: %v\nSQL: %s", err, compiled.SQL)
		}
		if len(rows) != 2 {
			t.Fatalf("строк %d, ожидалось 2: %v", len(rows), rows)
		}
		want := []struct{ login, fullName, name, id string }{
			{"ivanov", "Иванов И.И.", "Иванов И.И.", ivanov.ID},
			{"petrov", "", "petrov", petrov.ID},
		}
		for i, w := range want {
			row := rows[i]
			if got := fmt.Sprint(row["логин"]); got != w.login {
				t.Errorf("строка %d: Логин = %q, want %q (row %v)", i, got, w.login, row)
			}
			if got := fmt.Sprint(row["полноеимя"]); got != w.fullName {
				t.Errorf("строка %d: ПолноеИмя = %q, want %q", i, got, w.fullName)
			}
			if got := fmt.Sprint(row["имя"]); got != w.name {
				t.Errorf("строка %d: Наименование = %q, want %q", i, got, w.name)
			}
			if got := usersRefID(row["ид"]); got != w.id {
				t.Errorf("строка %d: Ссылка = %q, want %q", i, got, w.id)
			}
		}

		// Квалифицированный путь и сортировка по представлению тоже исполнимы.
		ordered, err := query.Compile(`
			ВЫБРАТЬ З.Номер, З.Автор.Логин КАК Л
			ИЗ Документ.Заявка КАК З
			УПОРЯДОЧИТЬ ПО З.Автор.Наименование`, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{заявка}})
		if err != nil {
			t.Fatalf("компиляция квалифицированного пути: %v", err)
		}
		if _, _, err := query.Run(ctx, db, &ordered); err != nil {
			t.Fatalf("выполнение квалифицированного пути: %v\nSQL: %s", err, ordered.SQL)
		}
	})
}

func TestUsersRefNavigationRejectsAuthColumns(t *testing.T) {
	заявка := usersRefEntity()
	for _, text := range []string{
		`ВЫБРАТЬ Автор.password_hash ИЗ Документ.Заявка`,
		`ВЫБРАТЬ Автор.totp_secret ИЗ Документ.Заявка`,
		`ВЫБРАТЬ Автор.is_admin ИЗ Документ.Заявка`,
		`ВЫБРАТЬ Автор.full_name ИЗ Документ.Заявка`,
		`ВЫБРАТЬ Автор.* ИЗ Документ.Заявка`,
		`ВЫБРАТЬ З.Автор.* ИЗ Документ.Заявка КАК З`,
		`ВЫБРАТЬ З.Автор.password_hash КАК Х ИЗ Документ.Заявка КАК З`,
		`ВЫБРАТЬ Номер ИЗ Документ.Заявка ГДЕ Автор.password_hash ПОДОБНО "$2%"`,
		`ВЫБРАТЬ Номер ИЗ Документ.Заявка УПОРЯДОЧИТЬ ПО Автор.auth_subject`,
		// Псевдоним авто-JOIN — служебное имя: через него читалась бы вся
		// строка _users, как только соединение появилось ради Автор.Логин.
		`ВЫБРАТЬ ref_автор.password_hash КАК Х ИЗ Документ.Заявка ГДЕ Автор.Логин = "boss"`,
		`ВЫБРАТЬ Номер ИЗ Документ.Заявка ГДЕ Автор.Логин <> "" И REF_АВТОР.totp_secret <> ""`,
		`ВЫБРАТЬ Автор.Логин ИЗ Документ.Заявка УПОРЯДОЧИТЬ ПО ref_автор.password_hash`,
	} {
		for _, dialect := range []storage.Dialect{storage.SQLiteDialect{}, storage.PgDialect{}} {
			res, err := query.Compile(text, query.CompileOpts{Dialect: dialect, Entities: []*metadata.Entity{заявка}})
			if err == nil {
				t.Errorf("%s (%s): запрос скомпилирован, ожидался отказ\nSQL: %s", text, dialect.Name(), res.SQL)
				continue
			}
			if !strings.Contains(err.Error(), "недоступно") {
				t.Errorf("%s (%s): ошибка не называет причину: %v", text, dialect.Name(), err)
			}
		}
	}
}

// Звёздочка без квалификатора разворачивается во все колонки источников FROM,
// включая авто-JOIN учётных записей, а он появляется, как только запрос
// упоминает ссылку — хоть в отборе, хоть в сортировке. Раньше
// «ВЫБРАТЬ * ИЗ Документ.Заявка ГДЕ Автор.Логин <> ""» отдавал всю строку
// _users: хеш пароля, секрет второго фактора, признак администратора (#1752).
// Звёздочка остаётся законной, но от учётки в результат попадают только
// login и full_name.
func TestUsersRefStarDoesNotExposeAuthColumns(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}
		boss, err := repo.Create(ctx, "boss", "пароль-123456", "Директор", true)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		var hash []byte
		if err := db.QueryRow(ctx, `SELECT password_hash FROM _users WHERE login = 'boss'`).Scan(&hash); err != nil || len(hash) == 0 {
			t.Fatalf("хеш пароля: %v (%d байт)", err, len(hash))
		}
		заявка := usersRefEntity()
		if err := db.Migrate(ctx, []*metadata.Entity{заявка}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := db.Upsert(ctx, заявка.Name, uuid.New(), map[string]any{"Номер": "0001", "Автор": boss.ID}, заявка); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		own, err := db.QueryAll(ctx, `SELECT * FROM заявка`)
		if err != nil || len(own) != 1 {
			t.Fatalf("колонки документа: %v (%d строк)", err, len(own))
		}
		for _, text := range []string{
			`ВЫБРАТЬ * ИЗ Документ.Заявка ГДЕ Автор.Логин <> ""`,
			`ВЫБРАТЬ * ИЗ Документ.Заявка УПОРЯДОЧИТЬ ПО Автор.Наименование`,
		} {
			compiled, err := query.Compile(text, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{заявка}})
			if err != nil {
				t.Fatalf("%s: компиляция: %v", text, err)
			}
			rows, _, err := query.Run(ctx, db, &compiled)
			if err != nil {
				t.Fatalf("%s: выполнение: %v\nSQL: %s", text, err, compiled.SQL)
			}
			if len(rows) != 1 {
				t.Fatalf("%s: строк %d, ожидалась 1: %v", text, len(rows), rows)
			}
			for col, v := range rows[0] {
				if _, ownCol := own[0][col]; !ownCol && col != "login" && col != "full_name" {
					t.Errorf("%s: колонка учётки %q в результате\nSQL: %s", text, col, compiled.SQL)
				}
				if b, ok := v.([]byte); ok && string(b) == string(hash) {
					t.Errorf("%s: хеш пароля в колонке %q\nSQL: %s", text, col, compiled.SQL)
				}
			}
			if got := fmt.Sprint(rows[0]["login"]); got != "boss" {
				t.Errorf("%s: login = %q, want boss (row %v)", text, got, rows[0])
			}
		}
	})
}

func TestUsersRefJoinAliasDoesNotHideOwnField(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := auth.NewRepo(db).EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		entity := usersRefEntity()
		entity.Fields = append(entity.Fields, metadata.Field{Name: "Ref_Автор", Type: metadata.FieldTypeString})
		if err := metadata.Validate([]*metadata.Entity{entity}, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Номер": "1", "Ref_Автор": "прикладное поле"}, entity); err != nil {
			t.Fatal(err)
		}
		compiled, err := query.Compile(`ВЫБРАТЬ Автор.Ссылка, Ref_Автор КАК Значение ИЗ Документ.Заявка`, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{entity}})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		rows, _, err := query.Run(ctx, db, &compiled)
		if err != nil {
			t.Fatalf("выполнение: %v\nSQL: %s", err, compiled.SQL)
		}
		if len(rows) != 1 || fmt.Sprint(rows[0]["значение"]) != "прикладное поле" {
			t.Fatalf("своё поле Ref_Автор потеряно: %v", rows)
		}
		if _, err := query.Compile(`ВЫБРАТЬ Ref_Автор.password_hash ИЗ Документ.Заявка ГДЕ Автор.Логин <> ""`, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{entity}}); err == nil || !strings.Contains(err.Error(), "недоступно") {
			t.Fatalf("служебный alias не должен открываться при одноимённом поле: %v", err)
		}
	})
}

// Имя реквизита достаётся колонке, только когда реквизит — целый элемент
// списка выборки. В аргументе агрегата «AS логин» давало бы MAX(x AS логин) —
// синтаксическую ошибку на обоих диалектах.
func TestUsersRefAttributeInsideExpressions(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}
		ivanov, err := repo.Create(ctx, "ivanov", "пароль-123456", "Иванов И.И.", true)
		if err != nil {
			t.Fatalf("Create ivanov: %v", err)
		}
		petrov, err := repo.Create(ctx, "petrov", "пароль-654321", "", false)
		if err != nil {
			t.Fatalf("Create petrov: %v", err)
		}
		заявка := usersRefEntity()
		if err := db.Migrate(ctx, []*metadata.Entity{заявка}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		for номер, автор := range map[string]string{"0001": ivanov.ID, "0002": petrov.ID, "0003": petrov.ID} {
			if err := db.Upsert(ctx, заявка.Name, uuid.New(), map[string]any{"Номер": номер, "Автор": автор}, заявка); err != nil {
				t.Fatalf("Upsert %s: %v", номер, err)
			}
		}
		run := func(text string) []map[string]any {
			t.Helper()
			compiled, err := query.Compile(text, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{заявка}})
			if err != nil {
				t.Fatalf("%s: компиляция: %v", text, err)
			}
			rows, _, err := query.Run(ctx, db, &compiled)
			if err != nil {
				t.Fatalf("%s: выполнение: %v\nSQL: %s", text, err, compiled.SQL)
			}
			return rows
		}

		for _, c := range []struct{ text, col, want string }{
			{`ВЫБРАТЬ МАКСИМУМ(Автор.Логин) КАК Л ИЗ Документ.Заявка`, "л", "petrov"},
			{`ВЫБРАТЬ МИНИМУМ(Автор.Наименование) КАК Н ИЗ Документ.Заявка`, "н", "petrov"},
			{`ВЫБРАТЬ КОЛИЧЕСТВО(РАЗЛИЧНЫЕ Автор.Логин) КАК Ч ИЗ Документ.Заявка`, "ч", "2"},
			// Целый элемент после модификатора — по-прежнему с именем реквизита.
			{`ВЫБРАТЬ ПЕРВЫЕ 1 Автор.Логин ИЗ Документ.Заявка УПОРЯДОЧИТЬ ПО Номер`, "логин", "ivanov"},
		} {
			rows := run(c.text)
			if len(rows) != 1 {
				t.Fatalf("%s: строк %d, ожидалась 1: %v", c.text, len(rows), rows)
			}
			if got := fmt.Sprint(rows[0][c.col]); got != c.want {
				t.Errorf("%s: %s = %q, want %q (row %v)", c.text, c.col, got, c.want, rows[0])
			}
		}

		distinct := run(`ВЫБРАТЬ РАЗЛИЧНЫЕ Автор.Логин ИЗ Документ.Заявка`)
		if len(distinct) != 2 {
			t.Fatalf("РАЗЛИЧНЫЕ: строк %d, ожидалось 2: %v", len(distinct), distinct)
		}
		for _, row := range distinct {
			if _, ok := row["логин"]; !ok {
				t.Errorf("РАЗЛИЧНЫЕ: колонка не названа именем реквизита: %v", row)
			}
		}
		// Агрегат без КАК тоже исполним: колонку называет СУБД, но запрос цел.
		if rows := run(`ВЫБРАТЬ МАКСИМУМ(Автор.Логин) ИЗ Документ.Заявка`); len(rows) != 1 {
			t.Fatalf("агрегат без КАК: строк %d, ожидалась 1", len(rows))
		}
	})
}

func usersRefID(v any) string {
	switch id := v.(type) {
	case [16]byte:
		return uuid.UUID(id).String()
	case uuid.UUID:
		return id.String()
	case []byte:
		return string(id)
	}
	return fmt.Sprint(v)
}

// Квалифицированный путь «З.Автор.Логин» — такой же самостоятельный элемент
// выборки, как неквалифицированный «Автор.Логин»: имя колонки результата берёт
// имя реквизита, а не колонку _users. Раньше квалификатор источника сбивал
// определение границ элемента, колонка приезжала как login/full_name (а
// Наименование — целым SQL-выражением COALESCE), Выборка.Логин её не находила,
// и «УПОРЯДОЧИТЬ ПО Логин» падал на «no such column: логин».
func TestUsersRefQualifiedPathNamesColumnByAttribute(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}
		ivanov, err := repo.Create(ctx, "ivanov", "пароль-123456", "Иванов И.И.", true)
		if err != nil {
			t.Fatalf("Create ivanov: %v", err)
		}
		petrov, err := repo.Create(ctx, "petrov", "пароль-654321", "", false)
		if err != nil {
			t.Fatalf("Create petrov: %v", err)
		}
		заявка := usersRefEntity()
		if err := db.Migrate(ctx, []*metadata.Entity{заявка}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		for номер, автор := range map[string]string{"0001": ivanov.ID, "0002": petrov.ID} {
			if err := db.Upsert(ctx, заявка.Name, uuid.New(), map[string]any{"Номер": номер, "Автор": автор}, заявка); err != nil {
				t.Fatalf("Upsert %s: %v", номер, err)
			}
		}
		run := func(text string) []map[string]any {
			t.Helper()
			compiled, err := query.Compile(text, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{заявка}})
			if err != nil {
				t.Fatalf("%s: компиляция: %v", text, err)
			}
			rows, _, err := query.Run(ctx, db, &compiled)
			if err != nil {
				t.Fatalf("%s: выполнение: %v\nSQL: %s", text, err, compiled.SQL)
			}
			return rows
		}

		// Имя колонки — по имени реквизита, как написано в запросе.
		for _, c := range []struct{ text, col, want string }{
			{`ВЫБРАТЬ З.Автор.Логин ИЗ Документ.Заявка КАК З УПОРЯДОЧИТЬ ПО З.Номер`, "логин", "ivanov"},
			{`ВЫБРАТЬ З.Автор.ПолноеИмя ИЗ Документ.Заявка КАК З УПОРЯДОЧИТЬ ПО З.Номер`, "полноеимя", "Иванов И.И."},
			{`ВЫБРАТЬ З.Автор.Наименование ИЗ Документ.Заявка КАК З УПОРЯДОЧИТЬ ПО З.Номер`, "наименование", "Иванов И.И."},
			// Явное КАК по-прежнему главнее неявного имени.
			{`ВЫБРАТЬ З.Автор.Логин КАК Л ИЗ Документ.Заявка КАК З УПОРЯДОЧИТЬ ПО З.Номер`, "л", "ivanov"},
			// Не первый и не единственный элемент списка выборки.
			{`ВЫБРАТЬ З.Номер, З.Автор.Логин, З.Автор.ПолноеИмя ИЗ Документ.Заявка КАК З УПОРЯДОЧИТЬ ПО З.Номер`, "логин", "ivanov"},
		} {
			rows := run(c.text)
			if len(rows) != 2 {
				t.Fatalf("%s: строк %d, ожидалось 2: %v", c.text, len(rows), rows)
			}
			if _, ok := rows[0][c.col]; !ok {
				t.Fatalf("%s: колонка %q не названа именем реквизита: %v", c.text, c.col, rows[0])
			}
			if got := fmt.Sprint(rows[0][c.col]); got != c.want {
				t.Errorf("%s: %s = %q, want %q (row %v)", c.text, c.col, got, c.want, rows[0])
			}
		}

		// Сортировка по неявному имени: «УПОРЯДОЧИТЬ ПО Логин» видит алиас вывода.
		ordered := run(`ВЫБРАТЬ З.Автор.Логин ИЗ Документ.Заявка КАК З УПОРЯДОЧИТЬ ПО Логин`)
		if len(ordered) != 2 {
			t.Fatalf("УПОРЯДОЧИТЬ ПО Логин: строк %d, ожидалось 2: %v", len(ordered), ordered)
		}
		if got := fmt.Sprint(ordered[0]["логин"]); got != "ivanov" {
			t.Errorf("УПОРЯДОЧИТЬ ПО Логин: первая строка %q, want %q (rows %v)", got, "ivanov", ordered)
		}

		// В выражении и в аргументе функции имени быть не должно: «MAX(x AS л)»
		// не разбирается ни одной СУБД. Проверяем переносимыми обёртками, а не
		// «+»: строки он не склеивает нигде — в SQLite это арифметика, операнды
		// приводятся к числам и из двух строк молча выходит 0, а PostgreSQL
		// отвечает «operator does not exist: text + unknown». Конкатенация в обоих
		// диалектах — «||». Границы элемента выборки такой кейс всё равно не
		// проверяет.
		for _, text := range []string{
			`ВЫБРАТЬ МАКСИМУМ(З.Автор.Логин) КАК Л ИЗ Документ.Заявка КАК З`,
			`ВЫБРАТЬ ЕстьNULL(З.Автор.Логин, "нет") КАК Л ИЗ Документ.Заявка КАК З`,
		} {
			if rows := run(text); len(rows) == 0 {
				t.Errorf("%s: строк 0", text)
			}
		}

		// Служебные колонки _users в ответе не появляются ни под каким именем.
		for _, row := range run(`ВЫБРАТЬ З.Номер, З.Автор.Логин, З.Автор.ПолноеИмя, З.Автор.Наименование ИЗ Документ.Заявка КАК З`) {
			for col := range row {
				switch col {
				case "login", "full_name", "password_hash", "totp_secret", "is_admin", "auth_subject", "id":
					t.Errorf("служебная колонка _users в ответе: %q (row %v)", col, row)
				}
			}
		}
	})
}
