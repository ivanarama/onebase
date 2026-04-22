package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/query"
)

func TestCompile_BalancesQuery(t *testing.T) {
	src := `ВЫБРАТЬ
  Номенклатура,
  СУММА(Количество) КАК Количество
ИЗ РегистрНакопления.ТоварноеДвижение
СГРУППИРОВАТЬ ПО Номенклатура
УПОРЯДОЧИТЬ ПО Номенклатура`

	r, err := query.Compile(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	sql := r.SQL
	if !strings.Contains(sql, "SELECT") {
		t.Errorf("expected SELECT, got: %s", sql)
	}
	if !strings.Contains(sql, "SUM(количество)") {
		t.Errorf("expected SUM(количество), got: %s", sql)
	}
	if !strings.Contains(sql, "рег_товарноедвижение") {
		t.Errorf("expected рег_товарноедвижение, got: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY") {
		t.Errorf("expected GROUP BY, got: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY") {
		t.Errorf("expected ORDER BY, got: %s", sql)
	}
	if len(r.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(r.Args))
	}
}

func TestCompile_WithParam(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура ИЗ РегистрНакопления.ТоварноеДвижение ГДЕ вид_движения = &ВидДвижения`

	r, err := query.Compile(src, map[string]any{"ВидДвижения": "Приход"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "$1") {
		t.Errorf("expected $1 placeholder, got: %s", r.SQL)
	}
	if len(r.Args) != 1 || r.Args[0] != "Приход" {
		t.Errorf("expected args=[Приход], got %v", r.Args)
	}
}

func TestCompile_StringLiteral(t *testing.T) {
	src := `ВЫБРАТЬ Номенклатура ИЗ РегистрНакопления.ТоварноеДвижение ГДЕ вид_движения = "Приход"`

	r, err := query.Compile(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SQL, "'Приход'") {
		t.Errorf("expected single-quoted string, got: %s", r.SQL)
	}
}
