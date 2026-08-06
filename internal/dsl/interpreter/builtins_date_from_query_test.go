package interpreter

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
)

// Дата, прочитанная ЗАПРОСОМ, приходит строкой: storage.RunQuery типизирует
// значения без метаданных, и на SQLite колонка timestamp возвращается как
// RFC3339-строка. Раньше Час/Минута/Год над таким значением молча отдавали
// Неопределено (toTime принимал только time.Time), Число() превращал это в 0, и
// конфигурации хранили час отдельным числовым полем при записи.

func callBuiltin(t *testing.T, name string, arg any) any {
	t.Helper()
	fn, ok := builtins[name]
	if !ok {
		t.Fatalf("встроенная функция %q не зарегистрирована", name)
	}
	got, err := fn([]any{arg}, "test", 1)
	if err != nil {
		t.Fatalf("%s(%v): %v", name, arg, err)
	}
	return got
}

func TestДатаИзЗапроса_ЧастиДатыДоступны(t *testing.T) {
	// Момент, как его пишет платформа: локальное время переводится в UTC и
	// сохраняется строкой RFC3339 (см. storage: t.UTC().Format(time.RFC3339)).
	местное := time.Date(2026, 7, 29, 17, 25, 37, 0, time.Local)
	хранимое := местное.UTC().Format(time.RFC3339)

	cases := []struct {
		fn   string
		want float64
	}{
		{"час", 17},
		{"минута", 25},
		{"секунда", 37},
		{"год", 2026},
		{"месяц", 7},
		{"день", 29},
	}
	for _, c := range cases {
		got := callBuiltin(t, c.fn, хранимое)
		f, ok := got.(float64)
		if !ok {
			t.Fatalf("%s(%q) = %#v, ожидалось число: дата из запроса не разобрана", c.fn, хранимое, got)
		}
		if f != c.want {
			t.Errorf("%s(%q) = %v, ожидалось %v (локальное время записи)", c.fn, хранимое, f, c.want)
		}
	}
}

func TestДатаИзЗапроса_ФорматыХранения(t *testing.T) {
	// Все раскладки, в которых дата может прийти из БД: RFC3339 с Z и со
	// смещением, «legacy» без смещения (локальное настенное время) и дата без
	// времени. Проверяем сам факт разбора — час зависит от раскладки.
	for _, s := range []string{
		"2026-07-29T14:25:37Z",
		"2026-07-29T17:25:37+03:00",
		"2026-07-29 17:25:37",
		"2026-07-29",
	} {
		if got := callBuiltin(t, "год", s); got != float64(2026) {
			t.Errorf("Год(%q) = %#v, ожидалось 2026", s, got)
		}
	}
}

func TestДатаИзЗапроса_НеДатаОстаётсяНеопределено(t *testing.T) {
	// Произвольная строка датой не становится: молчаливое приведение мусора к
	// дате хуже, чем честное Неопределено.
	for _, s := range []string{"", "не дата", "ЗАЯ-000001"} {
		if got := callBuiltin(t, "час", s); got != nil {
			t.Errorf("Час(%q) = %#v, ожидалось Неопределено", s, got)
		}
	}
}

// Сквозная проверка: значение проходит реальный путь «запись → SQLite → запрос»
// и только потом попадает во встроенную функцию.
func TestДатаИзЗапроса_СквознойПутьЧерезSQLite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "dates.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(ctx, `CREATE TABLE событие (дата TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	местное := time.Date(2026, 7, 29, 17, 25, 37, 0, time.Local)
	if _, err := db.Exec(ctx, `INSERT INTO событие (дата) VALUES (`+db.Dialect().Placeholder(1)+`)`,
		местное.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	rows, _, err := db.RunQuery(ctx, `SELECT дата FROM событие`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("строк %d, ожидалась 1", len(rows))
	}
	значение := rows[0]["дата"]

	if got := callBuiltin(t, "час", значение); got != float64(17) {
		t.Errorf("Час(значение из запроса) = %#v, ожидалось 17 (записан локальный 17:25)", got)
	}
}
