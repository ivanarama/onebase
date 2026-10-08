package query_test

// Запрос по документу с реквизитом type: reference:_users на живой SQLite
// (issue #1646): авто-JOIN идёт на системную таблицу учётных записей, bare-select
// ссылочного реквизита отдаёт представление учётки — ПолноеИмя, а при пустом —
// логин. У таблицы _users нет колонки «наименование», поэтому общий путь
// displayCol здесь не работал бы.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestSQLiteUsersRefDisplayJoin(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	u, err := repo.Create(ctx, "ivanov", "пароль-123456", "Иванов И.И.", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := db.Exec(ctx, `
		CREATE TABLE заявки (
			id TEXT PRIMARY KEY,
			номер TEXT,
			автор_id TEXT REFERENCES _users(id)
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO заявки (id, номер, автор_id) VALUES (?, ?, ?)`,
		"99999999-9999-9999-9999-999999999999", "0001", u.ID,
	); err != nil {
		t.Fatal(err)
	}

	entity := &metadata.Entity{
		Name: "Заявки",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Автор", Type: "reference:_users", RefEntity: metadata.SystemUsersEntity},
		},
	}
	result, err := query.Compile(
		`ВЫБРАТЬ Автор ИЗ Документ.Заявки ГДЕ Номер = &Номер`,
		query.CompileOpts{
			Dialect:  db.Dialect(),
			Entities: []*metadata.Entity{entity},
			Params:   map[string]any{"Номер": "0001"},
		},
	)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var author string
	if err := db.QueryRow(ctx, result.SQL, result.Args...).Scan(&author); err != nil {
		t.Fatalf("run: %v\nSQL: %s", err, result.SQL)
	}
	if author != "Иванов И.И." {
		t.Fatalf("Автор = %q, want «Иванов И.И.»", author)
	}
}
