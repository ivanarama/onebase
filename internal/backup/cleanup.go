package backup

import (
	"errors"
	"io"
	"log/slog"
	"os"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

// Уборка на путях резервного копирования и восстановления.
//
// Здесь идут закрытия читающей стороны (открытый дамп, распакованный поток,
// проверочное соединение с копией) и удаление временных путей. Наверх в этот
// момент уходит либо успех, либо уже сформированная причина сбоя, и подменять
// её ошибкой уборки значит потерять настоящую.
//
// Граница: Close ПИШУЩЕЙ стороны сюда не относится. Он сбрасывает буфер, и его
// ошибка означает усечённый дамп — то есть неисправную резервную копию,
// выглядящую исправной. Такие места (gzip-writer в Dump, tmp в restoreFile)
// разбирают ошибку сами и уже это делают.

func backupLog() *slog.Logger { return oblog.Component("backup") }

// closeRead закрывает читающую сторону.
func closeRead(what string, c io.Closer) {
	if err := c.Close(); err != nil {
		backupLog().Debug("не удалось закрыть "+what, "err", err)
	}
}

// removeTemp удаляет временный файл или каталог.
func removeTemp(path string) {
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		backupLog().Debug("не удалось удалить временный путь", "path", path, "err", err)
	}
}
