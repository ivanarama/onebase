package interpreter

// Кодировка объявления XML (#1036).
//
// Выгрузки 1С (CommerceML, обмен с сайтом) объявляют windows-1251, и раньше
// ПрочитатьXML отвергала такой документ целиком: «кодировка XML не
// поддерживается». Причём отвергала даже тогда, когда байты давно были
// перекодированы — файловые операции платформы (decodeText в file_builtins)
// сами приводят не-UTF-8 к UTF-8 при чтении, а объявление в тексте остаётся
// прежним. Пользователь получал отказ на файле, который платформа уже прочитала
// правильно, и исправить это в конфигурации было нечем.
//
// Поэтому решают БАЙТЫ, а не объявление:
//
//   - текст уже валидный UTF-8 → объявление считается устаревшим и не мешает;
//   - текст невалиден как UTF-8, а объявление называет известную однобайтовую
//     кодировку → перекодируем по объявлению;
//   - кодировка незнакомая → отказ с внятным текстом (лучше отказ, чем тихий
//     мусор в данных).

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// xmlCharsets — однобайтовые кодировки, встречающиеся в выгрузках. UTF-16 здесь
// намеренно нет: он не однобайтовый, а его появление означает, что файл читали
// не тем способом, и молча «починить» это нельзя.
var xmlCharsets = map[string]encoding.Encoding{
	"windows-1251": charmap.Windows1251,
	"cp1251":       charmap.Windows1251,
	"windows-1252": charmap.Windows1252,
	"koi8-r":       charmap.KOI8R,
	"ibm866":       charmap.CodePage866,
	"cp866":        charmap.CodePage866,
	"iso-8859-1":   charmap.ISO8859_1,
	"iso-8859-5":   charmap.ISO8859_5,
}

// xmlDeclaredEncoding вытаскивает значение encoding из объявления документа.
// Пустая строка — объявления нет или кодировка не указана.
func xmlDeclaredEncoding(text string) string {
	const opening = "<?xml"
	if !strings.HasPrefix(text, opening) {
		return ""
	}
	end := strings.Index(text, "?>")
	if end < 0 {
		return ""
	}
	decl := text[len(opening):end]
	idx := strings.Index(decl, "encoding")
	if idx < 0 {
		return ""
	}
	rest := decl[idx+len("encoding"):]
	eq := strings.IndexByte(rest, '=')
	if eq < 0 {
		return ""
	}
	value := strings.TrimLeft(rest[eq+1:], " \t\r\n")
	if value == "" || (value[0] != '"' && value[0] != '\'') {
		return ""
	}
	closing := strings.IndexByte(value[1:], value[0])
	if closing < 0 {
		return ""
	}
	return strings.TrimSpace(value[1 : 1+closing])
}

// xmlSupportedEncoding — знаем ли мы объявленную кодировку. UTF-8 знаем всегда.
func xmlSupportedEncoding(name string) bool {
	if name == "" || strings.EqualFold(name, "utf-8") || strings.EqualFold(name, "utf8") {
		return true
	}
	_, ok := xmlCharsets[strings.ToLower(name)]
	return ok
}

// decodeXMLByDeclaration приводит документ к UTF-8 по объявленной кодировке.
//
// Если текст уже валидный UTF-8, он возвращается как есть: перекодировать его
// «по объявлению» значило бы испортить данные, которые платформа прочитала
// правильно — а это самый частый случай, потому что чтение файла приводит
// кодировку само.
func decodeXMLByDeclaration(text string) (string, error) {
	name := xmlDeclaredEncoding(text)
	if name == "" || strings.EqualFold(name, "utf-8") || strings.EqualFold(name, "utf8") {
		return text, nil
	}
	enc, known := xmlCharsets[strings.ToLower(name)]
	if !known {
		return "", fmt.Errorf("кодировка XML «%s» не поддерживается: перекодируйте документ в UTF-8", name)
	}
	if utf8.ValidString(text) {
		// Байты уже UTF-8 — объявление устарело (так выглядит выгрузка 1С,
		// прочитанная файловыми операциями платформы). Ничего не трогаем.
		return text, nil
	}
	decoded, err := enc.NewDecoder().String(text)
	if err != nil {
		return "", fmt.Errorf("не удалось перекодировать XML из «%s»: %w", name, err)
	}
	return decoded, nil
}
