package storage_test

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// Добавленные пользователем поля панели «Отбор»: за пользователем и списком,
// пустой выбор удаляет запись.
func TestListFilterFieldsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if got := db.GetListFilterFields(ctx, "Заявка", "anna"); got != nil {
			t.Fatalf("до сохранения: %v", got)
		}
		if err := db.SaveListFilterFields(ctx, "Заявка", "anna", []string{"Модель", "Бренд"}); err != nil {
			t.Fatal(err)
		}
		if got := db.GetListFilterFields(ctx, "Заявка", "anna"); len(got) != 2 || got[0] != "Модель" {
			t.Fatalf("после сохранения: %v", got)
		}
		if got := db.GetListFilterFields(ctx, "Заявка", "oleg"); got != nil {
			t.Fatalf("чужой пользователь: %v", got)
		}
		if got := db.GetListFilterFields(ctx, "Задача", "anna"); got != nil {
			t.Fatalf("другой список: %v", got)
		}
		if err := db.SaveListFilterFields(ctx, "Заявка", "anna", nil); err != nil {
			t.Fatal(err)
		}
		if got := db.GetListFilterFields(ctx, "Заявка", "anna"); got != nil {
			t.Fatalf("после сброса: %v", got)
		}
	})
}
