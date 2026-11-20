package ui

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// Navigation scenarios exercise sessions, roles and HTTP authorization, not
// password creation. Keep a valid default-cost credential without repeating
// its expensive derivation for every user in every isolated database.
var schoolNavigationPasswordHash = sync.OnceValues(func() ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.DefaultCost)
})

func createSchoolNavigationUser(t *testing.T, db *storage.DB, login string) *auth.User {
	t.Helper()
	hash, err := schoolNavigationPasswordHash()
	if err != nil {
		t.Fatal(err)
	}
	user := &auth.User{ID: uuid.NewString(), Login: login, FullName: login, IsAdmin: login == "admin"}
	d := db.Dialect()
	query := fmt.Sprintf(`INSERT INTO _users (id, login, password_hash, full_name, is_admin) VALUES (%s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5))
	if _, err := db.Exec(context.Background(), query, user.ID, user.Login, bytes.Clone(hash), user.FullName, user.IsAdmin); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestSchoolNavigationFixtureCredentials(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		schoolNavigationFixture(t, db, "testdata/navigation-school", true)
		repo := auth.NewRepo(db)
		ctx := context.Background()
		ids := map[string]bool{}
		for _, login := range []string{"admin", "alice", "bob"} {
			user, err := repo.Authenticate(ctx, login, "secret123")
			if err != nil {
				t.Fatalf("authenticate %s: %v", login, err)
			}
			if user.Login != login || user.FullName != login || user.IsAdmin != (login == "admin") {
				t.Fatalf("incorrect fixture identity for %s: %+v", login, user)
			}
			if _, err := uuid.Parse(user.ID); err != nil || ids[user.ID] {
				t.Fatalf("fixture user %s has invalid or duplicate ID %q", login, user.ID)
			}
			ids[user.ID] = true
			var hash []byte
			query := "SELECT password_hash FROM _users WHERE id = " + db.Dialect().Placeholder(1)
			if err := db.QueryRow(ctx, query, user.ID).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if cost, err := bcrypt.Cost(hash); err != nil || cost != bcrypt.DefaultCost {
				t.Fatalf("fixture credential cost: %d, %v", cost, err)
			}
			if login != "admin" {
				roles, err := repo.GetRolesForUser(ctx, user.ID)
				if err != nil || len(roles) == 0 {
					t.Fatalf("fixture role for %s: %v, %v", login, roles, err)
				}
			}
		}
		if _, err := repo.Authenticate(ctx, "alice", "incorrect-password"); err == nil {
			t.Fatal("fixture credential accepted an incorrect password")
		}
	})
}
