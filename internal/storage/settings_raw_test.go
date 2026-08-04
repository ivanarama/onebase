package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// Секрет TOTP лежит колонкой _users.totp_secret, а не в _settings, поэтому
// раньше он не попадал ни в `onebase secret list`, ни в `secret rotate`:
// штатная ротация мастер-ключа молча ломала второй фактор у всех сразу,
// а команда печатала «перешифровывать нечего».
func TestSecretCarriers_ВключаютСекретTOTP(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "carriers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE _users (
		id TEXT PRIMARY KEY, login TEXT NOT NULL, totp_secret TEXT NOT NULL DEFAULT '',
		totp_enabled BOOLEAN NOT NULL DEFAULT 0, totp_last_step BIGINT NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _users(id, login, totp_secret) VALUES ('u1','admin','enc:v1:AAAA')`); err != nil {
		t.Fatal(err)
	}

	carriers, err := db.SecretCarriers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, c := range carriers {
		if c.Path == "auth.user.admin.totp_secret" {
			found = c.Value
		}
	}
	if found != "enc:v1:AAAA" {
		t.Fatalf("секрет TOTP не попал в носители секретов: %+v", carriers)
	}

	// Перешифровка на месте — тот путь, которым пользуется secret rotate.
	if err := db.SaveTOTPSecretRaw(ctx, "u1", "enc:v2:BBBB"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListTOTPSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Value != "enc:v2:BBBB" {
		t.Fatalf("перешифрованный секрет не записан: %+v", rows)
	}
}
