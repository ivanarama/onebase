package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestAuditSettings_Defaults(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Таблицы _settings ещё нет — должны вернуться значения по умолчанию.
	if got := db.GetAuditSettings(ctx); got != DefaultAuditSettings() {
		t.Errorf("без таблицы ожидались дефолты %+v, получили %+v", DefaultAuditSettings(), got)
	}
}

func TestAuditSettings_SaveLoad(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	want := AuditSettings{Enabled: true, Create: false, Update: true, Delete: false, Post: true, Login: true}
	if err := db.SaveAuditSettings(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := db.GetAuditSettings(ctx); got != want {
		t.Errorf("round-trip: сохранили %+v, прочитали %+v", want, got)
	}
}

// #41: на базе без пользователей (анонимный режим) аудит изменений данных
// всё равно пишется, если журнал включён.
func TestAudit_AnonymousCreateIsLogged(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}

	ent := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: "string"}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}

	// ctx без audit-пользователя — как при доступе к базе без авторизации.
	if err := db.Upsert(ctx, "Товар", uuid.New(), map[string]any{"Наименование": "Стол"}, ent); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _audit WHERE action = 'create'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("ожидалась 1 запись аудита создания в анонимном режиме, получили %d", n)
	}
}

// Чтение журнала: SQLite хранит колонку at как TEXT — scanAuditRows должен
// её разобрать. Иначе AuditSearch падает на Scan и /ui/admin/audit пуст.
func TestAuditSearch_RoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}

	rec := uuid.New()
	if err := db.Log(ctx, &AuditEntry{
		Action: "create", EntityKind: "catalog", EntityName: "Товар",
		RecordID: rec.String(), UserLogin: "tester",
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	entries, err := db.AuditSearch(ctx, AuditFilter{}, 50, 0)
	if err != nil {
		t.Fatalf("AuditSearch: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ожидалась 1 запись, получили %d", len(entries))
	}
	if entries[0].At.IsZero() {
		t.Error("колонка at не разобрана — время записи нулевое")
	}
	if entries[0].Action != "create" {
		t.Errorf("action = %q, ожидалось create", entries[0].Action)
	}

	byRec, err := db.AuditByRecord(ctx, "Товар", rec)
	if err != nil {
		t.Fatalf("AuditByRecord: %v", err)
	}
	if len(byRec) != 1 {
		t.Errorf("AuditByRecord: ожидалась 1 запись, получили %d", len(byRec))
	}

	decisionRec := uuid.New()
	if err := db.LogDecisionAction(ctx, "publish", "document", "ПубликацияПравила",
		decisionRec.String(), "DECISION-42", "publisher"); err != nil {
		t.Fatalf("LogDecisionAction: %v", err)
	}
	decisions, err := db.AuditSearch(ctx, AuditFilter{Action: "publish"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].DecisionID != "DECISION-42" ||
		decisions[0].UserLogin != "publisher" {
		t.Fatalf("decision audit round-trip = %+v", decisions)
	}
}

// При выключенном журнале аудит не пишется.
func TestAudit_DisabledNoWrite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAuditSettings(ctx, AuditSettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}

	ent := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: "string"}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, "Товар", uuid.New(), map[string]any{"Наименование": "Стол"}, ent); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _audit`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("при выключенном журнале аудит писаться не должен, получили %d записей", n)
	}
}
