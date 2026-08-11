package interpreter

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Функции окружения: временные файлы, разделитель пути, URL-кодирование.
// Файловые операции уже есть (builtins_files.go), но им негде взять временный
// путь — конфигурации приходилось придумывать его самой, зашивая каталог
// в код. URL-кодирование нужно всем, кто собирает адрес с параметрами:
// без него параметр с пробелом или кириллицей ломает запрос.
func init() {
	builtins["каталогвременныхфайлов"] = catchableEnvBuiltin(tempDirFn)
	builtins["tempfilesdir"] = catchableEnvBuiltin(tempDirFn)
	builtins["получитьимявременногофайла"] = catchableEnvBuiltin(tempFileNameFn)
	builtins["gettempfilename"] = catchableEnvBuiltin(tempFileNameFn)
	builtins["получитьразделительпути"] = catchableEnvBuiltin(pathSeparatorFn)
	builtins["getpathseparator"] = catchableEnvBuiltin(pathSeparatorFn)
	builtins["кодироватьстроку"] = catchableEnvBuiltin(encodeStringFn)
	builtins["encodestring"] = catchableEnvBuiltin(encodeStringFn)
	builtins["раскодироватьстроку"] = catchableEnvBuiltin(decodeStringFn)
	builtins["decodestring"] = catchableEnvBuiltin(decodeStringFn)
}

// catchableEnvBuiltin переводит штатные ошибки функций окружения в
// пользовательские исключения DSL. Обычный error из BuiltinFunc означает для
// интерпретатора системную остановку (dslStop) и намеренно обходит Попытку;
// ошибки аргументов и ОС у этих функций, напротив, должны быть перехватываемы.
// Чистые функции ниже продолжают возвращать error, поэтому их можно тестировать
// и переиспользовать без recover.
func catchableEnvBuiltin(fn BuiltinFunc) BuiltinFunc {
	return func(args []any, file string, line int) (any, error) {
		result, err := fn(args, file, line)
		if err != nil {
			panic(userError{Msg: err.Error(), File: file, Line: line, Err: err})
		}
		return result, nil
	}
}

// installEnvironmentConstants публикует совместимые с 1С значения способов
// кодирования. Объекты создаются для каждого запуска отдельно: DSL-код не
// сможет изменить перечисление для другого параллельного запуска.
func installEnvironmentConstants(e *env) {
	stringEncodingMethod := &MapThis{M: map[string]any{
		"КодировкаURL":     "КодировкаURL",
		"URLВКодировкеURL": "URLВКодировкеURL",
		"URLEncoding":      "URLEncoding",
		"URLInURLCoding":   "URLInURLCoding",
		"URLInURLEncoding": "URLInURLEncoding",
	}}
	textEncoding := &MapThis{M: map[string]any{
		"UTF8": "UTF-8",
	}}

	e.setLocal("СпособКодированияСтроки", stringEncodingMethod)
	e.setLocal("StringEncodingMethod", stringEncodingMethod)
	e.setLocal("КодировкаТекста", textEncoding)
	e.setLocal("TextEncoding", textEncoding)

	// Короткие имена встречаются в перенесённых конфигурациях и остаются
	// однозначными в поддерживаемом подмножестве API.
	e.setLocal("КодировкаURL", "КодировкаURL")
	e.setLocal("URLВКодировкеURL", "URLВКодировкеURL")
	e.setLocal("URLEncoding", "URLEncoding")
	e.setLocal("URLInURLCoding", "URLInURLCoding")
	e.setLocal("URLInURLEncoding", "URLInURLEncoding")
	e.setLocal("UTF8", "UTF-8")
}

// КаталогВременныхФайлов() — каталог для временных файлов.
//
// При включённой файловой песочнице (демо-режим) возвращается подкаталог
// внутри её корня: иначе функция выдавала бы путь, которым файловые builtins
// всё равно не дадут воспользоваться.
func tempDirFn(args []any, _ string, _ int) (any, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("КаталогВременныхФайлов: ожидается 0 аргументов, получено %d", len(args))
	}
	return dslTempDir()
}

func dslTempDir() (string, error) {
	if fileSandboxRoot != "" {
		dir := filepath.Join(fileSandboxRoot, "tmp")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("создать каталог временных файлов: %w", err)
		}
		// MkdirAll не меняет права уже существующего каталога. Подтягиваем его
		// к приватному режиму и не выдаём путь, если это не удалось.
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: directory needs execute bit; 0700 is least privilege
			return "", fmt.Errorf("защитить каталог временных файлов: %w", err)
		}
		return withTrailingPathSeparator(dir), nil
	}
	return withTrailingPathSeparator(os.TempDir()), nil
}

func withTrailingPathSeparator(dir string) string {
	dir = filepath.Clean(dir)
	separator := string(filepath.Separator)
	if strings.HasSuffix(dir, separator) {
		return dir
	}
	return dir + separator
}

// ПолучитьИмяВременногоФайла([Расширение]) — уникальное имя во временном
// каталоге. Файл НЕ создаётся: возвращается только путь, как в 1С.
//
// Расширение принимается и с точкой, и без неё («txt» и «.txt» равнозначны) —
// в переносимом коде встречаются оба написания.
func tempFileNameFn(args []any, _ string, _ int) (any, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("ПолучитьИмяВременногоФайла: ожидается не более 1 аргумента, получено %d", len(args))
	}
	ext := ".tmp"
	if len(args) > 0 && args[0] != nil {
		var err error
		ext, err = normalizeTempExtension(strArg(args, 0))
		if err != nil {
			return nil, err
		}
	}

	dir, err := dslTempDir()
	if err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("создать имя временного файла: %w", err)
	}
	name := "onebase-" + hex.EncodeToString(random[:]) + ext
	path := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("расширение временного файла выводит путь за пределы каталога")
	}
	return path, nil
}

// normalizeTempExtension принимает именно суффикс имени, а не фрагмент пути.
// Это важно и на Windows, и на Unix: filepath.Join не должен получить через
// «расширение» разделитель, имя тома или alternate data stream.
func normalizeTempExtension(src string) (string, error) {
	ext := strings.TrimSpace(src)
	if ext == "" {
		return "", nil
	}
	if !utf8.ValidString(ext) {
		return "", fmt.Errorf("расширение временного файла должно быть корректным UTF-8")
	}
	ext = strings.TrimPrefix(ext, ".")
	if ext == "" || len(ext) > 64 || strings.HasPrefix(ext, ".") || strings.Contains(ext, "..") {
		return "", fmt.Errorf("недопустимое расширение временного файла")
	}
	for _, r := range ext {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			continue
		}
		return "", fmt.Errorf("недопустимый символ %q в расширении временного файла", r)
	}
	return "." + ext, nil
}

// ПолучитьРазделительПути() — разделитель каталогов текущей ОС.
func pathSeparatorFn(args []any, _ string, _ int) (any, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("ПолучитьРазделительПути: ожидается 0 аргументов, получено %d", len(args))
	}
	return string(filepath.Separator), nil
}

// Наборы символов, которые 1С оставляет незакодированными. Сверено прогоном
// на живой платформе 8.3, а не по документации:
//
//	КодироватьСтроку("-_.~!*()'+/:@?#[]$,;= ", КодировкаURL)
//	  → -_.~%21%2A%28%29%27%2B%2F%3A%40%3F%23%5B%5D%24%2C%3B%3D%20
//	КодироватьСтроку(то же, URLВКодировкеURL)
//	  → -_.~!*()'+/:@?#[]$,;=%20
//
// Отсюда два вывода, из-за которых нельзя взять url.QueryEscape:
// пробел кодируется как %20 (а не «+»), и в режиме URL сохраняются
// зарезервированные символы адреса.
const (
	unreservedURLValue = "-_.~"
	unreservedURLWhole = "-_.~!*()'+/:@?#[]$,;="
)

// КодироватьСтроку(Строка[, Способ[, Кодировка]]) — процентное кодирование для URL.
//
// Способ: «КодировкаURL» (по умолчанию) кодирует значение параметра целиком;
// «URLВКодировкеURL» сохраняет структуру адреса и кодирует только небезопасные
// символы. Имена способов совпадают с 1С, чтобы перенесённый код читался без
// правок; принимаются и короткие url/path.
func encodeStringFn(args []any, _ string, _ int) (any, error) {
	if err := validateStringCodecArgs("КодироватьСтроку", args); err != nil {
		return nil, err
	}
	wholeURL, err := wholeURLMode(args)
	if err != nil {
		return nil, err
	}
	src := strArg(args, 0)
	if !utf8.ValidString(src) {
		return nil, fmt.Errorf("кодируемая строка должна быть корректным UTF-8")
	}
	keep := unreservedURLValue
	if wholeURL {
		keep = unreservedURLWhole
	}
	return percentEncode(src, keep), nil
}

// percentEncode кодирует всё, кроме букв, цифр и символов из keep.
// Работает по байтам UTF-8 — так же, как это делает платформа.
func percentEncode(src, keep string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(src); i++ {
		c := src[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0F])
	}
	return b.String()
}

// РаскодироватьСтроку(Строка[, Способ[, Кодировка]]) — обратное преобразование.
//
// ⚠️ «+» остаётся плюсом, а не превращается в пробел: проверено на 1С —
// РаскодироватьСтроку("a+b") возвращает «a+b». Именно поэтому здесь
// url.PathUnescape, а не QueryUnescape.
//
// Неразбираемую последовательность (например «%zz») возвращаем как есть —
// платформа ведёт себя так же, и обмен данными не должен падать из-за одного
// кривого значения в параметре.
func decodeStringFn(args []any, _ string, _ int) (any, error) {
	if err := validateStringCodecArgs("РаскодироватьСтроку", args); err != nil {
		return nil, err
	}
	if _, err := wholeURLMode(args); err != nil {
		return nil, err
	}
	src := strArg(args, 0)
	if !utf8.ValidString(src) {
		return nil, fmt.Errorf("раскодируемая строка должна быть корректным UTF-8")
	}
	if out, err := url.PathUnescape(src); err == nil && utf8.ValidString(out) {
		return out, nil
	}
	return src, nil
}

// wholeURLMode распознаёт режим кодирования и отклоняет опечатки. Молчаливый
// fallback к режиму значения меняет данные: например, внезапно кодирует `/`.
func wholeURLMode(args []any) (bool, error) {
	if len(args) < 2 {
		return false, nil
	}
	if args[1] == nil {
		return false, fmt.Errorf("способ URL-кодирования не задан")
	}
	mode := strings.ToLower(strings.TrimSpace(strArg(args, 1)))
	switch mode {
	case "кодировкаurl", "urlencoding", "component", "value", "query":
		return false, nil
	case "urlвкодировкеurl", "urlinurlcoding", "urlinurlencoding", "path", "url":
		return true, nil
	default:
		return false, fmt.Errorf("неизвестный способ URL-кодирования %q", strArg(args, 1))
	}
}

func validateStringCodecArgs(name string, args []any) error {
	if len(args) == 0 || args[0] == nil {
		return fmt.Errorf("%s: обязательный первый аргумент не задан", name)
	}
	if len(args) > 3 {
		return fmt.Errorf("%s: ожидается не более 3 аргументов, получено %d", name, len(args))
	}
	if len(args) < 3 {
		return nil
	}
	if args[2] == nil {
		return fmt.Errorf("%s: кодировка не задана", name)
	}
	encoding := strings.ToLower(strings.TrimSpace(strArg(args, 2)))
	encoding = strings.NewReplacer("-", "", "_", "", " ", "").Replace(encoding)
	switch encoding {
	case "utf8", "unicodeutf8", "кодировкаutf8":
		return nil
	default:
		return fmt.Errorf("%s: неподдерживаемая кодировка %q; поддерживается UTF-8", name, strArg(args, 2))
	}
}
