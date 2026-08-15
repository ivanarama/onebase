package storage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Удаление измерения регистра сведений — на обоих диалектах (пункт #888,
// регрессия #616/#642).
//
// Измерение входит в первичный ключ, и снятие его метаданными меняет PK. Пути
// у диалектов разные по существу: SQLite не умеет менять PK и пересоздаёт
// таблицу (именно там осиротевшая колонка когда-то и терялась в обход отказа
// плана 81), PostgreSQL обходится ALTER TABLE. Тест был только на SQLite,
// то есть ровно на одной из двух реализаций.
func dropDimRegister(name string, withSklad bool) *metadata.InfoRegister {
	dims := []metadata.Field{{ID: "d_nom", Name: "Номенклатура", Type: "string"}}
	if withSklad {
		dims = append(dims, metadata.Field{ID: "d_sklad", Name: "Склад", Type: "string"})
	}
	return &metadata.InfoRegister{
		Name:       name,
		Dimensions: dims,
		Resources:  []metadata.Field{{ID: "r_price", Name: "Цена", Type: "number"}},
	}
}

func seedDropDim(t *testing.T, db *storage.DB, ir *metadata.InfoRegister) string {
	t.Helper()
	ctx := context.Background()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция v1: %v", err)
	}
	table := metadata.InfoRegTableName(ir.Name)
	if _, err := db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO %s (номенклатура, склад, цена) VALUES ('Гвозди','Основной','10')", table)); err != nil {
		t.Fatalf("вставка строки: %v", err)
	}
	return table
}

func hasColumn(t *testing.T, db *storage.DB, table, col string) bool {
	t.Helper()
	has, err := db.Dialect().ColumnExists(context.Background(), db, table, col)
	if err != nil {
		t.Fatalf("проверка колонки %s.%s: %v", table, col, err)
	}
	return has
}

// Без --allow-destructive измерение не удаляется: разрушительное изменение
// откладывается, данные остаются на месте.
func TestMigrateInfoRegisters_DropDimensionKeepsDataMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		name := "ЦеныНоменклатуры" + strings.ToLower(uuid.NewString()[:8])
		table := seedDropDim(t, db, dropDimRegister(name, true))

		if err := db.MigrateInfoRegisters(ctx,
			[]*metadata.InfoRegister{dropDimRegister(name, false)}); err != nil {
			t.Fatalf("миграция v2: %v", err)
		}

		if !hasColumn(t, db, table, "склад") {
			t.Fatal("колонка «склад» удалена без --allow-destructive — данные потеряны")
		}
		var sklad, price string
		if err := db.QueryRow(ctx, fmt.Sprintf(
			"SELECT склад, CAST(цена AS TEXT) FROM %s WHERE номенклатура='Гвозди'", table)).
			Scan(&sklad, &price); err != nil {
			t.Fatalf("строка исчезла: %v", err)
		}
		if sklad != "Основной" || price != "10" {
			t.Errorf("данные искажены: склад=%q цена=%q", sklad, price)
		}
	})
}

// С --allow-destructive измерение удаляется осознанно: колонка исчезает,
// остальные данные целы, а PK перестроен на оставшееся измерение — что и
// проверяется повторной записью того же ключа.
func TestMigrateInfoRegisters_DropDimensionWithPermissionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		name := "ЦеныНоменклатуры" + strings.ToLower(uuid.NewString()[:8])
		table := seedDropDim(t, db, dropDimRegister(name, true))

		db.SetSchemaOptions(storage.SchemaOptions{AllowDestructive: true})
		t.Cleanup(func() { db.SetSchemaOptions(storage.SchemaOptions{}) })
		v2 := dropDimRegister(name, false)
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{v2}); err != nil {
			t.Fatalf("миграция v2: %v", err)
		}

		if hasColumn(t, db, table, "склад") {
			t.Fatal("с --allow-destructive колонка «склад» должна исчезнуть")
		}
		var price string
		if err := db.QueryRow(ctx, fmt.Sprintf(
			"SELECT CAST(цена AS TEXT) FROM %s WHERE номенклатура='Гвозди'", table)).Scan(&price); err != nil {
			t.Fatalf("строка исчезла целиком: %v", err)
		}
		if price != "10" {
			t.Errorf("цена искажена: %q", price)
		}

		// PK действительно перестроен: запись по тому же ключу обновляет строку,
		// а не заводит вторую. На SQLite это результат пересоздания таблицы, на
		// PostgreSQL — ALTER TABLE; проверяем следствие, а не способ.
		if err := db.InfoRegSet(ctx, v2,
			map[string]any{"Номенклатура": "Гвозди"}, map[string]any{"Цена": 20}, nil); err != nil {
			t.Fatalf("повторная запись по ключу: %v", err)
		}
		var rows int
		if err := db.QueryRow(ctx, fmt.Sprintf(
			"SELECT COUNT(*) FROM %s WHERE номенклатура='Гвозди'", table)).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("строк по ключу «Гвозди»: %d — первичный ключ не перестроен", rows)
		}
	})
}
