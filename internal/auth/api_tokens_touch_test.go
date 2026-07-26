package auth

// White-box тест троттлинга last_used_at API-токенов: интеграции ходят с
// высоким RPS, UPDATE на каждый запрос давал бы постоянный writer-трафик.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
)

func TestTouchAPITokenThrottled(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	repo := NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	user, err := repo.Create(ctx, "api", "password", "", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tok, _, err := repo.CreateAPIToken(ctx, "интеграция", user.ID, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	lastUsed := func() *time.Time {
		t.Helper()
		tokens, err := repo.ListAPITokens(ctx)
		if err != nil || len(tokens) != 1 {
			t.Fatalf("ListAPITokens: %v (%d)", err, len(tokens))
		}
		return tokens[0].LastUsedAt
	}

	base := time.Now()
	repo.touchAPIToken(ctx, tok.ID, base)
	first := lastUsed()
	if first == nil {
		t.Fatal("last_used_at должен быть заполнен после первого touch")
	}

	// Затираем метку напрямую: если бы touch писал каждый раз, значение бы
	// восстановилось. Троттлинг обязан пропустить запись внутри 5 минут.
	if _, err := db.Exec(ctx, `UPDATE _api_tokens SET last_used_at = NULL WHERE id = ?`, tok.ID); err != nil {
		t.Fatalf("сброс last_used_at: %v", err)
	}
	repo.touchAPIToken(ctx, tok.ID, base.Add(time.Minute))
	if lastUsed() != nil {
		t.Fatal("троттлинг: повторный touch внутри 5 минут не должен писать в БД")
	}

	// После интервала — запись проходит.
	repo.touchAPIToken(ctx, tok.ID, base.Add(6*time.Minute))
	if lastUsed() == nil {
		t.Fatal("после интервала touch обязан обновить last_used_at")
	}
}
