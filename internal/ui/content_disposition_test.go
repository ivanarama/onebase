package ui

import (
	"strings"
	"testing"
)

// Issue #46: сырой UTF-8 в filename="..." декодируется браузером как latin-1 —
// имя файла превращается в кракозябры. RFC 6266: ASCII-фолбэк в filename= и
// полное имя в filename*=UTF-8”.
func TestContentDisposition(t *testing.T) {
	got := contentDisposition("Контрагенты.xlsx")
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("нет RFC 5987 параметра: %q", got)
	}
	if strings.Contains(got, `filename="Контрагенты`) {
		t.Fatalf("сырой UTF-8 в quoted-string остался: %q", got)
	}
	if !strings.HasPrefix(got, "attachment; ") {
		t.Fatalf("ожидался attachment: %q", got)
	}
	// ASCII-имя остаётся читаемым в обоих параметрах.
	got = contentDisposition("report.pdf")
	if !strings.Contains(got, `filename="report.pdf"`) {
		t.Fatalf("ASCII-фолбэк потерян: %q", got)
	}
}

// Issue #46 регрессия: строгое RFC 5987-кодирование — «=», «@» и «:» запрещены
// в attr-char и должны percent-кодироваться.
func TestEncodeRFC5987_StrictEncoding(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		illegal string // подстрока, которой НЕ должно быть в результате
		want    string // подстрока, которая ДОЛЖНА присутствовать
	}{
		{
			name:    "знак равно кодируется",
			input:   "a=b.xlsx",
			illegal: "=",
			want:    "%3D",
		},
		{
			name:    "знак @ кодируется",
			input:   "прайс@2026.xlsx",
			illegal: "@",
			want:    "%40",
		},
		{
			name:    "кириллица кодируется percent-октетами",
			input:   "прайс.xlsx",
			illegal: "п", // сырая кириллица недопустима в attr-value
			want:    "%D0%BF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disp := contentDisposition(tt.input)
			// Берём только часть после UTF-8''
			idx := strings.Index(disp, "UTF-8''")
			if idx < 0 {
				t.Fatalf("нет RFC 5987 параметра: %q", disp)
			}
			encoded := disp[idx+len("UTF-8''"):]
			if strings.Contains(encoded, tt.illegal) {
				t.Errorf("незакодированный символ %q присутствует в %q", tt.illegal, encoded)
			}
			if !strings.Contains(encoded, tt.want) {
				t.Errorf("ожидаемый код %q отсутствует в %q", tt.want, encoded)
			}
		})
	}
}
