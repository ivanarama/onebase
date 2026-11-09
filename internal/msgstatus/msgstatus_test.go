package msgstatus

import "testing"

func TestEncodeSplitRoundTrip(t *testing.T) {
	for _, status := range []string{Information, Attention, Important, VeryImportant} {
		text, got := Split(Encode("текст", status))
		if text != "текст" || got != status {
			t.Errorf("%s: получили %q %q", status, text, got)
		}
		if wantErr := status == Important || status == VeryImportant; IsError(status) != wantErr {
			t.Errorf("%s: IsError=%v, ждали %v", status, IsError(status), wantErr)
		}
	}
	if Encode("текст", Ordinary) != "текст" || Encode("текст", "неизвестный") != "текст" {
		t.Error("обычный и неизвестный статус не должны менять текст")
	}
	// Повторная метка не накапливается: статус — последний.
	if text, status := Split(Encode(Encode("т", Important), Information)); text != "т" || status != Information {
		t.Errorf("перекодирование: %q %q", text, status)
	}
	// Строка, случайно начинающаяся с U+2063 без завершителя, — обычный текст.
	if text, status := Split("⁣⁣x"); text != "⁣⁣x" || status != Ordinary {
		t.Errorf("без завершителя: %q %q", text, status)
	}
	if text, status := Split("⁣⁣⁣⁣⁣⁤x"); status != Ordinary || text == "x" {
		t.Errorf("номер вне диапазона принят: %q %q", text, status)
	}
}

func TestFromArgs(t *testing.T) {
	if _, ok := FromArgs(nil); ok {
		t.Error("без аргументов сообщать нечего")
	}
	cases := []struct {
		args []any
		want string
	}{
		{[]any{"т"}, Ordinary},
		{[]any{"т", "Важное"}, Important},
		{[]any{"т", "оченьважное"}, VeryImportant},
		{[]any{"т", "Внимание"}, Attention},
		{[]any{"т", "Информация"}, Information},
		{[]any{"т", "БезСтатуса"}, Ordinary},
		{[]any{"т", nil}, Ordinary},
		{[]any{"т", float64(3)}, Ordinary},
	}
	for _, c := range cases {
		msg, ok := FromArgs(c.args)
		text, status := Split(msg)
		if !ok || text != "т" || status != c.want {
			t.Errorf("%v: %q %q, ждали %q", c.args, text, status, c.want)
		}
	}
}

func TestStripAll(t *testing.T) {
	plain := []string{"a", "b"}
	if got := StripAll(plain); &got[0] != &plain[0] {
		t.Error("срез без меток должен возвращаться без копии")
	}
	marked := []string{"a", Encode("b", Important)}
	got := StripAll(marked)
	if got[0] != "a" || got[1] != "b" || marked[1] == "b" {
		t.Errorf("StripAll: %q, исходный %q", got, marked)
	}
}
