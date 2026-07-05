package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestReportUserSettings(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatalf("EnsureSettingsSchema: %v", err)
	}

	// Пусто до сохранения.
	got, err := db.GetReportUserSettings(ctx, "Продажи", "alice")
	if err != nil {
		t.Fatalf("Get(пусто): %v", err)
	}
	if got != "" {
		t.Fatalf("ожидали пусто, получили %q", got)
	}

	// Сохранение и чтение.
	raw := `{"variant":"X"}`
	if err := db.SaveReportUserSettings(ctx, "Продажи", "alice", raw); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err = db.GetReportUserSettings(ctx, "Продажи", "alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != raw {
		t.Fatalf("Get: хотели %q, получили %q", raw, got)
	}

	// Настройки разных пользователей не пересекаются (ключи …alice vs …bob).
	if err := db.SaveReportUserSettings(ctx, "Продажи", "bob", `{"variant":"Y"}`); err != nil {
		t.Fatalf("Save(bob): %v", err)
	}
	if alice, _ := db.GetReportUserSettings(ctx, "Продажи", "alice"); alice != raw {
		t.Fatalf("alice затёрта bob: %q", alice)
	}
	if bob, _ := db.GetReportUserSettings(ctx, "Продажи", "bob"); bob != `{"variant":"Y"}` {
		t.Fatalf("bob: %q", bob)
	}

	// Сброс (Delete) затрагивает только alice.
	if err := db.DeleteReportUserSettings(ctx, "Продажи", "alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, _ := db.GetReportUserSettings(ctx, "Продажи", "alice"); got != "" {
		t.Fatalf("после Delete ожидали пусто, получили %q", got)
	}
	if bob, _ := db.GetReportUserSettings(ctx, "Продажи", "bob"); bob == "" {
		t.Fatal("Delete(alice) не должен затрагивать bob")
	}
}

// TestReportSettingsKeyNoCollision: ключ _settings однозначен — точки в имени
// отчёта/логина не дают коллизий (issue #22). (report="a.b",user="c") и
// (report="a",user="b.c") должны давать РАЗНЫЕ ключи.
func TestReportSettingsKeyNoCollision(t *testing.T) {
	k1 := reportSettingsKey("a.b", "c")
	k2 := reportSettingsKey("a", "b.c")
	if k1 == k2 {
		t.Fatalf("ключи отчётов с точками совпали (коллизия): %q == %q", k1, k2)
	}

	// Проверка на реальном хранилище: настройки не перетирают друг друга.
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "collide.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatalf("EnsureSettingsSchema: %v", err)
	}
	if err := db.SaveReportUserSettings(ctx, "a.b", "c", `{"variant":"AB"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveReportUserSettings(ctx, "a", "b.c", `{"variant":"A"}`); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.GetReportUserSettings(ctx, "a.b", "c"); v != `{"variant":"AB"}` {
		t.Fatalf("(a.b,c) затёрта: %q", v)
	}
	if v, _ := db.GetReportUserSettings(ctx, "a", "b.c"); v != `{"variant":"A"}` {
		t.Fatalf("(a,b.c) затёрта: %q", v)
	}
}

func TestJournalUserSettings(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "journal-settings.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatalf("EnsureSettingsSchema: %v", err)
	}

	if got, err := db.GetJournalUserSettings(ctx, "Входящие", "alice"); err != nil || got != "" {
		t.Fatalf("empty journal settings: got %q err=%v", got, err)
	}
	raw := `{"columns":[{"field":"Номер","visible":true}]}`
	if err := db.SaveJournalUserSettings(ctx, "Входящие", "alice", raw); err != nil {
		t.Fatalf("SaveJournalUserSettings: %v", err)
	}
	if got, err := db.GetJournalUserSettings(ctx, "Входящие", "alice"); err != nil || got != raw {
		t.Fatalf("GetJournalUserSettings: got %q err=%v", got, err)
	}
	if err := db.SaveJournalUserSettings(ctx, "Входящие", "bob", `{"columns":[]}`); err != nil {
		t.Fatalf("SaveJournalUserSettings bob: %v", err)
	}
	if got, _ := db.GetJournalUserSettings(ctx, "Входящие", "alice"); got != raw {
		t.Fatalf("alice settings overwritten by bob: %q", got)
	}
	if err := db.DeleteJournalUserSettings(ctx, "Входящие", "alice"); err != nil {
		t.Fatalf("DeleteJournalUserSettings: %v", err)
	}
	if got, _ := db.GetJournalUserSettings(ctx, "Входящие", "alice"); got != "" {
		t.Fatalf("journal settings after delete: %q", got)
	}
	if got, _ := db.GetJournalUserSettings(ctx, "Входящие", "bob"); got == "" {
		t.Fatal("DeleteJournalUserSettings(alice) must not delete bob settings")
	}
}

func TestJournalSettingsKeyNoCollision(t *testing.T) {
	k1 := journalSettingsKey("a.b", "c")
	k2 := journalSettingsKey("a", "b.c")
	if k1 == k2 {
		t.Fatalf("journal settings keys collided: %q == %q", k1, k2)
	}
}

func TestReportPresets(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "presets.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)

	id1, err := db.SaveReportPreset(ctx, ReportPreset{
		Report:       "Продажи",
		User:         "alice",
		Name:         "По товарам",
		SettingsJSON: `{"variant":"A"}`,
		IsDefault:    true,
	})
	if err != nil {
		t.Fatalf("SaveReportPreset #1: %v", err)
	}
	id2, err := db.SaveReportPreset(ctx, ReportPreset{
		Report:       "Продажи",
		User:         "alice",
		Name:         "По складам",
		SettingsJSON: `{"variant":"B"}`,
		IsDefault:    true,
	})
	if err != nil {
		t.Fatalf("SaveReportPreset #2: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("разные пресеты получили один id: %q", id1)
	}

	def, err := db.GetDefaultReportPreset(ctx, "Продажи", "alice")
	if err != nil {
		t.Fatalf("GetDefaultReportPreset: %v", err)
	}
	if def == nil || def.ID != id2 || !def.IsDefault {
		t.Fatalf("дефолт должен быть вторым пресетом: %+v", def)
	}

	list, err := db.ListReportPresets(ctx, "Продажи", "alice")
	if err != nil {
		t.Fatalf("ListReportPresets: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ожидали 2 пресета, got %+v", list)
	}
	if list[0].ID != id2 || !list[0].IsDefault {
		t.Fatalf("дефолтный пресет должен быть первым: %+v", list)
	}
	if list[1].ID != id1 || list[1].IsDefault {
		t.Fatalf("первый пресет должен перестать быть default: %+v", list)
	}

	got, err := db.GetReportPreset(ctx, "Продажи", "alice", id1)
	if err != nil {
		t.Fatalf("GetReportPreset: %v", err)
	}
	if got == nil || got.Name != "По товарам" || got.SettingsJSON != `{"variant":"A"}` {
		t.Fatalf("не тот пресет: %+v", got)
	}
	if other, _ := db.GetReportPreset(ctx, "Продажи", "bob", id1); other != nil {
		t.Fatalf("bob не должен видеть preset alice: %+v", other)
	}

	if _, err := db.SaveReportPreset(ctx, ReportPreset{
		ID:           id1,
		Report:       "Продажи",
		User:         "alice",
		Name:         "По товарам v2",
		SettingsJSON: `{"variant":"A2"}`,
	}); err != nil {
		t.Fatalf("update preset: %v", err)
	}
	updated, _ := db.GetReportPreset(ctx, "Продажи", "alice", id1)
	if updated == nil || updated.Name != "По товарам v2" || updated.SettingsJSON != `{"variant":"A2"}` {
		t.Fatalf("update не применился: %+v", updated)
	}

	if err := db.DeleteReportPreset(ctx, "Продажи", "alice", id1); err != nil {
		t.Fatalf("DeleteReportPreset: %v", err)
	}
	if deleted, _ := db.GetReportPreset(ctx, "Продажи", "alice", id1); deleted != nil {
		t.Fatalf("пресет должен быть удалён: %+v", deleted)
	}
}

func TestReportPresetRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preset-dup.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.SaveReportPreset(ctx, ReportPreset{
		Report:       "Продажи",
		User:         "alice",
		Name:         "По товарам",
		SettingsJSON: `{"variant":"A"}`,
	}); err != nil {
		t.Fatalf("SaveReportPreset #1: %v", err)
	}
	if _, err := db.SaveReportPreset(ctx, ReportPreset{
		Report:       "Продажи",
		User:         "alice",
		Name:         "По товарам",
		SettingsJSON: `{"variant":"B"}`,
	}); !errors.Is(err, ErrReportPresetNameExists) {
		t.Fatalf("duplicate name: got %v, want ErrReportPresetNameExists", err)
	}

	if _, err := db.SaveReportPreset(ctx, ReportPreset{
		Report:       "Продажи",
		User:         "bob",
		Name:         "По товарам",
		SettingsJSON: `{"variant":"B"}`,
	}); err != nil {
		t.Fatalf("same name for another user must be allowed: %v", err)
	}
}

func TestReportPresetReservedIDIsIgnored(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preset-reserved.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := db.SaveReportPreset(ctx, ReportPreset{
		ID:           StandardReportPresetID,
		Report:       "Продажи",
		User:         "alice",
		Name:         "Стандартный",
		SettingsJSON: `{"variant":"bad"}`,
	}); !errors.Is(err, ErrReservedReportPresetID) {
		t.Fatalf("reserved id save: got %v, want ErrReservedReportPresetID", err)
	}

	if _, err := db.Exec(ctx, `INSERT INTO _report_presets
		(id, report_name, user_login, name, settings_json, is_default)
		VALUES (?, ?, ?, ?, ?, ?)`,
		StandardReportPresetID, "Продажи", "alice", "Стандартный", `{"variant":"bad"}`, true); err != nil {
		t.Fatal(err)
	}
	if p, err := db.GetReportPreset(ctx, "Продажи", "alice", StandardReportPresetID); err != nil || p != nil {
		t.Fatalf("reserved preset must not be loaded by id: preset=%+v err=%v", p, err)
	}
	if p, err := db.GetDefaultReportPreset(ctx, "Продажи", "alice"); err != nil || p != nil {
		t.Fatalf("reserved preset must not become default: preset=%+v err=%v", p, err)
	}
	list, err := db.ListReportPresets(ctx, "Продажи", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("reserved preset must not be listed: %+v", list)
	}
}
