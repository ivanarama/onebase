package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Регрессия (план критических фиксов, #1): удаление одной строки периодического
// регистра сведений из списка не должно стирать остальные периоды. Список отдаёт
// машинный ключ period_key, обработчик удаления разбирает его через ParseRegPeriod
// и удаляет ровно одну запись. Раньше period_key не было, а период в hidden-поле
// (формат «02.01.2006») не парсился → period=nil → DELETE без условия по периоду
// сносил всю историю комбинации измерений.
func TestInfoRegList_PeriodKeyRoundTripsToDelete(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ir := &metadata.InfoRegister{
		Name:       "Курсы",
		Periodic:   true,
		Dimensions: []metadata.Field{{Name: "Валюта", Type: "string"}},
		Resources:  []metadata.Field{{Name: "Курс", Type: "number"}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}

	mustSet := func(p time.Time, course float64) {
		t.Helper()
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Валюта": "USD"}, map[string]any{"Курс": course}, &p); err != nil {
			t.Fatal(err)
		}
	}
	mustSet(time.Date(2026, 5, 1, 0, 0, 0, 0, time.Local), 95)
	mustSet(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local), 100)

	rows, err := db.InfoRegList(ctx, ir, RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("ожидалось 2 записи, получили %d", len(rows))
	}

	// Находим period_key майской строки — ровно так, как обработчик удаления
	// возьмёт его из hidden-поля списка.
	var mayKey string
	for _, row := range rows {
		key, _ := row["period_key"].(string)
		if key == "" {
			t.Fatalf("period_key пуст в строке %v", row)
		}
		p, ok := ParseRegPeriod(key)
		if !ok {
			t.Fatalf("ParseRegPeriod(%q) не разобрал период", key)
		}
		if p.In(time.Local).Month() == time.May {
			mayKey = key
		}
	}
	if mayKey == "" {
		t.Fatalf("не нашли period_key майской строки; rows=%v", rows)
	}

	p, _ := ParseRegPeriod(mayKey)
	if err := db.InfoRegDelete(ctx, ir, map[string]any{"Валюта": "USD"}, &p); err != nil {
		t.Fatal(err)
	}

	rows2, err := db.InfoRegList(ctx, ir, RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) != 1 {
		t.Fatalf("после удаления майской записи ожидалась 1 строка (июнь), получили %d", len(rows2))
	}
	if key, _ := rows2[0]["period_key"].(string); true {
		if rem, ok := ParseRegPeriod(key); !ok || rem.In(time.Local).Month() != time.June {
			t.Errorf("осталась не та запись: period_key=%q (ожидался июнь)", key)
		}
	}
}

// ParseRegPeriod должен разбирать оба транспортных формата period_key:
// RFC3339/SQLite UTC (несут инстант), старую зононезависимую SQLite-строку
// (локальные стенные часы), а также суточный «2006-01-02» из формы.
func TestParseRegPeriod(t *testing.T) {
	cases := []string{
		"2026-05-01T00:00:00+03:00",
		"2026-05-01T00:00:00.123456+03:00",
		"2026-05-01T00:00:00Z",
		"2026-05-01 00:00:00+03:00",
		"2026-05-01 00:00:00",
		"2026-05-01T00:00:00",
		"2026-05-01",
	}
	for _, s := range cases {
		got, ok := ParseRegPeriod(s)
		if !ok {
			t.Errorf("ParseRegPeriod(%q) = false, ожидался успех", s)
			continue
		}
		if local := got.In(time.FixedZone("UTC+3", 3*60*60)); local.Year() != 2026 || local.Month() != time.May || local.Day() != 1 {
			t.Errorf("ParseRegPeriod(%q) = %v, ожидался 2026-05-01", s, got)
		}
	}
	if _, ok := ParseRegPeriod("02.05.2026"); ok {
		// дисплей-формат не является валидным ключом — обработчик должен отказать,
		// а не молча удалить всё.
		t.Errorf("ParseRegPeriod не должен принимать дисплей-формат 02.05.2026")
	}
	if _, ok := ParseRegPeriod(""); ok {
		t.Errorf("ParseRegPeriod(\"\") должен возвращать false")
	}
}
