package interpreter_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDSL_XML_ASCIIAndUnicodeCharacters(t *testing.T) {
	const find = `Процедура Тест()
		Возврат НайтиНедопустимыеСимволыXML(Текст);
	КонецПроцедуры`
	// Exhaust the ASCII boundary after a multibyte prefix: the answer is a
	// character position, including DEL, which XML 1.0 permits.
	for b := rune(0); b < 128; b++ {
		t.Run(fmt.Sprintf("byte-%02x", b), func(t *testing.T) {
			text := "я🙂abc" + string(b) + "tail"
			want := int64(0)
			if b < 0x20 && b != 9 && b != 10 && b != 13 {
				want = 6
			}
			got, err := evalXMLWithVars(t, find, map[string]any{"Текст": text})
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	for _, tc := range []struct {
		name, text string
		position   int64
	}{
		{"long ASCII", strings.Repeat("a<&>_'\"\t\n", 1024), 0},
		{"mixed Unicode", strings.Repeat("abcЯ🙂�", 1024), 0},
		{"literal replacement", "abc\uFFFD", 0},
		{"broken UTF8", "я🙂abc\xff", 6},
		{"forbidden Unicode", "я🙂abc\uFFFF", 6},
		{"control after long prefix", strings.Repeat("a", 8192) + "я🙂\x01", 8195},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]any{"Текст": tc.text}
			got, err := evalXMLWithVars(t, find, vars)
			require.NoError(t, err)
			assert.Equal(t, tc.position, got)
			const write = `Процедура Тест()
				Узел = Новый Структура;
				Узел.Вставить("Имя", "root");
				Узел.Вставить("Текст", Текст);
				Возврат ПрочитатьXML(ЗаписатьXML(Узел)).Текст;
			КонецПроцедуры`
			got, err = evalXMLWithVars(t, write, vars)
			if tc.position != 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), fmt.Sprintf("недопустимый символ XML в позиции %d", tc.position))
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.text, got)
			}
		})
	}
}

func TestDSL_XML_ASCIIAndUnicodeNames(t *testing.T) {
	for _, tc := range []struct{ label, name, diagnostic string }{
		{"long ASCII", "_" + strings.Repeat("aZ09-._", 1024), ""},
		{"mixed Unicode", "Имя" + strings.Repeat("aЖ🙂", 1024), ""},
		{"replacement rune", "a\uFFFD", ""},
		{"qualified", "p:local", ""},
		{"first digit", "0abc", "первый символ"},
		{"next space", "abc def", "символ ' ' недопустим"},
		{"first Unicode invalid", "\u037Eabc", "первый символ"},
		{"next Unicode invalid", "abc\uFFFF", "недопустим"},
		{"broken UTF8", "abc\xff", "некорректную UTF-8"},
		{"UTF8 error precedes first character", "0abc\xff", "некорректную UTF-8"},
		{"empty prefix", ":local", "префикс"},
		{"empty local", "p:", "локальное имя"},
		{"multiple colons", "p:x:y", "более одного"},
	} {
		for _, attribute := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/attribute=%v", tc.label, attribute), func(t *testing.T) {
				src := `Процедура Тест()
					Возврат ЗаписатьXML("value", Имя);
				КонецПроцедуры`
				if attribute {
					src = `Процедура Тест()
						Атрибуты = Новый Соответствие;
						Атрибуты.Вставить(Имя, "value");
						Узел = Новый Структура;
						Узел.Вставить("Имя", "root");
						Узел.Вставить("Атрибуты", Атрибуты);
						Возврат ЗаписатьXML(Узел);
					КонецПроцедуры`
				}
				got, err := evalXMLWithVars(t, src, map[string]any{"Имя": tc.name})
				if tc.diagnostic != "" {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tc.diagnostic)
				} else {
					require.NoError(t, err)
					assert.Contains(t, got, tc.name)
				}
			})
		}
	}
}

func TestDSL_XML_ValidationCacheChecksContent(t *testing.T) {
	// Equal lengths must not make a different string inherit a successful
	// request-local validation. Both repeated and distinct values use the DSL
	// tree path; accounting for repeated occurrences is covered by the existing
	// 1 MiB / 17-reference regression and its unchanged three-second deadline.
	const src = `Процедура Тест()
		Дети = Новый Массив;
		Для Индекс = 1 По 3 Цикл
			Узел = Новый Структура;
			Узел.Вставить("Имя", "child");
			Узел.Вставить("Текст", Первый);
			Дети.Добавить(Узел);
		КонецЦикла;
		Узел = Новый Структура;
		Узел.Вставить("Имя", "child");
		Узел.Вставить("Текст", Второй);
		Дети.Добавить(Узел);
		Корень = Новый Структура;
		Корень.Вставить("Имя", "root");
		Корень.Вставить("Элементы", Дети);
		Возврат ЗаписатьXML(Корень);
	КонецПроцедуры`
	first := strings.Repeat("a", 8192)
	for _, second := range []string{first, strings.Repeat("b", len(first)), first[:len(first)-1] + "\x01"} {
		_, err := evalXMLWithVars(t, src, map[string]any{"Первый": first, "Второй": second})
		if strings.HasSuffix(second, "\x01") {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "недопустимый символ XML в позиции 8192")
		} else {
			require.NoError(t, err)
		}
	}
}
