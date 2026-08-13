//go:build integration

package backup

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// DR-01 / issue #784: сквозной тест backup→restore для PostgreSQL. Раньше CI
// проверял только SQLite dump/restore и частные свойства PG-restore (отказ от
// битого gzip, единую транзакцию), но полного round-trip не было — бэкап мог
// успешно создаваться и при этом не восстанавливать актуальные данные. Тест
// выполняет реальный pg_dump → уничтожение данных → psql-restore и сверяет
// СОДЕРЖИМОЕ восстановленной базы, а не только код возврата.
//
// Работает на ВЫДЕЛЕННОЙ базе (Restore дропает все таблицы схемы public и
// накатывает дамп — операция уровня всей БД), чтобы не затронуть общую
// сервисную onebase_test, которую делят остальные интеграционные пакеты.
// Требует pg_dump и psql в PATH: их отсутствие обязано валить джоб, а не
// пропускать проверку.
func TestPostgresBackupRestoreRoundTrip(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	dedicatedDSN, adminDSN, dbName := deriveRoundTripDSN(t, baseDSN)

	// Чистый старт и гарантированная уборка выделенной базы.
	dropRoundTripDatabase(ctx, t, adminDSN, dbName)
	if err := storage.EnsureDatabase(ctx, dedicatedDSN); err != nil {
		t.Fatalf("EnsureDatabase: %v", err)
	}
	t.Cleanup(func() { dropRoundTripDatabase(context.Background(), t, adminDSN, dbName) })

	client := &metadata.Entity{
		Name:   "Контрагент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}

	// Наполняем контрольными данными через обычный слой storage.
	db, err := storage.Connect(ctx, dedicatedDSN)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := db.Migrate(ctx, []*metadata.Entity{client}); err != nil {
		db.Close()
		t.Fatalf("Migrate: %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, "Контрагент", id, map[string]any{"Наименование": "ООО Контроль"}, client); err != nil {
		db.Close()
		t.Fatalf("Upsert: %v", err)
	}
	// Закрываем: pg_dump подключается сам, а Restore должен суметь дропнуть
	// таблицы (открытое соединение помешало бы).
	db.Close()

	// Бэкап.
	dir := t.TempDir()
	backupPath, err := Dump(ctx, dedicatedDSN, dir)
	if err != nil {
		t.Fatalf("Dump (нужен pg_dump в PATH): %v", err)
	}

	// «Авария»: полностью сносим таблицу с данными.
	db2, err := storage.Connect(ctx, dedicatedDSN)
	if err != nil {
		t.Fatalf("Connect(disaster): %v", err)
	}
	if _, err := db2.Exec(ctx, `DROP TABLE IF EXISTS `+metadata.TableName("Контрагент")+` CASCADE`); err != nil {
		db2.Close()
		t.Fatalf("симуляция аварии (DROP TABLE): %v", err)
	}
	db2.Close()

	// Восстановление из бэкапа.
	if err := Restore(ctx, dedicatedDSN, backupPath); err != nil {
		t.Fatalf("Restore (нужен psql в PATH): %v", err)
	}

	// Проверяем СОДЕРЖИМОЕ восстановленной базы, а не только успех команды.
	db3, err := storage.Connect(ctx, dedicatedDSN)
	if err != nil {
		t.Fatalf("Connect(verify): %v", err)
	}
	defer db3.Close()
	row, err := db3.GetByID(ctx, "Контрагент", id, client)
	if err != nil {
		t.Fatalf("после restore запись не найдена — round-trip не восстановил данные: %v", err)
	}
	if got, _ := row["Наименование"].(string); got != "ООО Контроль" {
		t.Fatalf("после restore Наименование = %q, ожидалось «ООО Контроль»", got)
	}
}

// deriveRoundTripDSN строит DSN выделенной базы и maintenance-базы из базового
// TEST_DATABASE_URL, меняя только имя базы.
func deriveRoundTripDSN(t *testing.T, baseDSN string) (dedicated, admin, dbName string) {
	t.Helper()
	u, err := url.Parse(baseDSN)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Skipf("TEST_DATABASE_URL не в формате postgres URL (%v) — round-trip пропущен", err)
	}
	dbName = "onebase_roundtrip_test"
	du := *u
	du.Path = "/" + dbName
	au := *u
	au.Path = "/postgres"
	return du.String(), au.String(), dbName
}

// dropRoundTripDatabase удаляет выделенную базу, предварительно обрубив чужие
// подключения (иначе DROP DATABASE заблокируется).
func dropRoundTripDatabase(ctx context.Context, t *testing.T, adminDSN, dbName string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("подключение к maintenance-базе: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`,
		dbName); err != nil {
		t.Logf("pg_terminate_backend(%s): %v", dbName, err)
	}
	safe := strings.ReplaceAll(dbName, `"`, `""`)
	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS "`+safe+`"`); err != nil {
		t.Fatalf("DROP DATABASE %s: %v", dbName, err)
	}
}
