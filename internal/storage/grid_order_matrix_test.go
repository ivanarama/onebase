package storage_test

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// Порядок колонок таблицы формы хранится в _settings за пользователем;
// пустой порядок удаляет запись. Ключ с точкой и «/» внутри частей не
// сталкивается с соседним (кодировка длиной-префиксом).
func TestFormGridOrderMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		const grid = "processor/ПоискЗаявок/ФормаОбъекта/ТЧНайденные"
		if got := db.GetFormGridOrder(ctx, grid, "anna"); got != nil {
			t.Fatalf("до сохранения: %v", got)
		}
		if err := db.SaveFormGridOrder(ctx, grid, "anna", []string{"Мастер", "Номер"}); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveFormGridOrder(ctx, grid, "anna", []string{"Адрес", "Мастер", "Номер"}); err != nil {
			t.Fatal(err)
		}
		if got := db.GetFormGridOrder(ctx, grid, "anna"); len(got) != 3 || got[0] != "Адрес" {
			t.Fatalf("после перезаписи: %v", got)
		}
		if got := db.GetFormGridOrder(ctx, grid, "oleg"); got != nil {
			t.Fatalf("чужой пользователь видит порядок: %v", got)
		}
		if got := db.GetFormGridOrder(ctx, grid+".x", "anna"); got != nil {
			t.Fatalf("соседний ключ видит порядок: %v", got)
		}
		if err := db.SaveFormGridOrder(ctx, grid, "anna", nil); err != nil {
			t.Fatal(err)
		}
		if got := db.GetFormGridOrder(ctx, grid, "anna"); got != nil {
			t.Fatalf("после сброса: %v", got)
		}
	})
}
