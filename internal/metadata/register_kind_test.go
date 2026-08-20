package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

// Вид регистра (план 151): kind:обороты → оборотный, отсутствие/прочее → балансовый.
func TestLoadRegisterFile_Kind(t *testing.T) {
	cases := []struct {
		name       string
		yaml       string
		wantTurnov bool
	}{
		{"по умолчанию — балансовый", "name: Остатки\nresources:\n  - {name: Кол}\n", false},
		{"kind: turnover", "name: Обороты\nkind: turnover\nresources:\n  - {name: Сумма}\n", true},
		{"kind: обороты", "name: Обороты\nkind: обороты\nresources:\n  - {name: Сумма}\n", true},
		{"kind: balance", "name: Остатки\nkind: balance\nresources:\n  - {name: Кол}\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "reg.yaml")
			if err := os.WriteFile(p, []byte(c.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			reg, err := LoadRegisterFile(p)
			if err != nil {
				t.Fatalf("LoadRegisterFile: %v", err)
			}
			if reg.IsTurnover() != c.wantTurnov {
				t.Errorf("IsTurnover()=%v, ждали %v (kind=%q)", reg.IsTurnover(), c.wantTurnov, reg.Kind)
			}
		})
	}
}
