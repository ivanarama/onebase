package launcher

import (
	"net/http"
	"net/url"
	"testing"
)

// Индексы регистра сведений форма полей не редактирует — сохранение полей
// обязано перенести их из файла, иначе правка заголовка молча снимала бы
// индекс с таблицы на следующей миграции.
func TestSaveInfoReg_KeepsIndexes(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFileRv(t, cfgDir, "inforegs", "Зоны.yaml", `name: Зоны
periodic: true
dimensions:
  - name: Адрес
    type: string
resources:
  - name: Сумма
    type: number
indexes:
  - fields: [Адрес, period]
`)
	form := url.Values{}
	form.Set("inforeg", "Зоны")
	form.Set("periodic", "true")
	form.Set("dim.0.name", "Адрес")
	form.Set("dim.0.type", "string")
	form.Set("res.0.name", "Сумма")
	form.Set("res.0.type", "number")

	rec := postCfgRv(t, "test", "/bases/test/configurator/inforeg-fields", form, h.configuratorSaveInfoRegFields)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	assertFileContainsRv(t, p, "indexes:", "fields: [Адрес, period]")
}
