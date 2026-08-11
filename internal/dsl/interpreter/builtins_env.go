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
	builtins["каталогвременныхфайлов"] = tempDirFn
	builtins["tempfilesdir"] = tempDirFn
	builtins["получитьимявременногофайла"] = tempFileNameFn
	builtins["gettempfilename"] = tempFileNameFn
	builtins["получитьразделительпути"] = pathSeparatorFn
	builtins["getpathseparator"] = pathSeparatorFn
	builtins["кодироватьстроку"] = encodeStringFn
	builtins["encodestring"] = encodeStringFn
	builtins["раскодироватьстроку"] = decodeStringFn
	builtins["decodestring"] = decodeStringFn
}

// КаталогВременныхФайлов() — каталог для временных файлов.
//
// При включённой файловой песочнице (демо-режим) возвращается подкаталог
// внутри её корня: иначе функция выдавала бы путь, которым файловые builtins
// всё равно не дадут воспользоваться.
func tempDirFn(args []any, _ string, _ int) (any, error) {
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
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("защитить каталог временных файлов: %w", err)
		}
		return dir, nil
	}
	return os.TempDir(), nil
}

// ПолучитьИмяВременногоФайла([Расширение]) — уникальное имя во временном
// каталоге. Файл НЕ создаётся: возвращается только путь, как в 1С.
//
// Расширение принимается и с точкой, и без неё («txt» и «.txt» равнозначны) —
// в переносимом коде встречаются оба написания.
func tempFileNameFn(args []any, _ string, _ int) (any, error) {
	ext := ""
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

// КодироватьСтроку(Строка[, Способ]) — процентное кодирование для URL.
//
// Способ: «КодировкаURL» (по умолчанию) кодирует значение параметра целиком;
// «URLВКодировкеURL» сохраняет структуру адреса и кодирует только небезопасные
// символы. Имена способов совпадают с 1С, чтобы перенесённый код читался без
// правок; принимаются и короткие url/path.
func encodeStringFn(args []any, _ string, _ int) (any, error) {
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

// РаскодироватьСтроку(Строка[, Способ]) — обратное преобразование.
//
// ⚠️ «+» остаётся плюсом, а не превращается в пробел: проверено на 1С —
// РаскодироватьСтроку("a+b") возвращает «a+b». Именно поэтому здесь
// url.PathUnescape, а не QueryUnescape.
//
// Неразбираемую последовательность (например «%zz») возвращаем как есть —
// платформа ведёт себя так же, и обмен данными не должен падать из-за одного
// кривого значения в параметре.
func decodeStringFn(args []any, _ string, _ int) (any, error) {
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
	if len(args) < 2 || args[1] == nil {
		return false, nil
	}
	mode := strings.ToLower(strings.TrimSpace(strArg(args, 1)))
	switch mode {
	case "", "кодировкаurl", "urlencoding", "component", "value", "query":
		return false, nil
	case "urlвкодировкеurl", "urlinurlencoding", "path", "url":
		return true, nil
	default:
		return false, fmt.Errorf("неизвестный способ URL-кодирования %q", strArg(args, 1))
	}
}
