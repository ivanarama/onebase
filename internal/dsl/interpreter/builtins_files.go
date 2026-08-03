package interpreter

import (
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"os"
	"path/filepath"
)

// init регистрирует глобальные файловые операции. Объект Файл и
// ЧтениеТекста/ЗаписьТекста уже есть (file_builtins.go); здесь — процедуры
// уровня файловой системы. Рассчитаны на однопользовательский desktop-режим,
// где DSL-обработки выполняет сам разработчик.
func init() {
	builtins["копироватьфайл"] = copyFileFn
	builtins["copyfile"] = copyFileFn
	builtins["переместитьфайл"] = moveFileFn
	builtins["movefile"] = moveFileFn
	builtins["удалитьфайлы"] = deleteFileFn
	builtins["deletefiles"] = deleteFileFn
	builtins["создатькаталог"] = makeDirFn
	builtins["createdirectory"] = makeDirFn
	builtins["найтифайлы"] = findFilesFn
	builtins["findfiles"] = findFilesFn
}

// КопироватьФайл(Откуда, Куда) — копирование содержимого файла.
func copyFileFn(args []any, _ string, _ int) (any, error) {
	src := safePathOrRaise("КопироватьФайл", strArg(args, 0))
	dst := safePathOrRaise("КопироватьФайл", strArg(args, 1))
	in, err := os.Open(src)
	if err != nil {
		RaiseUserError("КопироватьФайл: " + err.Error())
	}
	defer oblog.CloseQuiet("dsl", "исходный файл", in)
	out, err := os.Create(dst)
	if err != nil {
		RaiseUserError("КопироватьФайл: " + err.Error())
	}
	// Close пишущей стороны проверяем: он сбрасывает буфер, и без проверки
	// КопироватьФайл отчитался бы об успехе при усечённой копии.
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		RaiseUserError("КопироватьФайл: " + err.Error())
	}
	if err := out.Close(); err != nil {
		RaiseUserError("КопироватьФайл: " + err.Error())
	}
	return nil, nil
}

// ПереместитьФайл(Откуда, Куда) — переименование/перемещение.
func moveFileFn(args []any, _ string, _ int) (any, error) {
	src := safePathOrRaise("ПереместитьФайл", strArg(args, 0))
	dst := safePathOrRaise("ПереместитьФайл", strArg(args, 1))
	if err := os.Rename(src, dst); err != nil {
		RaiseUserError("ПереместитьФайл: " + err.Error())
	}
	return nil, nil
}

// УдалитьФайлы(Путь) — удаление файла или пустого каталога. Намеренно не
// рекурсивно (os.Remove), чтобы случайно не снести дерево каталогов.
func deleteFileFn(args []any, _ string, _ int) (any, error) {
	if err := os.Remove(safePathOrRaise("УдалитьФайлы", strArg(args, 0))); err != nil {
		RaiseUserError("УдалитьФайлы: " + err.Error())
	}
	return nil, nil
}

// СоздатьКаталог(Путь) — создание каталога вместе с родительскими.
func makeDirFn(args []any, _ string, _ int) (any, error) {
	if err := os.MkdirAll(safePathOrRaise("СоздатьКаталог", strArg(args, 0)), 0o755); err != nil {
		RaiseUserError("СоздатьКаталог: " + err.Error())
	}
	return nil, nil
}

// НайтиФайлы(Путь, Маска) → Массив путей подходящих файлов.
func findFilesFn(args []any, _ string, _ int) (any, error) {
	pattern := filepath.Join(safePathOrRaise("НайтиФайлы", strArg(args, 0)), strArg(args, 1))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		RaiseUserError("НайтиФайлы: " + err.Error())
	}
	arr := &Array{}
	for _, m := range matches {
		arr.items = append(arr.items, m)
	}
	return arr, nil
}
