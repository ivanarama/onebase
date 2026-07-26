package onec_forms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bslSample = `
&НаСервере
Перем Глоб1, Глоб2;

// Комментарий 1
// Комментарий 2
&НаКлиенте
Процедура ПолучитьДанные(Команда)
	Если ТЗ.Количество() > 0 Тогда
		Сообщить("Данные есть");
	КонецЕсли;
КонецПроцедуры

&НаСервере
Процедура ПолучитьДанные_сервер()
	Запрос = Новый Запрос;
	Запрос.Текст = "ВЫБРАТЬ * ИЗ Справочник.Контрагенты";
	Рез = Запрос.Выполнить();
КонецПроцедуры

&НаКлиентеНаСервереБезКонтекста
Функция ПриВыборе(Знач Х, Знач Y) Экспорт
	Возврат Х + Y;
КонецФункции
`

func TestSplitProcedures(t *testing.T) {
	lines := strings.Split(strings.TrimPrefix(bslSample, "\n"), "\n")
	procs := splitProcedures(lines)
	if len(procs) != 3 {
		t.Fatalf("procs count = %d, want 3", len(procs))
	}

	p0 := procs[0]
	if p0.Name != "ПолучитьДанные" {
		t.Errorf("p0.Name = %q", p0.Name)
	}
	if p0.Directive != "&НаКлиенте" {
		t.Errorf("p0.Directive = %q", p0.Directive)
	}
	if len(p0.Comments) != 2 {
		t.Errorf("p0.Comments = %v", p0.Comments)
	}
	if !strings.Contains(p0.Body, "Сообщить") {
		t.Errorf("p0.Body не содержит Сообщить: %q", p0.Body)
	}
	if p0.IsExport {
		t.Error("p0 не должна быть экспортной")
	}
	if len(p0.Params) != 1 || p0.Params[0] != "Команда" {
		t.Errorf("p0.Params = %v", p0.Params)
	}

	p1 := procs[1]
	if p1.Directive != "&НаСервере" {
		t.Errorf("p1.Directive = %q", p1.Directive)
	}
	if !strings.Contains(p1.Body, "Новый Запрос") {
		t.Error("p1.Body не содержит 'Новый Запрос'")
	}
	if len(p1.Params) != 0 {
		t.Errorf("p1.Params = %v (ожидался пустой)", p1.Params)
	}

	p2 := procs[2]
	if !p2.IsFunc {
		t.Error("p2 должен быть IsFunc")
	}
	if !p2.IsExport {
		t.Error("p2 должен быть IsExport")
	}
	if p2.Directive != "&НаКлиентеНаСервереБезКонтекста" {
		t.Errorf("p2.Directive = %q", p2.Directive)
	}
	if len(p2.Params) != 2 || p2.Params[0] != "Знач Х" || p2.Params[1] != "Знач Y" {
		t.Errorf("p2.Params = %v", p2.Params)
	}
}

func TestScanCompatibilityWarnings(t *testing.T) {
	lines := strings.Split(strings.TrimPrefix(bslSample, "\n"), "\n")
	procs := splitProcedures(lines)
	warns := ScanCompatibilityWarnings(procs)

	// Должны быть как минимум: Сообщить( + Новый Запрос
	var seenSoobshit, seenZapros bool
	for _, w := range warns {
		if w.Code != W040_BSLNotInDSL {
			t.Errorf("неверный код: %+v", w)
			continue
		}
		if w.Field == "Сообщить(" {
			seenSoobshit = true
		}
		if w.Field == "Новый Запрос" {
			seenZapros = true
		}
	}
	if !seenSoobshit {
		t.Error("warning о Сообщить( не сгенерирован")
	}
	if !seenZapros {
		t.Error("warning о Новый Запрос не сгенерирован")
	}
}

func TestEmitDSLSource(t *testing.T) {
	lines := strings.Split(strings.TrimPrefix(bslSample, "\n"), "\n")
	procs := splitProcedures(lines)
	dsl := EmitDSLSource(procs)

	// Должны быть аннотации директив
	if !strings.Contains(dsl, "// @directive=&НаКлиенте") {
		t.Error("отсутствует аннотация @directive=&НаКлиенте")
	}
	if !strings.Contains(dsl, "// @directive=&НаСервере") {
		t.Error("отсутствует аннотация @directive=&НаСервере")
	}
	// Экспортная функция
	if !strings.Contains(dsl, "Функция ПриВыборе(Знач Х, Знач Y) Экспорт") {
		t.Error("ПриВыборе не отрендерилась с Экспорт")
	}
	if !strings.Contains(dsl, "КонецФункции") {
		t.Error("КонецФункции не сгенерирован")
	}
	if !strings.Contains(dsl, "КонецПроцедуры") {
		t.Error("КонецПроцедуры не сгенерирован")
	}
}

func TestReadBSL_RealFile(t *testing.T) {
	realPath := `C:\Projects\АА5БП3\УТ11УТ11\ПереносДанныхУТ11УТ11_52\Forms\Форма\Ext\Form\Module.bsl`
	if _, err := os.Stat(realPath); err != nil {
		t.Skip("real УТ11 Module.bsl не найден")
	}
	procs, warns, err := ReadBSL(realPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("procedures: %d, warnings: %d", len(procs), len(warns))
	if len(procs) < 5 {
		t.Errorf("слишком мало процедур (%d), реальная форма должна иметь много", len(procs))
	}

	// проверим что нашлась ПолучитьДанные с директивой &НаКлиенте
	var pd *BSLProcedure
	for _, p := range procs {
		if p.Name == "ПолучитьДанные" {
			pd = p
			break
		}
	}
	if pd == nil {
		t.Skip("ПолучитьДанные не найдена — у этой версии формы может быть другая структура")
		return
	}
	if pd.Directive != "&НаКлиенте" {
		t.Errorf("ПолучитьДанные.Directive = %q, ожидается &НаКлиенте", pd.Directive)
	}
}

func TestReadBSL_NoFile(t *testing.T) {
	procs, warns, err := ReadBSL(filepath.Join(t.TempDir(), "nope.bsl"))
	if err != nil {
		t.Errorf("ReadBSL отсутствующего файла должен вернуть nil-error: %v", err)
	}
	if procs != nil || warns != nil {
		t.Errorf("procs/warns должны быть nil: %v / %v", procs, warns)
	}
}
