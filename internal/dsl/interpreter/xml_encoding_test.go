package interpreter

// Кодировка объявления XML (#1036).
//
// Выгрузки 1С объявляют windows-1251, и ПрочитатьXML отвергала такой документ
// целиком — даже когда байты уже были перекодированы файловыми операциями
// платформы. Пользователь получал отказ на файле, который платформа прочитала
// правильно, и починить это в конфигурации было нечем.

import (
	"testing"

	"golang.org/x/text/encoding/charmap"
)

const declared1251 = `<?xml version="1.0" encoding="windows-1251"?>` +
	`<КоммерческаяИнформация><Товар>Насос «Гроза»</Товар></КоммерческаяИнформация>`

// Самый частый случай: объявление говорит 1251, а байты уже UTF-8 — так
// выглядит выгрузка, прочитанная файловыми операциями платформы (decodeText).
func TestReadXML_Declared1251ButAlreadyUTF8(t *testing.T) {
	v, err := builtinReadXML([]any{declared1251}, "", 0)
	if err != nil {
		t.Fatalf("документ с объявлением windows-1251 отвергнут: %v", err)
	}
	root, ok := v.(*Struct)
	if !ok {
		t.Fatalf("ожидалась Структура, получено %T", v)
	}
	children, ok := root.Get(xmlFieldChildren).(*Array)
	if !ok || len(children.Iterate()) != 1 {
		t.Fatal("документ разобран неверно")
	}
	if got := children.Index(0).(*Struct).Get(xmlFieldText); got != "Насос «Гроза»" {
		t.Fatalf("текст = %v — кириллица испорчена перекодировкой уже верных байтов", got)
	}
}

// Настоящие байты windows-1251: перекодируем по объявлению, иначе в данных
// окажется мусор вместо кириллицы.
func TestReadXML_RealWindows1251BytesAreDecoded(t *testing.T) {
	raw, err := charmap.Windows1251.NewEncoder().String(declared1251)
	if err != nil {
		t.Fatal(err)
	}
	if raw == declared1251 {
		t.Fatal("подготовка: текст не изменился, кодировщик не сработал")
	}
	v, err := builtinReadXML([]any{raw}, "", 0)
	if err != nil {
		t.Fatalf("документ в windows-1251 отвергнут: %v", err)
	}
	root := v.(*Struct)
	children := root.Get(xmlFieldChildren).(*Array)
	if got := children.Index(0).(*Struct).Get(xmlFieldText); got != "Насос «Гроза»" {
		t.Fatalf("текст = %v, ожидалось «Насос «Гроза»» — перекодировка не сработала", got)
	}
}

// Незнакомая кодировка по-прежнему отвергается: разобрать документ не так, как
// он записан, хуже отказа. Сообщение обязано говорить, что делать.
func TestReadXML_UnknownEncodingRejected(t *testing.T) {
	// Отказ приходит пользовательской ошибкой (паникой userError), как и
	// остальные отказы разбора, — ловим её тем же хелпером, что и соседние тесты.
	doc := `<?xml version="1.0" encoding="utf-16"?><a/>`
	requireXMLUserError(t, "utf-16", func() {
		_, _ = builtinReadXML([]any{doc}, "", 0)
	})
	requireXMLUserError(t, "UTF-8", func() {
		_, _ = builtinReadXML([]any{doc}, "", 0)
	})
}

func TestReadXML_UTF8DeclarationStillWorks(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?><a><b>текст</b></a>`
	if _, err := builtinReadXML([]any{doc}, "", 0); err != nil {
		t.Fatalf("обычный документ отвергнут: %v", err)
	}
}
