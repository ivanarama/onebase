package storage

// Персональный вид списка (#1485): прямые тесты storage-функций — перезапись
// upsert'ом и изоляция ключа length-префиксом (наивная склейка «сущность.юзер»
// сталкивала разные пары).
import (
	"context"
	"path/filepath"
	"testing"
)

func TestListViewSettingsRoundTripAndOverwrite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "listview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SaveListViewUserSettings(ctx, "Клиент", "u1", "tiles"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if v, err := db.GetListViewUserSettings(ctx, "Клиент", "u1"); err != nil || v != "tiles" {
		t.Fatalf("get после save = %q, %v; ожидалась tiles", v, err)
	}
	// Повторный upsert той же пары перезаписывает значение, а не падает.
	if err := db.SaveListViewUserSettings(ctx, "Клиент", "u1", "list"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if v, _ := db.GetListViewUserSettings(ctx, "Клиент", "u1"); v != "list" {
		t.Fatalf("upsert не перезаписал: %q", v)
	}
	// Неизвестная пара — пустой вид без ошибки.
	if v, err := db.GetListViewUserSettings(ctx, "Клиент", "никто"); err != nil || v != "" {
		t.Fatalf("отсутствующий ключ = %q, %v; ожидалось пусто", v, err)
	}
}

func TestListViewSettingsKeyIsolation(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "listview-keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Наивная склейка дала бы одинаковый ключ «listview.a.b.c» для обеих пар.
	if err := db.SaveListViewUserSettings(ctx, "a", "b.c", "tiles"); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if err := db.SaveListViewUserSettings(ctx, "a.b", "c", "list"); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if v, _ := db.GetListViewUserSettings(ctx, "a", "b.c"); v != "tiles" {
		t.Fatalf("ключ первой пары перезаписан второй: %q", v)
	}
	if v, _ := db.GetListViewUserSettings(ctx, "a.b", "c"); v != "list" {
		t.Fatalf("вторая пара не сохранилась отдельно: %q", v)
	}
}
