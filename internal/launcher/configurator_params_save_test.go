package launcher

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

// Второй и каждый следующий параметр, добавленный кнопкой «+», молча не
// сохранялся ни у отчёта, ни у обработки (issue #677).
//
// Клиент считал индекс новой строки по числу строк с текстовым полем, а шаблон
// рисует под каждым параметром ВТОРУЮ строку — блок переводов, который тоже
// состоит из текстовых полей. Индекс выходил 2×N, а сервер перебирал индексы
// подряд и обрывался на первом пропуске. Тот же обрыв уносил все параметры ниже
// удалённого.

const reportWithOneParam = `name: Продажи
title: Продажи за период
params:
  - {name: НачалоПериода, type: date, label: "С"}
query: |
  ВЫБРАТЬ 1
`

const processorWithOneParam = `name: Загрузка
title: Загрузка данных
params:
  - {name: Файл, type: string, label: "Файл"}
`

// Разреженный индекс — ровно тот, что формирует клиент при непустом блоке
// переводов: у отчёта с одним параметром новая строка получает param.2.
func TestConfiguratorSaveReport_SparseParamIndexIsSaved(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "reports", "продажи.yaml", reportWithOneParam)

	rec := postCfg(t, "test", "/bases/test/configurator/report", url.Values{
		"report_name":  {"Продажи"},
		"query":        {"ВЫБРАТЬ 1"},
		"param.0.name": {"НачалоПериода"},
		"param.0.type": {"date"},
		"param.2.name": {"КонецПериода"},
		"param.2.type": {"date"},
	}, h.configuratorSaveReport)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	assertFileContains(t, p, "НачалоПериода", "КонецПериода")
}

// Та же JS-функция обслуживает форму обработки, и серверный цикл был таким же.
func TestConfiguratorSaveProcessor_SparseParamIndexIsSaved(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "processors", "загрузка.yaml", processorWithOneParam)

	rec := postCfg(t, "test", "/bases/test/configurator/processor", url.Values{
		"processor_name": {"Загрузка"},
		"param.0.name":   {"Файл"},
		"param.0.type":   {"string"},
		"param.2.name":   {"Кодировка"},
		"param.2.type":   {"string"},
	}, h.configuratorSaveProcessor)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	assertFileContains(t, p, "Файл", "Кодировка")
}

// Удаление параметра из середины не должно уносить те, что ниже: прежний цикл
// обрывался на образовавшейся дыре.
func TestConfiguratorSaveReport_GapKeepsLaterParams(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "reports", "продажи.yaml", reportWithOneParam)

	rec := postCfg(t, "test", "/bases/test/configurator/report", url.Values{
		"report_name":  {"Продажи"},
		"query":        {"ВЫБРАТЬ 1"},
		"param.0.name": {"А"},
		"param.0.type": {"string"},
		// параметр 1 удалён пользователем
		"param.2.name": {"В"},
		"param.2.type": {"string"},
		"param.3.name": {"Г"},
		"param.3.type": {"string"},
	}, h.configuratorSaveReport)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	assertFileContains(t, p, "name: А", "name: В", "name: Г")
}

// Многоязычные подписи обязаны остаться у своего параметра.
func TestConfiguratorSaveReport_LabelsStayWithTheirParam(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "reports", "продажи.yaml", reportWithOneParam)

	rec := postCfg(t, "test", "/bases/test/configurator/report", url.Values{
		"report_name":       {"Продажи"},
		"query":             {"ВЫБРАТЬ 1"},
		"param.0.name":      {"НачалоПериода"},
		"param.0.type":      {"date"},
		"param.0.labels.en": {"From"},
		"param.2.name":      {"КонецПериода"},
		"param.2.type":      {"date"},
		"param.2.labels.en": {"To"},
	}, h.configuratorSaveReport)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	assertFileContains(t, p, "From", "To")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if got := string(raw); strings.Index(got, "From") > strings.Index(got, "КонецПериода") {
		t.Errorf("подпись первого параметра уехала ко второму:\n%s", raw)
	}
}
