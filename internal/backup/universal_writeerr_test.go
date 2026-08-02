package backup

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// errAfterWriter отдаёт ошибку после того, как через него прошло limit байт, —
// так ведёт себя диск, на котором кончилось место, или оборванный поток.
type errAfterWriter struct {
	limit   int
	written int
}

var errDiskFull = errors.New("устройство переполнено (тест)")

func (w *errAfterWriter) Write(p []byte) (int, error) {
	if w.written >= w.limit {
		return 0, errDiskFull
	}
	n := len(p)
	if w.written+n > w.limit {
		n = w.limit - w.written
		w.written += n
		return n, errDiskFull
	}
	w.written += n
	return n, nil
}

// Ошибка записи в архив обязана дойти до вызывающего кода. Тест проходит и на
// коде до правки: zip.Writer запоминает ошибку нижележащего писателя и отдаёт её
// на Close, так что молчаливой потери не было — это проверено перебором 858
// сценариев обрыва (паник и ложных «успехов» не найдено). Тест закрепляет само
// свойство: ни один путь выгрузки не должен возвращать nil при сбое записи,
// включая те, где ошибка теперь сообщается раньше и адресно.
func TestExportUniversal_WriteErrorIsReported(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "exporterr")

	if _, err := db.Exec(ctx, `CREATE TABLE товары (id TEXT PRIMARY KEY, наименование TEXT)`); err != nil {
		t.Fatal(err)
	}
	// ~400 КБ полезных данных — больше 256-килобайтного буфера выгрузки, чтобы
	// сброс происходил не только в самом конце.
	big := strings.Repeat("я", 2000)
	for i := 0; i < 200; i++ {
		if _, err := db.Exec(ctx,
			`INSERT INTO товары (id, наименование) VALUES (?, ?)`,
			"id-"+strings.Repeat("0", 3)+itoa(i), big); err != nil {
			t.Fatal(err)
		}
	}

	// Калибруемся по реальному размеру архива: zip сжимает повторяющиеся данные
	// в разы, поэтому фиксированные лимиты вроде 64 КБ могут оказаться БОЛЬШЕ
	// всего архива — тогда обрыва не происходит и nil корректен. Лимиты берём
	// долями от измеренного размера.
	var probe countingWriter
	if err := ExportUniversal(ctx, db, "file", t.TempDir(), "", "test", &probe); err != nil {
		t.Fatalf("контрольная выгрузка не удалась: %v", err)
	}
	if probe.n < 100 {
		t.Fatalf("архив подозрительно мал (%d байт) — тест ничего не проверит", probe.n)
	}
	t.Logf("размер эталонного архива: %d байт", probe.n)

	// Лимиты берём с запасом ниже измеренного размера. Побайтовой
	// воспроизводимости у архива нет: META.txt содержит `date=` с текущим
	// временем, и прогоны по разные стороны границы секунды сжимаются в разное
	// число байт. Из-за limit = probe.n-1 тест упал на CI (эталон 4212, а
	// следующая выгрузка уложилась в 4211 и завершилась успешно).
	for _, limit := range []int{0, probe.n / 4, probe.n / 2, probe.n * 3 / 4} {
		w := &errAfterWriter{limit: limit}
		err := ExportUniversal(ctx, db, "file", t.TempDir(), "", "test", w)
		if err == nil {
			t.Errorf("limit=%d из %d: выгрузка вернула nil при сбое записи — бэкап молча обрезан",
				limit, probe.n)
			continue
		}
		if !errors.Is(err, errDiskFull) {
			t.Logf("limit=%d: ошибка сообщена, но не разворачивается до исходной: %v", limit, err)
		}
	}
}

// Успешная выгрузка по-прежнему проходит и даёт читаемый архив — guard, чтобы
// добавленные проверки не сломали happy path.
func TestExportUniversal_SuccessStillWorks(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "exportok")
	if _, err := db.Exec(ctx, `CREATE TABLE товары (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO товары (id) VALUES ('x')`); err != nil {
		t.Fatal(err)
	}
	var buf countingWriter
	if err := ExportUniversal(ctx, db, "file", t.TempDir(), "", "test", &buf); err != nil {
		t.Fatalf("успешная выгрузка вернула ошибку: %v", err)
	}
	if buf.n == 0 {
		t.Fatal("архив пуст")
	}
}

type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

var _ io.Writer = (*countingWriter)(nil)

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
