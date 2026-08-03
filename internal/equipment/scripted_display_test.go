package equipment

import (
	"bytes"
	"testing"
)

// scriptedDisplayParams — типовой CD5220-подобный протокол, заданный данными.
func scriptedDisplayParams(addr string) map[string]string {
	return map[string]string{
		"порт":           addr,
		"командаиниц":    "1B40",           // ESC @
		"командаочистки": "0C",             // CLR
		"шаблонстроки1":  "1B5141{text}0D", // ESC Q A … CR — верхняя строка
		"шаблонстроки2":  "1B5142{text}0D", // ESC Q B … CR — нижняя строка
		"ширина":         "20",
		// Кодировка не задана → по умолчанию CP866.
	}
}

func TestScriptedDisplay_ShowLines_CP866(t *testing.T) {
	addr, received := captureServer(t)

	dev, err := Open("scripted_display", scriptedDisplayParams(addr))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	disp, ok := dev.(CustomerDisplay)
	if !ok {
		t.Fatal("устройство не реализует CustomerDisplay")
	}
	if err := disp.ShowLines([]string{"Молоко", "ИТОГО 150"}); err != nil {
		t.Fatalf("ShowLines: %v", err)
	}
	disconnect(dev)

	got := <-received
	if !bytes.HasPrefix(got, []byte{0x1B, 0x40}) {
		t.Errorf("поток не начинается с КомандаИниц ESC @: % x", got[:min(4, len(got))])
	}
	if !bytes.Contains(got, []byte{0x0C}) {
		t.Error("нет КомандаОчистки 0C")
	}
	if !bytes.Contains(got, []byte{0x1B, 0x51, 0x41}) || !bytes.Contains(got, []byte{0x1B, 0x51, 0x42}) {
		t.Error("нет hex-префиксов строк (ESC Q A / ESC Q B)")
	}
	// Текст должен быть в CP866, а не UTF-8.
	if !bytes.Contains(got, encodeCP866("Молоко")) {
		t.Error("в потоке нет CP866-байтов «Молоко»")
	}
	if bytes.Contains(got, []byte("Молоко")) {
		t.Error("текст ушёл в UTF-8, а ожидался CP866")
	}
	// Дополнение до ширины: 20 байт на строку текста.
	if n := bytes.Count(got, []byte{0x20}); n < 14 {
		t.Errorf("строка не дополнена пробелами до ширины: пробелов=%d", n)
	}
}

// Явная UTF-8 кодировка отключает перекодирование (для дисплеев, понимающих UTF-8).
func TestScriptedDisplay_UTF8Override(t *testing.T) {
	addr, received := captureServer(t)
	params := scriptedDisplayParams(addr)
	params["кодировка"] = "utf8"

	dev, err := Open("scripted_display", params)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dev.(CustomerDisplay).ShowLines([]string{"Чай"}); err != nil {
		t.Fatalf("ShowLines: %v", err)
	}
	disconnect(dev)

	got := <-received
	if !bytes.Contains(got, []byte("Чай")) {
		t.Error("при Кодировка=utf8 текст должен остаться UTF-8")
	}
}

func TestScriptedDisplay_NoTemplates(t *testing.T) {
	addr, _ := captureServer(t)
	_, err := Open("scripted_display", map[string]string{"порт": addr})
	if err == nil {
		t.Error("ожидалась ошибка: не задан ни один ШаблонСтрокиN")
	}
}

// Бонус плана 32: общий CP866-энкодер применим и к зашитому принтеру escpos —
// по умолчанию UTF-8 (как было), но Кодировка=cp866 чинит кириллицу на железе.
func TestEscpos_CP866Opt(t *testing.T) {
	addr, received := captureServer(t)
	dev, err := Open("escpos_tcp", map[string]string{"порт": addr, "кодировка": "cp866"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dev.(ReceiptPrinter).PrintReceipt(Receipt{Header: []string{"Хлеб"}, Items: []ReceiptItem{{Name: "Батон", Qty: 1, Price: 30, Sum: 30}}, Total: 30}); err != nil {
		t.Fatalf("PrintReceipt: %v", err)
	}
	disconnect(dev)

	got := <-received
	if !bytes.Contains(got, encodeCP866("Хлеб")) || !bytes.Contains(got, encodeCP866("ИТОГО:")) {
		t.Error("в CP866-чеке нет ожидаемых cp866-байтов")
	}
	if bytes.Contains(got, []byte("Хлеб")) {
		t.Error("текст остался UTF-8 при Кодировка=cp866")
	}
}

func TestParseLineTemplate(t *testing.T) {
	prefix, suffix, err := parseLineTemplate("1B5141{text}0D")
	if err != nil {
		t.Fatalf("parseLineTemplate: %v", err)
	}
	if !bytes.Equal(prefix, []byte{0x1B, 0x51, 0x41}) {
		t.Errorf("префикс = % x, ожидался 1B 51 41", prefix)
	}
	if !bytes.Equal(suffix, []byte{0x0D}) {
		t.Errorf("суффикс = % x, ожидался 0D", suffix)
	}

	// Без {text} весь шаблон — префикс.
	prefix, suffix, err = parseLineTemplate("1B40")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prefix, []byte{0x1B, 0x40}) || len(suffix) != 0 {
		t.Errorf("без {text}: префикс=% x суффикс=% x", prefix, suffix)
	}
}

func TestEncodeCP866(t *testing.T) {
	// «Привет» в CP866.
	got := encodeCP866("Привет")
	want := []byte{0x8F, 0xE0, 0xA8, 0xA2, 0xA5, 0xE2}
	if !bytes.Equal(got, want) {
		t.Errorf("encodeCP866(Привет) = % x, ожидалось % x", got, want)
	}
	// ASCII проходит без изменений.
	if !bytes.Equal(encodeCP866("AB-12"), []byte("AB-12")) {
		t.Error("ASCII должен кодироваться как есть")
	}
	// Непредставимый символ → '?'.
	if got := encodeCP866("€"); !bytes.Equal(got, []byte("?")) {
		t.Errorf("символ вне CP866 = % x, ожидался '?'", got)
	}
	// deviceEncoder: utf8 — passthrough, cp866 — перекодирование.
	if !bytes.Equal(deviceEncoder("utf-8")("Я"), []byte("Я")) {
		t.Error("deviceEncoder(utf-8) должен возвращать строку как есть")
	}
	if !bytes.Equal(deviceEncoder("CP-866")("Я"), []byte{0x9F}) {
		t.Errorf("deviceEncoder(CP-866)(Я) = % x, ожидался 9F", deviceEncoder("CP-866")("Я"))
	}
}
