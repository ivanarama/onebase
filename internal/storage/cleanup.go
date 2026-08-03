package storage

import (
	"errors"
	"io"
	"log/slog"
	"os"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

// Уборка недописанных файлов вложений и блобов.
//
// Хранилище пишет файл, а метаданные о нём — в БД. Если запись файла сорвалась,
// на диске остаётся огрызок, и его надо убрать до возврата ошибки, иначе
// следующая попытка упрётся в O_EXCL, а место утечёт. Ошибка самой уборки при
// этом вторична: наверх идёт причина сбоя записи, подменять её «не удалось
// удалить временный файл» значит потерять настоящую.
//
// Поэтому решение принято один раз здесь: уборка логируется на Debug. Отдельно
// оговорено то, что уборкой НЕ является: Close пишущей стороны на успешном пути
// сбрасывает буфер, и его ошибка означает усечённый файл — она обязана дойти до
// вызывающего, иначе метаданные зарегистрируют огрызок как целый блоб.

func storageLog() *slog.Logger { return oblog.Component("storage") }

// discardPartial закрывает и удаляет недописанный файл на пути ошибки.
func discardPartial(f *os.File, path string) {
	if err := f.Close(); err != nil {
		storageLog().Debug("не удалось закрыть недописанный файл", "path", path, "err", err)
	}
	removeFile(path)
}

// removeFile удаляет файл или каталог; отсутствие — не ошибка.
func removeFile(path string) {
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		storageLog().Debug("не удалось удалить временный путь", "path", path, "err", err)
	}
}

// closeRead закрывает читающую сторону: данные уже прочитаны.
func closeRead(what string, c io.Closer) {
	if err := c.Close(); err != nil {
		storageLog().Debug("не удалось закрыть "+what, "err", err)
	}
}
