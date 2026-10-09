package configcheck

import (
	"path/filepath"
	"testing"
)

func TestRunFullInfoRegisterReferenceResources(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Филиал", "Реклама"} {
		mkFile(t, filepath.Join(dir, "catalogs", name+".yaml"), "name: "+name+"\nfields:\n  - name: Наименование\n    type: string\n")
	}
	mkFile(t, filepath.Join(dir, "inforegs", "номерарекламы.yaml"), `name: НомераРекламы
periodic: true
dimensions:
  - name: НомерТелефона
    type: string
  - name: Филиал
    type: reference:Филиал
resources:
  - name: Реклама
    type: reference:Реклама
`)
	mkFile(t, filepath.Join(dir, "src", "проба.os"), `Процедура Тест()
  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ Филиал, Реклама ИЗ РегистрСведений.НомераРекламы";
  Запрос.Текст = "ВЫБРАТЬ Филиал, Реклама, Реклама.Ссылка КАК РекСсылка ИЗ РегистрСведений.НомераРекламы.СрезПоследних(&НаДату)";
  Запрос.Текст = "ВЫБРАТЬ Филиал, Реклама, Реклама.Ссылка КАК РекСсылка ИЗ РегистрСведений.НомераРекламы.СрезПервых(&НаДату)";
КонецПроцедуры`)
	res := RunFull(dir)
	if !res.OK {
		t.Fatalf("valid info-register queries failed onebase check: %+v", res.Issues)
	}
}
