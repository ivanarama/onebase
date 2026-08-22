package launcher

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// browserSubmitForEntity — форма реквизитов КОНКРЕТНОГО объекта. На странице
// конфигуратора таких форм столько же, сколько объектов в конфигурации, а
// browserSubmit берёт первую попавшуюся: тест, где объектов больше одного,
// незаметно сохранял бы чужой объект и проверял бы не то, что собирался.
func browserSubmitForEntity(t *testing.T, page, entityName string) url.Values {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("разбор HTML: %v", err)
	}
	var found url.Values
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			if action, ok := attr(n, "action"); ok && strings.HasSuffix(action, "/configurator/fields") {
				values := url.Values{}
				collectControls(n, values)
				if values.Get("entity") == entityName {
					found = values
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if found == nil {
		t.Fatalf("на странице нет формы реквизитов объекта %q", entityName)
	}
	return found
}

// Ключ `default` (план 153) редактор реквизитов не показывает, поэтому он
// переносится из прежнего состояния файла — как id. Проверка этого переноса
// обязана идти ПУБЛИЧНЫМ путём: пользователь не зовёт ensureFieldIDs, он
// нажимает «Сохранить типы полей», а по дороге к YAML стоят ещё разбор формы,
// applyFieldEdits и marshal через saveEntity. Тест на приватную функцию был бы
// зелёным и при потере ключа в любом из этих звеньев — ровно тот случай, что
// разбирался в #611.
//
// Форма снимается с реально отрисованной страницы, как её отправил бы браузер:
// значения, которых интерфейс не даёт, тест подставлять не должен.
func TestSaveFields_KeepsFieldDefault(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "catalogs", "Организация.yaml", `name: Организация
fields:
    - name: Наименование
      type: string
`)
	writeCfgFile(t, cfgDir, "constants", "константы.yaml", `constants:
  - name: НашаОрганизация
    type: reference:Организация
`)
	p := writeCfgFile(t, cfgDir, "documents", "РеализацияТоваров.yaml", `name: РеализацияТоваров
fields:
    - name: Дата
      type: date
      default: сейчас
    - name: Организация
      type: reference:Организация
      default: константа.НашаОрганизация
    - name: Склад
      type: reference:Организация
      default: единственный
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "РеализацияТоваров")

	// Единственное действие пользователя — добавить посторонний реквизит.
	form.Set("new_field.1.name", "Комментарий")
	form.Set("new_field.1.type", "string")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	assertFileContains(t, p,
		"Комментарий",     // правка применилась
		"default: сейчас", // дефолты уцелели все три
		"default: константа.НашаОрганизация",
		"default: единственный",
	)

	// И главное следствие: конфигурация осталась загружаемой, то есть дефолты
	// не только лежат в файле, но и проходят проверку ValidateDefaults.
	if after := h.loadCfgData(context.Background(), b, "tree"); after.Error != "" {
		t.Fatalf("после сохранения конфигурация не загружается: %s", after.Error)
	}
}
