package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// SQLite не укорачивает файл после удаления — именно поэтому обслуживание
// вообще понадобилось. Тест показывает обе половины: файл раздут после
// удаления и уменьшается после Reclaim.
func TestReclaimShrinksSQLiteFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "vacuum.db")
	db, err := ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	e := &metadata.Entity{Name: "Товары", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("данные", 2000)
	for i := 0; i < 300; i++ {
		if err := db.Upsert(ctx, e.Name, uuid.New(), map[string]any{"Наименование": big}, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `DELETE FROM товары`); err != nil {
		t.Fatal(err)
	}

	afterDelete := fileSize(t, path)
	if err := db.Reclaim(ctx); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	afterVacuum := fileSize(t, path)

	if afterVacuum >= afterDelete {
		t.Fatalf("файл не уменьшился: %d → %d", afterDelete, afterVacuum)
	}
	// База осталась рабочей.
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM товары`).Scan(&n); err != nil {
		t.Fatalf("после обслуживания база не читается: %v", err)
	}
	if n != 0 {
		t.Fatalf("строк осталось %d", n)
	}
}

// Внутри транзакции обслуживание запрещено обеими СУБД — говорим об этом
// понятной ошибкой, а не проваливаемся в сообщение драйвера.
func TestReclaimRefusesInsideTransaction(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = db.WithTx(ctx, func(txCtx context.Context) error {
		return db.Reclaim(txCtx)
	})
	if err == nil {
		t.Fatal("обслуживание внутри транзакции должно отклоняться")
	}
	if !strings.Contains(err.Error(), "транзакции") {
		t.Fatalf("непонятная ошибка: %v", err)
	}
}

func TestReclaimHintMentionsLock(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "hint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if hint := db.ReclaimHint(); !strings.Contains(hint, "заблокирована") {
		t.Fatalf("подсказка SQLite должна предупреждать о блокировке: %q", hint)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}
