package dbtest_test

// Матричный хелпер обязан отдавать тесту ПОЛНУЮ базу (issue #827).
//
// Раньше он отдавал пустую, и на PostgreSQL тесты проходили только потому, что
// search_path подхватывал служебные таблицы из public — их клал туда пакет,
// отработавший раньше в общей сервисной базе. Пакет, запущенный отдельно
// (обычная отладка) или первым в CI, падал лавиной «current transaction is
// aborted», в которой отсутствующая таблица не называлась ни разу.

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestForEachDialect_ProvidesServiceSchema(t *testing.T) {
	// Таблицы, отсутствие которых уже приводило к тихим или непонятным
	// поломкам: журнал аудита, настройки, счётчики нумерации, константы.
	want := []string{"_audit", "_settings", "_numerators", "_constants"}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, table := range want {
			if !db.HasTable(ctx, table) {
				t.Errorf("служебной таблицы %s нет — тест зависит от порядка прогона", table)
			}
		}
	})
}

// Записать и прочитать константу можно сразу, без ручной подготовки схемы:
// именно этого не хватало пакетам, которые падали при отдельном запуске.
func TestForEachDialect_ServiceSchemaIsUsable(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.SetConstant(ctx, "Проверка", "значение"); err != nil {
			t.Fatalf("запись константы: %v", err)
		}
		vals, err := db.ListConstants(ctx)
		if err != nil {
			t.Fatalf("чтение констант: %v", err)
		}
		if vals["Проверка"] != "значение" {
			t.Errorf("константы = %v, ожидалось значение «значение»", vals)
		}
	})
}
