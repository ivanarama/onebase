package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// «Колонки нет» и «прочитать не удалось» — разные ответы (#888, следствие #611).
//
// ListTOTPSecrets намеренно НЕ глотает ошибку Query: база без миграции плана 84
// законно возвращает пусто, а сбой соединения обязан дойти до вызывающего. Без
// этого различия `secret rotate` и disableUnreadableTOTP на транзиентной ошибке
// не сделали бы ничего и отрапортовали об успехе — ровно тот сорт зелёного
// молчания, из-за которого завели #611.
func totpDB(t *testing.T, name string) *DB {
	t.Helper()
	db, err := ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestListTOTPSecrets_БезТаблицыПользователей(t *testing.T) {
	ctx := context.Background()
	db := totpDB(t, "no-users.db")

	rows, err := db.ListTOTPSecrets(ctx)
	if err != nil {
		t.Fatalf("база без _users — не ошибка: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("секретов быть не может: %+v", rows)
	}
}

func TestListTOTPSecrets_ТаблицаБезКолонкиTOTP(t *testing.T) {
	ctx := context.Background()
	db := totpDB(t, "no-column.db")
	// База, не прошедшая миграцию плана 84: пользователи есть, второго фактора нет.
	if _, err := db.Exec(ctx,
		`CREATE TABLE _users (id TEXT PRIMARY KEY, login TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _users(id, login) VALUES ('u1','admin')`); err != nil {
		t.Fatal(err)
	}

	rows, err := db.ListTOTPSecrets(ctx)
	if err != nil {
		t.Fatalf("отсутствие колонки totp_secret — не ошибка: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("секретов быть не может: %+v", rows)
	}
	// И через публичный путь ротации мастер-ключа — он и есть потребитель.
	carriers, err := db.TOTPSecretCarriers(ctx)
	if err != nil {
		t.Fatalf("носители секретов: %v", err)
	}
	if len(carriers) != 0 {
		t.Fatalf("носителей быть не может: %+v", carriers)
	}
}

// Сбой чтения обязан дойти наверх, а не превратиться в «секретов нет».
func TestListTOTPSecrets_СбойЧтенияНеМолчит(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE _users (
		id TEXT PRIMARY KEY, login TEXT NOT NULL, totp_secret TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO _users(id, login, totp_secret) VALUES ('u1','admin','enc:v1:AAAA')`); err != nil {
		t.Fatal(err)
	}
	// Секрет читается, пока база жива, — иначе тест ниже проверял бы не то.
	if rows, err := db.ListTOTPSecrets(ctx); err != nil || len(rows) != 1 {
		t.Fatalf("подготовка: rows=%+v err=%v", rows, err)
	}

	db.Close() // сбой соединения — то, что в проде выглядит как обрыв или таймаут

	rows, err := db.ListTOTPSecrets(ctx)
	if err == nil {
		t.Fatalf("сбой чтения выдан за «секретов нет»: %+v", rows)
	}
	if len(rows) != 0 {
		t.Fatalf("при ошибке возвращены строки: %+v", rows)
	}
	// Потребитель ротации получает ту же ошибку, а не пустой список.
	if _, err := db.TOTPSecretCarriers(ctx); err == nil {
		t.Fatal("TOTPSecretCarriers проглотил ошибку — rotate решил бы, что перешифровывать нечего")
	} else if !strings.Contains(err.Error(), "totp") && !strings.Contains(err.Error(), "TOTP") &&
		!strings.Contains(err.Error(), "секрет") {
		t.Logf("текст ошибки без упоминания TOTP: %v", err)
	}
}
