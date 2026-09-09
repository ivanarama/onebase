package launcher

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Редактор реквизитов пока не показывает multiline, поэтому сохранение
// посторонней правки обязано перенести ключ из прежнего YAML. Тест отправляет
// реально отрисованную форму в HTTP-обработчик конфигуратора: скрыто
// подставить отсутствующий в интерфейсе ключ здесь невозможно.
func TestSaveFields_KeepsMultiline(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "направление.yaml", `name: Направление
fields:
  - id: f_aaa
    name: Инструкция
    type: string
    multiline: true
  - id: f_bbb
    name: Код
    type: string
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Направление")
	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	got := readCfg(t, p)
	if strings.Count(got, "multiline: true") != 1 {
		t.Fatalf("multiline потерян или перенесён соседнему реквизиту:\n%s", got)
	}
}

// Тот же saveField обслуживает измерения и ресурсы регистра сведений — двух
// разрешённых контекстов, где форму записи действительно можно отрисовать.
func TestSaveInfoRegisterFields_KeepsMultiline(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "inforegs", "правила.yaml", `name: Правила
dimensions:
  - id: f_aaa
    name: Условия
    type: string
    multiline: true
resources:
  - id: f_bbb
    name: Описание
    type: string
    multiline: true
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmit(t, renderCfgTree(t, data), "/configurator/inforeg-fields")
	rec := postCfg(t, "test", "/bases/test/configurator/inforeg-fields", form, h.configuratorSaveInfoRegFields)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}

	got := readCfg(t, p)
	if strings.Count(got, "multiline: true") != 2 {
		t.Fatalf("multiline потерян у части полей регистра:\n%s", got)
	}
}
