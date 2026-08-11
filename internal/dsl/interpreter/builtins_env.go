package interpreter

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// tempFileSeq различает имена, выданные в пределах одного тика системных часов.
// На Windows разрешение таймера — миллисекунды, поэтому два вызова подряд
// давали одинаковое имя, и второй временный файл затирал первый.
var tempFileSeq atomic.Uint64

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
	return dslTempDir(), nil
}

func dslTempDir() string {
	if fileSandboxRoot != "" {
		dir := filepath.Join(fileSandboxRoot, "tmp")
		_ = os.MkdirAll(dir, 0o755)
		return dir
	}
	return os.TempDir()
}

// ПолучитьИмяВременногоФайла([Расширение]) — уникальное имя во временном
// каталоге. Файл НЕ создаётся: возвращается только путь, как в 1С.
//
// Расширение принимается и с точкой, и без неё («txt» и «.txt» равнозначны) —
// в переносимом коде встречаются оба написания.
func tempFileNameFn(args []any, _ string, _ int) (any, error) {
	ext := ""
	if len(args) > 0 && args[0] != nil {
		ext = strings.TrimSpace(strArg(args, 0))
		if ext != "" && !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
	}
	seq := tempFileSeq.Add(1)
	name := "onebase-" + strconv.FormatInt(time.Now().UnixNano(), 36) +
		"-" + strconv.FormatUint(seq, 36) + ext
	return filepath.Join(dslTempDir(), name), nil
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
	keep := unreservedURLValue
	if pathMode(args) {
		keep = unreservedURLWhole
	}
	return percentEncode(strArg(args, 0), keep), nil
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
	src := strArg(args, 0)
	if out, err := url.PathUnescape(src); err == nil {
		return out, nil
	}
	return src, nil
}

// pathMode распознаёт режим «сохранить структуру URL» по второму аргументу.
func pathMode(args []any) bool {
	if len(args) < 2 || args[1] == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(strArg(args, 1)))
	switch mode {
	case "urlвкодировкеurl", "urlinurlencoding", "path", "url":
		return true
	default:
		return false
	}
}
