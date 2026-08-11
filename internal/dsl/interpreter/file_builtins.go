package interpreter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"golang.org/x/text/encoding/charmap"
)

// decodeText tries UTF-8 first; if not valid, decodes as Windows-1251.
// utf8.Valid — строгая проверка байтов (как в decodeUploadText), в отличие от
// эвристики «есть руна U+FFFD»: та давала ложные срабатывания на валидном
// UTF-8, легитимно содержащем U+FFFD, и портила такой текст перекодировкой.
func decodeText(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	decoded, err := charmap.Windows1251.NewDecoder().Bytes(data)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

// ─── dslTextReader (ЧтениеТекста) ──────────────────────────────────────────

type dslTextReader struct {
	path    string
	content string
	lines   []string
	lineIdx int
	isOpen  bool
}

func (r *dslTextReader) Get(field string) any {
	switch field {
	case "открыта", "isopen":
		return r.isOpen
	case "путь", "path":
		return r.path
	}
	return nil
}

func (r *dslTextReader) Set(field string, val any) {}

func (r *dslTextReader) CallMethod(name string, args []any) any {
	switch name {
	case "открыть", "open":
		if r.path == "" {
			panic(userError{Msg: "ЧтениеТекста.Открыть: не указан путь к файлу"})
		}
		data, err := os.ReadFile(safePathOrRaise("ЧтениеТекста.Открыть", r.path))
		if err != nil {
			panic(userError{Msg: "ЧтениеТекста: ошибка чтения файла " + r.path + ": " + err.Error()})
		}
		r.content = decodeText(data)
		r.lines = strings.Split(r.content, "\n")
		r.lineIdx = 0
		r.isOpen = true
		return nil
	case "прочитать", "read":
		if !r.isOpen {
			panic(userError{Msg: "ЧтениеТекста.Прочитать: файл не открыт"})
		}
		return r.content
	case "прочитатьстроку", "readline":
		if !r.isOpen {
			panic(userError{Msg: "ЧтениеТекста.ПрочитатьСтроку: файл не открыт"})
		}
		if r.lineIdx >= len(r.lines) {
			return nil
		}
		line := r.lines[r.lineIdx]
		r.lineIdx++
		return line
	case "закрыть", "close":
		r.isOpen = false
		return nil
	}
	panic(userError{Msg: "ЧтениеТекста: неизвестный метод " + name})
}

// ─── dslTextWriter (ЗаписьТекста) ──────────────────────────────────────────

type dslTextWriter struct {
	path   string
	buf    strings.Builder
	isOpen bool
}

func (w *dslTextWriter) Get(field string) any {
	switch field {
	case "открыта", "isopen":
		return w.isOpen
	case "путь", "path":
		return w.path
	}
	return nil
}

func (w *dslTextWriter) Set(field string, val any) {}

func (w *dslTextWriter) CallMethod(name string, args []any) any {
	switch name {
	case "открыть", "open":
		if w.path == "" {
			panic(userError{Msg: "ЗаписьТекста.Открыть: не указан путь к файлу"})
		}
		w.buf.Reset()
		w.isOpen = true
		return nil
	case "записать", "write":
		if !w.isOpen {
			panic(userError{Msg: "ЗаписьТекста.Записать: файл не открыт"})
		}
		if len(args) > 0 {
			fmt.Fprintf(&w.buf, "%v", args[0])
		}
		return nil
	case "записатьстроку", "writeline":
		if !w.isOpen {
			panic(userError{Msg: "ЗаписьТекста.ЗаписатьСтроку: файл не открыт"})
		}
		if len(args) > 0 {
			fmt.Fprintf(&w.buf, "%v", args[0])
		}
		w.buf.WriteByte('\n')
		return nil
	case "закрыть", "close":
		if w.isOpen && w.path != "" {
			err := os.WriteFile(safePathOrRaise("ЗаписьТекста.Закрыть", w.path), []byte(w.buf.String()), fsmode.File)
			if err != nil {
				panic(userError{Msg: "ЗаписьТекста: ошибка записи файла " + w.path + ": " + err.Error()})
			}
		}
		w.isOpen = false
		return nil
	}
	panic(userError{Msg: "ЗаписьТекста: неизвестный метод " + name})
}

// ─── dslFile (Файл) ───────────────────────────────────────────────────────

type dslFile struct {
	path string
	info os.FileInfo
}

func (f *dslFile) loadInfo() {
	if f.info == nil {
		f.info, _ = os.Stat(f.path)
	}
}

func (f *dslFile) Get(field string) any {
	f.loadInfo()
	switch field {
	case "существует", "exists":
		return f.info != nil
	case "этокаталог", "isdirectory":
		return f.info != nil && f.info.IsDir()
	case "размер", "size":
		if f.info != nil {
			return float64(f.info.Size())
		}
		return float64(0)
	case "полноеимя", "fullname":
		return f.path
	case "имя", "name":
		return filepath.Base(f.path)
	case "расширение", "extension":
		return filepath.Ext(f.path)
	case "имябезрасширения", "namewithoutextension":
		name := filepath.Base(f.path)
		ext := filepath.Ext(name)
		return name[:len(name)-len(ext)]
	}
	return nil
}

func (f *dslFile) Set(field string, val any) {}

func (f *dslFile) CallMethod(name string, args []any) any {
	switch name {
	case "существует", "exists":
		f.info = nil
		f.loadInfo()
		return f.info != nil
	}
	panic(userError{Msg: "Файл: неизвестный метод " + name})
}

// ─── DecodeFile builtin ───────────────────────────────────────────────────

// decodeFileBuiltin converts raw bytes (Windows-1251 → UTF-8) when needed.
// Used for uploaded file content that may not be UTF-8.
func decodeFileBuiltin(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return nil, i18nerr.New("ДекодироватьФайл: требуется текст")
	}
	s := fmt.Sprintf("%v", args[0])
	if utf8.ValidString(s) {
		return s, nil
	}
	decoded, err := charmap.Windows1251.NewDecoder().String(s)
	if err != nil {
		return s, nil
	}
	return decoded, nil
}

// ─── NewFileFunctions ──────────────────────────────────────────────────────

// FileGuard вызывается перед каждой файловой операцией. nil → без ограничений.
type FileGuard func() error

// checkFile паникует userError'ом, если guard запрещает файловые операции.
// Сообщение человеческое и ловится Попыткой (как checkNet, план 62).
func checkFile(guard FileGuard) {
	checkFileAt(guard, "", 0)
}

// checkFileAt — location-aware вариант для BuiltinFunc. Фабрики сохраняют
// прежнюю сигнатуру func([]any) any и вызывают checkFile без позиции.
func checkFileAt(guard FileGuard, file string, line int) {
	if guard == nil {
		return
	}
	if err := guard(); err != nil {
		panic(userError{Msg: err.Error(), File: file, Line: line, Err: err})
	}
}

// guardedFile оборачивает файловый builtin проверкой guard'а.
func guardedFile(guard FileGuard, fn BuiltinFunc) BuiltinFunc {
	return func(args []any, file string, line int) (any, error) {
		checkFileAt(guard, file, line)
		return fn(args, file, line)
	}
}

func NewFileFunctions(guard FileGuard) map[string]any {
	m := map[string]any{}

	textReaderFactory := func(args []any) any {
		checkFile(guard)
		return &dslTextReader{path: strArg(args, 0)}
	}
	textWriterFactory := func(args []any) any {
		checkFile(guard)
		return &dslTextWriter{path: strArg(args, 0)}
	}
	fileFactory := func(args []any) any {
		checkFile(guard)
		return &dslFile{path: strArg(args, 0)}
	}

	m["__factory_ЧтениеТекста"] = textReaderFactory
	m["__factory_TextReader"] = textReaderFactory
	m["__factory_ЗаписьТекста"] = textWriterFactory
	m["__factory_TextWriter"] = textWriterFactory
	m["__factory_Файл"] = fileFactory
	m["__factory_File"] = fileFactory

	m["декодироватьфайл"] = guardedFile(guard, decodeFileBuiltin)
	m["decodefile"] = guardedFile(guard, decodeFileBuiltin)

	// Процедурные файловые builtins (глобально зарегистрированы в
	// builtins_files.go) перекрываются здесь обёрткой с guard'ом: extraVars
	// разрешаются раньше глобальной карты builtins (interpreter.go:619).
	m["копироватьфайл"] = guardedFile(guard, copyFileFn)
	m["copyfile"] = guardedFile(guard, copyFileFn)
	m["переместитьфайл"] = guardedFile(guard, moveFileFn)
	m["movefile"] = guardedFile(guard, moveFileFn)
	m["удалитьфайлы"] = guardedFile(guard, deleteFileFn)
	m["deletefiles"] = guardedFile(guard, deleteFileFn)
	m["создатькаталог"] = guardedFile(guard, makeDirFn)
	m["createdirectory"] = guardedFile(guard, makeDirFn)
	m["найтифайлы"] = guardedFile(guard, findFilesFn)
	m["findfiles"] = guardedFile(guard, findFilesFn)

	// Эти функции не читают и не создают сам файл, но раскрывают путь хоста и
	// выдают пригодное для последующей записи имя. Поэтому DenyFile обязан
	// перекрывать их тем же guard'ом, что и остальные файловые возможности.
	m["каталогвременныхфайлов"] = guardedFile(guard, catchableEnvBuiltin(tempDirFn))
	m["tempfilesdir"] = guardedFile(guard, catchableEnvBuiltin(tempDirFn))
	m["получитьимявременногофайла"] = guardedFile(guard, catchableEnvBuiltin(tempFileNameFn))
	m["gettempfilename"] = guardedFile(guard, catchableEnvBuiltin(tempFileNameFn))

	return m
}
