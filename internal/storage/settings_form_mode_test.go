package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestFormOpenMode_GlobalDefaultAndSave(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Без ключа — дефолт pages.
	if got := db.GetFormOpenMode(ctx); got != FormModePages {
		t.Errorf("дефолт: ожидался %q, получено %q", FormModePages, got)
	}
	// Сохранили tabs — читается tabs.
	if err := db.SaveFormOpenMode(ctx, FormModeTabs); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := db.GetFormOpenMode(ctx); got != FormModeTabs {
		t.Errorf("после save tabs: получено %q", got)
	}
	// Битое значение → pages.
	if err := db.SaveFormOpenMode(ctx, "мусор"); err != nil {
		t.Fatalf("save мусор: %v", err)
	}
	if got := db.GetFormOpenMode(ctx); got != FormModePages {
		t.Errorf("битое значение должно дать %q, получено %q", FormModePages, got)
	}
}

func TestFormOpenMode_PersonalOverridesGlobal(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Глобально tabs, у пользователя ничего — эффективный tabs.
	if err := db.SaveFormOpenMode(ctx, FormModeTabs); err != nil {
		t.Fatal(err)
	}
	if got := db.EffectiveFormOpenMode(ctx, "ivan"); got != FormModeTabs {
		t.Errorf("без персонального → глобальный tabs, получено %q", got)
	}
	// Персонально pages — перебивает глобальный.
	if err := db.SaveUserFormOpenMode(ctx, "ivan", FormModePages); err != nil {
		t.Fatal(err)
	}
	if got := db.EffectiveFormOpenMode(ctx, "ivan"); got != FormModePages {
		t.Errorf("персональный pages должен перебить, получено %q", got)
	}
	// Другой пользователь не затронут — у него глобальный tabs.
	if got := db.EffectiveFormOpenMode(ctx, "petr"); got != FormModeTabs {
		t.Errorf("у другого пользователя глобальный tabs, получено %q", got)
	}
	// Сброс персонального ("" или "default") → снова глобальный.
	if err := db.SaveUserFormOpenMode(ctx, "ivan", "default"); err != nil {
		t.Fatal(err)
	}
	if got := db.GetUserFormOpenMode(ctx, "ivan"); got != "" {
		t.Errorf("после сброса персональный должен быть пуст, получено %q", got)
	}
	if got := db.EffectiveFormOpenMode(ctx, "ivan"); got != FormModeTabs {
		t.Errorf("после сброса → глобальный tabs, получено %q", got)
	}
	// Пустой логин (аноним) → глобальный.
	if got := db.EffectiveFormOpenMode(ctx, ""); got != FormModeTabs {
		t.Errorf("аноним → глобальный tabs, получено %q", got)
	}
}

// Закреплённый режим (ui.form_open_mode_locked): личные ключи перестают
// действовать без чистки данных, после снятия закрепления — снова действуют.
func TestFormOpenMode_LockIgnoresPersonal(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	if db.GetFormOpenModeLocked(ctx) {
		t.Fatal("по умолчанию режим не закреплён")
	}
	if err := db.SaveFormOpenMode(ctx, FormModeTabs); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveUserFormOpenMode(ctx, "ivan", FormModePages); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFormOpenModeLocked(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := db.EffectiveFormOpenMode(ctx, "ivan"); got != FormModeTabs {
		t.Errorf("при закреплении личный pages не должен действовать, получено %q", got)
	}
	if err := db.SaveFormOpenModeLocked(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := db.EffectiveFormOpenMode(ctx, "ivan"); got != FormModePages {
		t.Errorf("после снятия закрепления личный pages снова действует, получено %q", got)
	}
}
