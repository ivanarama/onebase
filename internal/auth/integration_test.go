//go:build integration

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

func connectTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestRepo_CreateAndAuthenticate(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()

	repo := auth.NewRepo(db.Pool())
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// Clean up
	db.Pool().Exec(ctx, `DELETE FROM _sessions`)
	db.Pool().Exec(ctx, `DELETE FROM _users WHERE login = 'testuser'`)

	user, err := repo.Create(ctx, "testuser", "secret123", "Тестовый Юзер", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.ID == "" {
		t.Fatal("Create should set ID")
	}

	// Correct credentials
	got, err := repo.Authenticate(ctx, "testuser", "secret123")
	if err != nil {
		t.Fatalf("Authenticate with correct password: %v", err)
	}
	if got.Login != "testuser" {
		t.Fatalf("want login=testuser, got %q", got.Login)
	}

	// Wrong password
	_, err = repo.Authenticate(ctx, "testuser", "wrong")
	if err == nil {
		t.Fatal("Authenticate with wrong password should return error")
	}

	// Unknown user
	_, err = repo.Authenticate(ctx, "nobody", "pass")
	if err == nil {
		t.Fatal("Authenticate with unknown user should return error")
	}
}

func TestRepo_Sessions(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()

	repo := auth.NewRepo(db.Pool())
	repo.EnsureSchema(ctx)

	db.Pool().Exec(ctx, `DELETE FROM _sessions`)
	db.Pool().Exec(ctx, `DELETE FROM _users WHERE login = 'sesstest'`)

	user, _ := repo.Create(ctx, "sesstest", "pass", "", false)

	token, err := repo.CreateSession(ctx, user.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if token == "" {
		t.Fatal("CreateSession should return non-empty token")
	}

	// Valid lookup
	looked, err := repo.LookupSession(ctx, token)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if looked.ID != user.ID {
		t.Fatalf("want user %s, got %s", user.ID, looked.ID)
	}

	// Delete session
	if err := repo.DeleteSession(ctx, token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	_, err = repo.LookupSession(ctx, token)
	if err == nil {
		t.Fatal("LookupSession after delete should return error")
	}
}

func TestMiddleware_NoUsers_PassThrough(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()

	repo := auth.NewRepo(db.Pool())
	repo.EnsureSchema(ctx)
	// Ensure empty _users
	db.Pool().Exec(ctx, `DELETE FROM _sessions`)
	db.Pool().Exec(ctx, `DELETE FROM _users`)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := repo.Middleware(next)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("next handler should be called when no users exist")
	}
}

func TestMiddleware_WithUsers_RequiresSession(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()

	repo := auth.NewRepo(db.Pool())
	repo.EnsureSchema(ctx)

	db.Pool().Exec(ctx, `DELETE FROM _sessions`)
	db.Pool().Exec(ctx, `DELETE FROM _users WHERE login = 'mwtest'`)
	repo.Create(ctx, "mwtest", "pass", "", false)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := repo.Middleware(next)

	// No cookie → redirect
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("want 302 redirect, got %d", rr.Code)
	}

	// Valid session → pass through
	user, _ := repo.Authenticate(ctx, "mwtest", "pass")
	token, _ := repo.CreateSession(ctx, user.ID)
	req2 := httptest.NewRequest(http.MethodGet, "/ui", nil)
	req2.AddCookie(&http.Cookie{Name: "onebase_session", Value: token})
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("want 200 with valid session, got %d", rr2.Code)
	}
}
