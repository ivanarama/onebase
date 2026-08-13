package launcher

import (
	"strings"
	"testing"
)

// ARCH-03 / issue #789: конструктор запросов конфигуратора должен предлагать и
// регистры бухгалтерии — как UI-конструктор (internal/ui/query_builder.go).
// Раньше launcher-копия схемы их пропускала (расхождение двух копий).
func TestBuildQBSchema_IncludesAccountRegisters(t *testing.T) {
	d := &configuratorData{
		AccountRegisters: []cfgAccountRegister{{
			Name:      "Хозрасчётный",
			Resources: []cfgField{{Name: "Сумма"}},
		}},
	}
	js := string(buildQBSchema(d))
	for _, want := range []string{
		"vt_acct_bal:Хозрасчётный",
		"vt_acct_trn:Хозрасчётный",
		"РегистрБухгалтерии.Хозрасчётный.Остатки",
		"РегистрБухгалтерии.Хозрасчётный.Обороты",
		"СуммаОстаток", "СуммаДт", "СуммаКт",
		"Регистры бухгалтерии",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("схема QB конфигуратора не содержит %q; получено: %s", want, js)
		}
	}
}
