package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Признак ПДн (`pii: true`) обязан пережить сохранение объекта из
// конфигуратора. Редактор реквизитов его не показывает, поэтому обратно он не
// приходит, и без переноса из прежнего состояния файла round-trip
// (Unmarshal → правка → Marshal) стирал бы ключ.
//
// Для fail-closed признака это была бы худшая из потерь: файл остаётся
// корректным, `onebase check` молчит, ошибок нет — просто с этого момента
// защищённый реквизит открыт всем ролям, которые про него не высказались.
// То есть раскрытие ПДн обычным действием в самом продукте и без единого следа.
//
// Тест идёт через saveEntityFieldsToFile — ту же функцию, которой заканчивается
// POST редактора реквизитов (configurator_entity.go), а не через ensureFieldIDs
// напрямую: проверять надо путь пользователя, включая marshal обратно в YAML.
func TestSaveEntityFieldsToFile_KeepsPII(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - id: f_aaa
    name: Наименование
    type: string
  - id: f_bbb
    name: Телефон
    type: string
    pii: true
`)

	// Редактор присылает поля без ключей, которых не знает.
	fields := []saveField{
		{Name: "Наименование", Type: "string"},
		{Name: "Телефон", Type: "string"},
	}
	if err := saveEntityFieldsToFile(dir, "Клиент", fields, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("сохранение: %v", err)
	}

	got := readCfg(t, filepath.Join(dir, "catalogs", "клиент.yaml"))
	if !strings.Contains(got, "pii: true") {
		t.Fatalf("после сохранения из конфигуратора признак pii потерян — реквизит молча открылся:\n%s", got)
	}
	// Ключ не должен расползаться на соседние реквизиты: omitempty + перенос по
	// имени, а не «поставить всем».
	if strings.Count(got, "pii: true") != 1 {
		t.Fatalf("pii проставлен не одному реквизиту:\n%s", got)
	}
}

// Тот же saveField обслуживает поля регистров, и перенос обязан работать и там:
// измерение регистра накопления — место, где признак ПДн исполняется.
func TestSaveRegisterFieldsToFile_KeepsPII(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, filepath.Join(dir, "registers", "звонки.yaml"), `name: Звонки
dimensions:
  - id: f_aaa
    name: Номер
    type: string
    pii: true
resources:
  - id: f_bbb
    name: Длительность
    type: number
`)

	dims := []saveField{{Name: "Номер", Type: "string"}}
	res := []saveField{{Name: "Длительность", Type: "number"}}
	if err := saveRegisterFieldsToFile(dir, "Звонки", dims, res, nil, nil); err != nil {
		t.Fatalf("сохранение: %v", err)
	}

	got := readCfg(t, filepath.Join(dir, "registers", "звонки.yaml"))
	if !strings.Contains(got, "pii: true") {
		t.Fatalf("после сохранения регистра из конфигуратора признак pii потерян:\n%s", got)
	}
}

// Реквизит без признака его не приобретает: перенос односторонний, но не
// «раздать всем, у кого сосед защищён».
func TestSaveEntityFieldsToFile_NoPIIStaysClean(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, filepath.Join(dir, "catalogs", "склад.yaml"), `name: Склад
fields:
  - id: f_aaa
    name: Наименование
    type: string
`)

	fields := []saveField{{Name: "Наименование", Type: "string"}}
	if err := saveEntityFieldsToFile(dir, "Склад", fields, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("сохранение: %v", err)
	}

	got := readCfg(t, filepath.Join(dir, "catalogs", "склад.yaml"))
	if strings.Contains(got, "pii") {
		t.Fatalf("в объекте без ПДн появился ключ pii:\n%s", got)
	}
}

func writeCfg(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCfg(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
