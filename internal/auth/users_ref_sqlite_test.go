package auth_test

// reference:_users на живой SQLite (issue #1646): реквизит со ссылкой на
// учётную запись несёт настоящий внешний ключ, поэтому существующую запись
// удалить нельзя (ErrUserReferenced с человеческим текстом), а без ссылок —
// можно. Заодно фиксируем, что FK-ошибка НЕ всплывает сырым текстом драйвера.

import (
	"errors"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestDeleteUser_ReferencedByAppTable(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)

	// Ссылку ставим на НЕ-админа: удаляемого нельзя оставлять ни последним
	// пользователем, ни последним администратором — инварианты Delete сработали
	// бы раньше проверки внешнего ключа.
	if _, err := repo.CreateManaged(ctx, "ivanov", "пароль-123456", "Иванов И.И.", true); err != nil {
		t.Fatalf("CreateManaged: %v", err)
	}
	u, err := repo.CreateManaged(ctx, "petrov", "пароль-123456", "Петров П.П.", false)
	if err != nil {
		t.Fatalf("CreateManaged(2): %v", err)
	}

	// Прикладная таблица с реквизитом-ссылкой на учётку — как её создаёт
	// CreateTableSQL для поля type: reference:_users.
	if _, err := db.Exec(ctx, `
		CREATE TABLE сотрудники (
			id TEXT PRIMARY KEY,
			наименование TEXT,
			учётнаязапись_id TEXT REFERENCES _users(id)
		)`); err != nil {
		t.Fatalf("create app table: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO сотрудники (id, наименование, учётнаязапись_id) VALUES (?, ?, ?)`,
		"99999999-9999-9999-9999-999999999999", "Иванов И.И.", u.ID,
	); err != nil {
		t.Fatalf("insert referencing row: %v", err)
	}

	if err := repo.Delete(ctx, u.ID); !errors.Is(err, auth.ErrUserReferenced) {
		t.Fatalf("Delete ссыланой учётки: err=%v, want ErrUserReferenced", err)
	}

	// После отвязки удаление проходит.
	if _, err := db.Exec(ctx, `UPDATE сотрудники SET учётнаязапись_id = NULL`); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if err := repo.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete отвязанной учётки: %v", err)
	}
	var users int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM _users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Fatalf("users = %d, want 1", users)
	}
}
