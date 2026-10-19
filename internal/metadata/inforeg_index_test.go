package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadInfoRegBody(t *testing.T, body string) (*InfoRegister, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reg.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadInfoRegisterFile(p)
}

const infoRegIndexHead = `name: Зоны
periodic: true
dimensions:
  - name: Адрес
    type: string
  - name: Направление
    type: string
resources:
  - name: Сумма
    type: number
`

func TestInfoRegIndexes_ParsedFromYAML(t *testing.T) {
	ir, err := loadInfoRegBody(t, infoRegIndexHead+`indexes:
  - fields: [Адрес, Направление, period]
  - fields: [" Сумма "]
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ir.Indexes) != 2 || strings.Join(ir.Indexes[0].Fields, ",") != "Адрес,Направление,period" ||
		strings.Join(ir.Indexes[1].Fields, ",") != "Сумма" {
		t.Fatalf("indexes = %+v", ir.Indexes)
	}
}

// Ошибка в indexes видна при загрузке (onebase check), а не падением миграции.
func TestInfoRegIndexes_RejectedAtLoad(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"неизвестное поле": {infoRegIndexHead + "indexes:\n  - fields: [Адрес, Бригада]\n", "unknown field Бригада"},
		"пустой индекс":    {infoRegIndexHead + "indexes:\n  - fields: []\n", "fields is required"},
		"уникальный":       {infoRegIndexHead + "indexes:\n  - fields: [Адрес]\n    unique: true\n", "unique не поддерживается"},
		"period у непериодического": {strings.Replace(infoRegIndexHead, "periodic: true\n", "", 1) +
			"indexes:\n  - fields: [Адрес, period]\n", "unknown field period"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadInfoRegBody(t, c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ошибка = %v, ожидалось %q", err, c.want)
			}
		})
	}
}
