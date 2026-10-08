package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// После Записать() собственная ссылка объекта лежала в нём без представления,
// и Строка(Об.Ссылка) давала пустую строку. Так текст «Склад «» не найден»
// уходил в сообщения и журналы, хотя у записанного объекта наименование или
// номер есть. У объекта, загруженного из базы, представление было и раньше.
//
// Проверка — обработкой тем же путём, что procrun.
func TestDSLSelfReferencePresentationAfterWrite(t *testing.T) {
	dir := t.TempDir()
	mk := func(sub string) string {
		p := filepath.Join(dir, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		return p
	}
	files := map[string]string{
		filepath.Join(mk("catalogs"), "склад.yaml"): "name: Склад\nfields:\n  - {name: Наименование, type: string}\n",
		filepath.Join(mk("documents"), "приход.yaml"): "name: Приход\nnumerator: {prefix: \"П-\", length: 3}\n" +
			"fields:\n  - {name: Номер, type: string}\n  - {name: Дата, type: date}\n",
		filepath.Join(mk("processors"), "проба.yaml"): "name: Проба\n",
		filepath.Join(mk("src"), "проба.proc.os"): `Процедура Выполнить()
  Склад = Справочники.Склад.Создать();
  Склад.Наименование = "Основной";
  Склад.Записать();
  Сообщить("справочник: " + Строка(Склад.Ссылка));

  Документ = Документы.Приход.Создать();
  Документ.Дата = Дата(2026, 9, 1);
  Документ.Записать();
  Сообщить("документ: " + Строка(Документ.Ссылка));
КонецПроцедуры
`,
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"справочник: Основной", "документ: П-001"}
	if got := runSelfRefProc(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("сообщения:\n got %q\nwant %q", got, want)
	}
}
