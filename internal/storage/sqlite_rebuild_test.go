package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// На SQLite ссылочный реквизит нельзя было удалить в принципе: платформа
// объявляет ссылки ограничением FOREIGN KEY прямо в CREATE TABLE, а SQLite
// отказывается удалять колонку, упомянутую в ограничении. Рекомендованная самим
// CLI команда `onebase migrate --allow-destructive` падала сырой ошибкой
// движка, причём посреди прогона (#615).
//
// Тот же путь у ретайпа: retypeSQLite удаляет старую колонку, поэтому смена
// типа ссылочного реквизита роняла ОБЫЧНЫЙ migrate, без флагов, и не давала
// стартовать серверу.

func refEntities(fieldType string) []*metadata.Entity {
	supplier := &metadata.Entity{Name: "Поставщики", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString}}}
	goods := &metadata.Entity{Name: "Товары", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString}}}
	if fieldType != "" {
		f := metadata.Field{ID: "f_sup", Name: "Поставщик", Type: metadata.FieldType(fieldType)}
		if fieldType == "reference:Поставщики" {
			f.Type = metadata.FieldType("reference")
			f.RefEntity = "Поставщики"
		}
		goods.Fields = append(goods.Fields, f)
	}
	return []*metadata.Entity{supplier, goods}
}

func openRebuildDB(t *testing.T) *DB {
	t.Helper()
	db, err := ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// Удаление ссылочного реквизита проходит, данные прочих колонок целы.
func TestDropColumn_СсылочныйРеквизитНаSQLite(t *testing.T) {
	ctx := context.Background()
	db := openRebuildDB(t)
	withRef := refEntities("reference:Поставщики")
	if err := db.Migrate(ctx, withRef); err != nil {
		t.Fatalf("Migrate(со ссылкой): %v", err)
	}
	goods := withRef[1]
	id := uuid.New()
	if err := db.Upsert(ctx, goods.Name, id, map[string]any{"Наименование": "Гвоздь"}, goods); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	db.schemaOpts.AllowDestructive = true
	if err := db.Migrate(ctx, refEntities("")); err != nil {
		t.Fatalf("Migrate(без ссылки): %v", err)
	}

	without := refEntities("")[1]
	row, err := db.GetByID(ctx, without.Name, id, without)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got, _ := row["Наименование"].(string); got != "Гвоздь" {
		t.Errorf("данные потеряны при пересоздании: Наименование=%q", got)
	}
	cols, err := tableColumnNames(ctx, db, metadata.TableName(without.Name))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cols {
		if c == "поставщик_id" {
			t.Errorf("колонка не удалена: %v", cols)
		}
	}
}

// Смена типа ссылочного реквизита на булев идёт тем же dropColumn, но
// разрешения на разрушение НЕ требует — значит роняет обычный `onebase migrate`
// без единого флага и не даёт стартовать серверу.
//
// Именно bool, а не строка: у строки тип колонки тот же (TEXT), ретайпа не
// происходит вовсе и падать нечему — на этом сценарии тест был бы зелёным и на
// сломанном коде.
func TestRetype_СсылочногоРеквизитаВБулев(t *testing.T) {
	ctx := context.Background()
	db := openRebuildDB(t)
	if err := db.Migrate(ctx, refEntities("reference:Поставщики")); err != nil {
		t.Fatalf("Migrate(со ссылкой): %v", err)
	}
	if err := db.Migrate(ctx, refEntities("bool")); err != nil {
		t.Fatalf("обычная миграция не должна падать на смене типа ссылки: %v", err)
	}
	cols, err := tableColumnNames(ctx, db, "товары")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range cols {
		if c == "поставщик" {
			found = true
		}
		if c == "поставщик_id" {
			t.Errorf("старая ссылочная колонка осталась: %v", cols)
		}
	}
	if !found {
		t.Errorf("колонка нового типа не создана: %v", cols)
	}
}

// Разбор определения таблицы: колонка и упоминающие её ограничения уходят,
// остальное остаётся дословно.
func TestCreateWithoutColumn(t *testing.T) {
	const src = `CREATE TABLE "товары" (
    id TEXT PRIMARY KEY,
    наименование TEXT,
    поставщик_id TEXT,
    FOREIGN KEY (поставщик_id) REFERENCES "поставщики"(id)
)`
	got, ok := createWithoutColumn(src, "товары", "поставщик_id")
	if !ok {
		t.Fatal("определение не построено")
	}
	for _, must := range []string{"id TEXT PRIMARY KEY", "наименование TEXT"} {
		if !contains(got, must) {
			t.Errorf("потеряно %q:\n%s", must, got)
		}
	}
	for _, mustNot := range []string{"поставщик_id", "FOREIGN KEY"} {
		if contains(got, mustNot) {
			t.Errorf("осталось %q:\n%s", mustNot, got)
		}
	}
}

// Запятая внутри REFERENCES не должна разрывать элемент списка.
func TestSplitTopLevel_ВложенныеСкобки(t *testing.T) {
	got := splitTopLevel(`a TEXT, FOREIGN KEY (x) REFERENCES t(id), UNIQUE (a, b)`)
	if len(got) != 3 {
		t.Fatalf("элементов %d, ожидалось 3: %q", len(got), got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
