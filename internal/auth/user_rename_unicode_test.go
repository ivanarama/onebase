package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// Уникальность логина при переименовании — без учёта регистра и для
// кириллицы/других Unicode-логинов на обоих диалектах (замечание ревью #2364):
// встроенная lower() SQLite меняет только ASCII, и «оператор» проходил мимо
// «Оператор».
func TestRenameUser_UnicodeCaseInsensitiveUniqueness(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		for _, l := range []string{"Оператор", "Équipe", "source", "source2"} {
			if _, err := repo.Create(ctx, l, "password1", l, false); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ from, to string }{{"source", "оператор"}, {"source2", "équipe"}} {
			if _, err := repo.RenameUser(ctx, tc.from, tc.to); !errors.Is(err, auth.ErrLoginTaken) {
				t.Fatalf("%s → %s: ожидалась ErrLoginTaken, получено %v", tc.from, tc.to, err)
			}
		}
		// Регистр своего же логина сменить можно: занятость считается по другим учёткам.
		if _, err := repo.RenameUser(ctx, "Оператор", "оператор"); err != nil {
			t.Fatalf("смена регистра своего логина: %v", err)
		}
	})
}
