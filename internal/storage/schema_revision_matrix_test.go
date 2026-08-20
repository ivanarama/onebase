package storage_test

// Матричные тесты ревизии схемы (issue #1057). Затрагивается схема и SQL с
// ON CONFLICT, поэтому одно тело гоняется на SQLite и (при TEST_DATABASE_URL)
// на PostgreSQL: раздельные тесты расхождений диалектов не показывают.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// setSchemaRevision изображает базу, обслуженную платформой другой версии.
// Пишет напрямую, минуя RaiseSchemaRevision: тест обязан уметь поставить и
// ревизию из будущего, чего публичный подъём принципиально не делает.
func setSchemaRevision(t *testing.T, db *storage.DB, revision int) {
	t.Helper()
	ctx := context.Background()
	d := db.Dialect()
	q := fmt.Sprintf(`INSERT INTO _schema_revision (id, revision, updated_at, updated_by)
		VALUES (1, %s, %s, %s)
		ON CONFLICT (id) DO UPDATE SET revision = excluded.revision, updated_by = excluded.updated_by`,
		d.Placeholder(1), d.CurrentTimestampTZ(), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, revision, "onebase v9.9.9 (linux/amd64)"); err != nil {
		t.Fatalf("подготовка ревизии %d: %v", revision, err)
	}
}

// TestSchemaRevisionGateMatrix — полный цикл: незавершённый marker отказывает,
// атомарный подъём делает его валидным, база из будущего отказывает обеим СУБД
// одинаково.
func TestSchemaRevisionGateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		// Таблица есть, строки ещё нет: это не legacy, а оборванный/повреждённый
		// marker. Обычный consumer обязан отказать; exclusive upgrader ниже
		// атомарно восстановит singleton.
		rev, known, by, err := db.SchemaRevisionOf(ctx)
		if err != nil {
			t.Fatalf("SchemaRevisionOf на пустой таблице: %v", err)
		}
		if known || rev != 0 || by != "" {
			t.Fatalf("непроштампованная база: got (%d, %v, %q), want (0, false, \"\")", rev, known, by)
		}
		if err := db.CheckSchemaRevision(ctx); !errors.Is(err, storage.ErrSchemaRevisionIncomplete) {
			t.Fatalf("пустой marker error = %v, want ErrSchemaRevisionIncomplete", err)
		}

		// Подъём ставит ревизию этого бинаря и называет, кто её поставил.
		got, err := db.RaiseSchemaRevision(ctx)
		if err != nil {
			t.Fatalf("RaiseSchemaRevision: %v", err)
		}
		if got != storage.SchemaRevision {
			t.Fatalf("после подъёма ревизия %d, ожидалась %d", got, storage.SchemaRevision)
		}
		rev, known, by, err = db.SchemaRevisionOf(ctx)
		if err != nil {
			t.Fatalf("SchemaRevisionOf после подъёма: %v", err)
		}
		if !known || rev != storage.SchemaRevision {
			t.Fatalf("после подъёма прочитано (%d, %v), ожидалось (%d, true)", rev, known, storage.SchemaRevision)
		}
		if by == "" {
			// Без этой отметки отказ старого бинаря назовёт только числа, а
			// пользователю нужна версия, которая базу подняла (#1052).
			t.Error("updated_by пуст: отказ не сможет назвать, чем база обслужена")
		}

		// Повторный подъём идемпотентен: сервер штампует базу на каждом старте.
		if again, err := db.RaiseSchemaRevision(ctx); err != nil || again != storage.SchemaRevision {
			t.Fatalf("повторный подъём: got (%d, %v), want (%d, nil)", again, err, storage.SchemaRevision)
		}
		if err := db.CheckSchemaRevision(ctx); err != nil {
			t.Fatalf("своя же ревизия должна открываться: %v", err)
		}

		// База из будущего — тот самый случай #1053.
		future := storage.SchemaRevision + 5
		setSchemaRevision(t, db, future)
		err = db.CheckSchemaRevision(ctx)
		if err == nil {
			t.Fatal("база с ревизией новее открылась без возражений")
		}
		if !errors.Is(err, storage.ErrNewerSchema) {
			t.Fatalf("отказ не опознаётся как ErrNewerSchema: %v", err)
		}
		var newer *storage.NewerSchemaError
		if !errors.As(err, &newer) {
			t.Fatalf("отказ не несёт NewerSchemaError: %v", err)
		}
		if newer.Base != future || newer.Known != storage.SchemaRevision {
			t.Errorf("отказ называет (%d, %d), ожидалось (%d, %d)",
				newer.Base, newer.Known, future, storage.SchemaRevision)
		}
		// Текст отказа — то, что увидит администратор. Он обязан назвать обе
		// стороны и выход, иначе разбор снова будет стоить переписки.
		msg := err.Error()
		for _, want := range []string{fmt.Sprint(future), fmt.Sprint(storage.SchemaRevision), "--allow-newer-schema", "v9.9.9"} {
			if !strings.Contains(msg, want) {
				t.Errorf("в тексте отказа нет %q:\n%s", want, msg)
			}
		}

		// Монотонность: старый бинарь, пущенный с флагом обхода, не должен
		// «омолодить» базу и снять защиту со следующего запуска.
		stayed, err := db.RaiseSchemaRevision(ctx)
		if err != nil {
			t.Fatalf("подъём на базе из будущего: %v", err)
		}
		if stayed != future {
			t.Fatalf("подъём понизил ревизию до %d, база была на %d", stayed, future)
		}
	})
}

// TestSchemaRevisionAbsentTableMatrix — база, заведённая до #1057. Таблицы нет
// вовсе, и это НЕ повод отказывать: ретроактивно гейт не действует.
func TestSchemaRevisionAbsentTableMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, `DROP TABLE _schema_revision`); err != nil {
			t.Fatalf("удаление таблицы ревизии: %v", err)
		}
		rev, known, _, err := db.SchemaRevisionOf(ctx)
		if err != nil {
			t.Fatalf("SchemaRevisionOf без таблицы: %v", err)
		}
		if known || rev != 0 {
			t.Fatalf("база без таблицы: got (%d, %v), want (0, false)", rev, known)
		}
		if err := db.CheckSchemaRevision(ctx); err != nil {
			t.Fatalf("база, заведённая до #1057, должна открываться: %v", err)
		}
		// И такая база проштамповывается на общих основаниях: таблицу заводит
		// сам подъём, иначе защита никогда не включится на старых базах.
		if got, err := db.RaiseSchemaRevision(ctx); err != nil || got != storage.SchemaRevision {
			t.Fatalf("подъём на базе без таблицы: got (%d, %v), want (%d, nil)", got, err, storage.SchemaRevision)
		}
	})
}
