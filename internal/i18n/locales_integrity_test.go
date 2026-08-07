package i18n

import (
	"encoding/json"
	"io/fs"
	"testing"
	"unicode"
)

// Ключи словарей — это исходные (русские) строки шаблонов: они состоят из
// кириллицы, латиницы (OK, ID, URL и т. п.), цифр, пробелов и пунктуации. Буква
// из другого письма в ключе — почти наверняка гомоглиф: перевод по такому ключу
// недостижим, а глазами подмена не видна (грузинская «დ» вместо кириллической
// «д» в ka.json, #614). i18ncheck этого не ловит — он смотрит покрытие шаблонов,
// а не целостность самих ключей.
func TestLocaleKeysUseSourceAlphabetOnly(t *testing.T) {
	files, err := fs.Glob(EmbeddedLocales, "locales/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("встроенные локали не найдены")
	}
	for _, f := range files {
		data, err := fs.ReadFile(EmbeddedLocales, f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for key := range m {
			for _, r := range key {
				if !unicode.IsLetter(r) {
					continue
				}
				if unicode.Is(unicode.Cyrillic, r) || unicode.Is(unicode.Latin, r) {
					continue
				}
				t.Errorf("%s: ключ %q содержит букву %q (U+%04X) не из кириллицы/латиницы — вероятно гомоглиф, перевод по такому ключу недостижим",
					f, key, r, r)
			}
		}
	}
}
