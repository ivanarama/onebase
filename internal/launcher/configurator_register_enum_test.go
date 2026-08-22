package launcher

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func regEnumHandler(t *testing.T) (*handler, string) {
	t.Helper()
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "enums", "результатсделки.yaml", `name: РезультатСделки
values:
  - Выиграна
  - Проиграна
`)
	writeCfgFile(t, cfgDir, "enums", "способоплаты.yaml", `name: СпособОплаты
values:
  - Наличные
  - Карта
`)
	return h, cfgDir
}

// Измерение регистра типа enum: раньше его нельзя было ни увидеть, ни сменить.
// Список типов существующей строки перечислений не предлагал (завести кнопкой
// «+ Добавить» — можно, это и была асимметрия), а соседний список объектов
// показывал справочники и прятался. Тип держался только запасным пунктом: он
// спасал от порчи, но выбрать другое перечисление всё равно было нельзя —
// оставалась правка YAML руками.
//
// Проверяем тем же путём, что у пользователя: снимаем форму с отрисованной
// страницы и меняем в ней перечисление.
func TestSaveRegisterFields_EnumDimensionIsEditable(t *testing.T) {
	h, cfgDir := regEnumHandler(t)
	p := writeCfgFile(t, cfgDir, "registers", "воронкапродаж.yaml", `name: ВоронкаПродаж
dimensions:
    - name: Результат
      type: enum:РезультатСделки
resources:
    - name: Сумма
      type: number
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	page := renderCfgTree(t, data)

	// Список объекта обязан быть виден и показывать ИМЕННО перечисления, а не
	// справочники: иначе пользователь видит выбранным чужое значение.
	selects := typeSelectValues(t, page)
	if got := selects["dim.0.type"]; !containsValue(got, "enum") {
		t.Fatalf("в списке типа измерения нет пункта «перечисление»: %v", got)
	}
	if !strings.Contains(page, `<option value="РезультатСделки" selected>`) {
		t.Error("в списке объекта не выбрано текущее перечисление")
	}

	form := browserSubmit(t, page, "/configurator/register-fields")
	if got := form.Get("dim.0.type"); got != "enum" {
		t.Fatalf("форма отправляет тип %q вместо enum", got)
	}
	if got := form.Get("dim.0.ref"); got != "РезультатСделки" {
		t.Fatalf("форма отправляет объект %q вместо текущего перечисления", got)
	}

	// Пользователь меняет перечисление на другое.
	form.Set("dim.0.ref", "СпособОплаты")

	rec := postCfg(t, "test", "/bases/test/configurator/register-fields", form, h.configuratorSaveRegisterFields)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	assertFileContains(t, p, "type: enum:СпособОплаты")
	if after := h.loadCfgData(context.Background(), b, "tree"); after.Error != "" {
		t.Fatalf("после правки конфигурация не загружается: %s", after.Error)
	}
}

// «Выбрал ссылочный тип, не выбрал объект» уезжало в YAML голым «reference» —
// типом, которого не существует. У реквизитов объекта такая проверка была с
// самого начала, у регистров её не было; теперь, когда в списке появилось и
// перечисление, промахнуться стало ещё легче.
func TestSaveRegisterFields_RejectsRefTypeWithoutTarget(t *testing.T) {
	h, cfgDir := regEnumHandler(t)
	const original = `name: ВоронкаПродаж
dimensions:
    - name: Результат
      type: enum:РезультатСделки
`
	p := writeCfgFile(t, cfgDir, "registers", "воронкапродаж.yaml", original)

	for _, c := range []struct{ name, typ, want string }{
		{"перечисление без имени", "enum", "перечисление"},
		{"ссылка без объекта", "reference", "объект для ссылки"},
	} {
		form := url.Values{}
		form.Set("register", "ВоронкаПродаж")
		form.Set("dim.0.name", "Результат")
		form.Set("dim.0.type", c.typ)
		form.Set("dim.0.ref", "")

		rec := postCfg(t, "test", "/bases/test/configurator/register-fields", form, h.configuratorSaveRegisterFields)
		ok, errText := cfgResponse(t, rec)
		if ok {
			t.Errorf("%s: правка принята", c.name)
			continue
		}
		if !strings.Contains(errText, c.want) {
			t.Errorf("%s: сообщение не называет, что выбрать: %s", c.name, errText)
		}
		assertFileContains(t, p, "type: enum:РезультатСделки")
	}
}

func containsValue(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
