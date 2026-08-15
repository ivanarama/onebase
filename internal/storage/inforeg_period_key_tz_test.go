package storage_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Машинный ключ периода и даты-измерения не зависит от таймзоны ПРОЦЕССА (#945).
//
// Драйвер PostgreSQL отдаёт timestamptz как time.Time в локальной зоне
// процесса, а ключ форматировался как есть. Поэтому один и тот же момент давал
// «2026-08-15T12:30:45.123+03:00» на сервере приложения в Europe/Moscow и
// «2026-08-15T09:30:45.123Z» на сервере в UTC. Момент один, текст разный — а
// этим текстом строка адресуется на удаление из UI и уезжает в пакет обмена:
// два узла в разных зонах давали расходящиеся payload на одинаковых данных
// (тот же класс, что #912 с числами).
//
// В CI это было невидимо: раннеры работают в UTC, и «как есть» там совпадало с
// каноническим видом. Поэтому тест сам ставит процессу зону +03:00 — иначе он
// проверял бы машину, на которой запущен, а не код.
func TestInfoRegPeriodKeyIsTimezoneIndependent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		// SQLite хранит период строкой RFC3339 в UTC и читает её обратно в UTC —
		// на нём дефекта не было и воспроизвести его нечем.
		t.Skip("TEST_DATABASE_URL not set")
	}
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	ctx := context.Background()
	db, err := storage.ConnectWithSchema(ctx, dsn, storage.NewEphemeralSchemaName())
	if err != nil {
		t.Fatalf("подключение: %v", err)
	}
	t.Cleanup(db.Close)

	ir := &metadata.InfoRegister{
		Name:       "СрезTZ" + uuid.NewString()[:8],
		Periodic:   true,
		Dimensions: []metadata.Field{{Name: "Момент", Type: metadata.FieldTypeDate}},
		Resources:  []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	moment := time.Date(2026, 8, 15, 9, 30, 45, 123000000, time.UTC)
	if err := db.InfoRegSet(ctx, ir,
		map[string]any{"Момент": moment}, map[string]any{"Значение": "x"}, &moment); err != nil {
		t.Fatalf("запись: %v", err)
	}

	rows, err := db.InfoRegListWithKeyValues(ctx, ir, storage.RegFilter{})
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("строк %d, ожидалась 1", len(rows))
	}
	wantKey := moment.UTC().Format(time.RFC3339Nano)

	periodKey, _ := rows[0]["period_key"].(string)
	if periodKey != wantKey {
		t.Errorf("ключ периода = %q, ожидался канонический %q", periodKey, wantKey)
	}
	// Машинные значения измерений хранилище кладёт отдельной картой — это то,
	// что уезжает в форму удаления как есть.
	keys, ok := rows[0][storage.InfoRegKeyValuesField].(map[string]string)
	if !ok {
		t.Fatalf("в строке нет машинных значений измерений: %#v", rows[0])
	}
	if got := keys["Момент"]; got != wantKey {
		t.Errorf("ключ даты-измерения = %q, ожидался канонический %q", got, wantKey)
	}
	// Плюс явное утверждение про форму: смысл ключа не должен зависеть от того,
	// кто его прочитал.
	if !strings.HasSuffix(periodKey, "Z") {
		t.Errorf("ключ периода не в UTC: %q", periodKey)
	}
}
