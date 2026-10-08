package storage

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// Контракт точного sum() на SQLite: суммы нецелых значений точны, остальное
// поведение совпадает со встроенной функцией — от него зависят HAVING, COALESCE
// и счётчики вида SUM(CASE … THEN 1 ELSE 0 END) по всей платформе.
func TestSQLiteSum_ExactAndBuiltinContract(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "sum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	one := func(q string) any {
		t.Helper()
		var v any
		if err := db.QueryRow(ctx, q).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}

	cases := []struct {
		name  string
		query string
		want  any
	}{
		// Деньги хранятся текстом: встроенная функция давала 2.7755575615628914e-17.
		{"текстовые деньги в ноль", `SELECT sum(x) FROM (SELECT '0.1' AS x UNION ALL SELECT '0.2' UNION ALL SELECT '-0.3')`, float64(0)},
		{"без хвоста двоичной дроби", `SELECT sum(x) FROM (SELECT '0.1' AS x UNION ALL SELECT '0.2')`, float64(0.3)},
		// REAL из выражения (унарный минус над текстом) — как в Остатки().
		{"знаковая сумма остатка", `SELECT sum(CASE WHEN v = 'Приход' THEN x ELSE -x END) FROM (
			SELECT 'Приход' AS v, '0.1' AS x UNION ALL SELECT 'Приход', '0.2' UNION ALL SELECT 'Расход', '0.3')`, float64(0)},
		{"целые остаются INTEGER", `SELECT sum(x) FROM (SELECT 1 AS x UNION ALL SELECT 2 UNION ALL SELECT '3')`, int64(6)},
		{"счётчик в HAVING", `SELECT sum(CASE WHEN x > 1 THEN 1 ELSE 0 END) FROM (SELECT 1 AS x UNION ALL SELECT 2 UNION ALL SELECT 3)`, int64(2)},
		{"пустой набор — NULL", `SELECT sum(x) FROM (SELECT 1 AS x) WHERE 0`, nil},
		{"только NULL — NULL", `SELECT sum(x) FROM (SELECT NULL AS x UNION ALL SELECT NULL)`, nil},
		{"нечисловой текст — ноль и REAL", `SELECT sum(x) FROM (SELECT 'abc' AS x UNION ALL SELECT 1)`, float64(1)},
		{"числовой префикс текста", `SELECT sum(x) FROM (SELECT '12x' AS x UNION ALL SELECT 1)`, float64(13)},
		{"дробный префикс текста", `SELECT sum(x) FROM (SELECT '0.1x' AS x UNION ALL SELECT '0.2')`, float64(0.3)},
		{"экспонента перед хвостом", `SELECT sum(x) FROM (SELECT '1e2x' AS x UNION ALL SELECT 1)`, float64(101)},
		{"бесконечный REAL", `SELECT sum(1e400)`, math.Inf(1)},
		{"отрицательный бесконечный REAL", `SELECT sum(-1e400)`, math.Inf(-1)},
		{"встречные бесконечности дают NULL", `SELECT sum(x) FROM (SELECT 1e400 AS x UNION ALL SELECT -1e400)`, nil},
		{"сравнение суммы с нулём", `SELECT sum(x) > 0 FROM (SELECT '0.1' AS x UNION ALL SELECT '0.2' UNION ALL SELECT '-0.3')`, int64(0)},
		// Медленный путь: всё, что не помещается в 18 цифр фиксированной точки.
		{"целое из 19 цифр в тексте", `SELECT sum(x) FROM (SELECT '9000000000000000000' AS x UNION ALL SELECT '1')`, int64(9000000000000000001)},
		{"экспонента в тексте", `SELECT sum(x) FROM (SELECT '1e2' AS x UNION ALL SELECT '0.5')`, float64(100.5)},
		{"огромный порядок в тексте", `SELECT sum(x) FROM (SELECT '1e1000000000' AS x UNION ALL SELECT '1')`, math.Inf(1)},
		{"огромный порядок в числовом префиксе", `SELECT sum(x) FROM (SELECT '1e1000000000x' AS x UNION ALL SELECT '1')`, math.Inf(1)},
		{"очень малый порядок в тексте", `SELECT sum(x) FROM (SELECT '1e-1000000000' AS x UNION ALL SELECT '1')`, float64(1)},
		{"пробелы вокруг числа", `SELECT sum(x) FROM (SELECT ' 0.1 ' AS x UNION ALL SELECT '0.2')`, float64(0.3)},
		// Граница по фактической величине: текст за пределами double — бесконечность,
		// как у встроенного sum(), а не точное число, которое гасится встречным.
		{"встречные переполненные тексты дают NULL", `SELECT sum(x) FROM (SELECT '2e308' AS x UNION ALL SELECT '-2e308')`, nil},
		{"встречные 1e324 дают NULL", `SELECT sum(x) FROM (SELECT '1e324' AS x UNION ALL SELECT '-1e324')`, nil},
		{"переполненный текст — бесконечность", `SELECT sum(x) FROM (SELECT '2e308' AS x UNION ALL SELECT '1')`, math.Inf(1)},
		// Точная сумма вышла за диапазон double — дальше бесконечность, как у встроенного.
		{"переполнение суммы необратимо", `SELECT sum(x) FROM (SELECT '1e308' AS x UNION ALL SELECT '1e308' UNION ALL SELECT '-1e308')`, math.Inf(1)},
		{"сумма у края диапазона остаётся конечной", `SELECT sum(x) FROM (SELECT '1e308' AS x UNION ALL SELECT '-1e308' UNION ALL SELECT '1e308')`, float64(1e308)},
		// Неразрывный пробел для SQLite не пробел: строка нечисловая, вклад — ноль.
		{"неразрывный пробел — не пробел", "SELECT sum(x) FROM (SELECT ' 12' AS x UNION ALL SELECT 1)", float64(1)},
		{"переполнение фиксированной точки", `SELECT sum(x) FROM (SELECT '9000000000000.000001' AS x UNION ALL SELECT '9000000000000.000001')`, float64(18000000000000.000002)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := one(c.query); got != c.want {
				t.Fatalf("%s\n получили %#v (%T), ждали %#v (%T)", c.query, got, got, c.want, c.want)
			}
		})
	}

	t.Run("переполнение целой суммы — ошибка, как у встроенной", func(t *testing.T) {
		var v any
		err := db.QueryRow(ctx, `SELECT sum(x) FROM (SELECT 9223372036854775807 AS x UNION ALL SELECT 1)`).Scan(&v)
		if err == nil || !strings.Contains(err.Error(), "integer overflow") {
			t.Fatalf("ждали ошибку integer overflow, получили %v (%v)", err, v)
		}
	})

	t.Run("оконная сумма со сдвигом окна", func(t *testing.T) {
		rows, err := db.Query(ctx, `SELECT sum(x) OVER (ORDER BY i ROWS BETWEEN 1 PRECEDING AND CURRENT ROW)
			FROM (SELECT 1 AS i, '0.1' AS x UNION ALL SELECT 2, '0.2' UNION ALL SELECT 3, '-0.3' UNION ALL SELECT 4, 5)`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []any
		for rows.Next() {
			var v any
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		// Окна: {0.1}, {0.1, 0.2}, {0.2, −0.3}, {−0.3, 5}.
		want := []any{float64(0.1), float64(0.3), float64(-0.1), float64(4.7)}
		if len(got) != len(want) {
			t.Fatalf("строк %d, ждали %d: %v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("окно %d: получили %#v, ждали %#v (все: %v)", i+1, got[i], want[i], got)
			}
		}
	})

	t.Run("удаление MinInt64 из окна", func(t *testing.T) {
		rows, err := db.Query(ctx, `SELECT sum(x) OVER (ORDER BY i ROWS BETWEEN 3 PRECEDING AND CURRENT ROW)
			FROM (SELECT 1 AS i, 0.0 AS x UNION ALL SELECT 2, -9223372036854775808
			UNION ALL SELECT 3, 9223372036854775807 UNION ALL SELECT 4, 1
			UNION ALL SELECT 5, 2 UNION ALL SELECT 6, 3)`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got any
		for rows.Next() {
			if err := rows.Scan(&got); err != nil {
				t.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if got != float64(9223372036854776000) {
			t.Fatalf("последнее окно: получили %#v, ждали положительную сумму", got)
		}
	})
}
