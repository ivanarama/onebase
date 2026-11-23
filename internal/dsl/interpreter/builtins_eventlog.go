package interpreter

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Замер и журнал из модуля, как в 1С: ТекущаяУниверсальнаяДатаВМиллисекундах
// для пошаговых замеров долгих команд и ЗаписьЖурналаРегистрации — строка в
// журнал сервера (тот же, что у платформы: journalctl, stderr). Без них замер
// приходилось собирать из ТекущаяДатаВремя (секунды) и Сообщить (видно только
// тому, кто нажал кнопку).
func init() {
	builtins["текущаяуниверсальнаядатавмиллисекундах"] = universalMillisFn
	builtins["currentuniversaldateinmilliseconds"] = universalMillisFn
	builtins["записьжурналарегистрации"] = writeLogEventFn
	builtins["writelogevent"] = writeLogEventFn
}

// eventLogCommentLimit — предел длины события и комментария в журнале:
// строка из модуля не должна раздувать журнал сервера.
const eventLogCommentLimit = 4000

func universalMillisFn(args []any, _ string, _ int) (any, error) {
	return float64(time.Now().UnixMilli()), nil
}

// ЗаписьЖурналаРегистрации(Событие, Уровень, ОбъектМетаданных, Данные,
// Комментарий[, РежимТранзакции]) — запись в журнал сервера: slog с
// component=dsl, event, comment и местом вызова. Уровень — значение
// УровеньЖурналаРегистрации (Информация, Предупреждение, Ошибка,
// Примечание); пропущенный — Информация. ОбъектМетаданных, Данные и
// РежимТранзакции принимаются для совместимости с кодом 1С и не пишутся.
func writeLogEventFn(args []any, file string, line int) (any, error) {
	event := eventLogText(strArg(args, 0))
	comment := eventLogText(strArg(args, 4))
	// Значения — структурные атрибуты: обработчик журнала их экранирует, а
	// управляющие символы eventLogText уже заменил, поэтому подделать строку
	// журнала из модуля нельзя.
	slog.Log(context.Background(), eventLogLevel(strArg(args, 1)), "dsl event log", //nolint:gosec // G706: событие и комментарий очищены eventLogText и пишутся атрибутами, не в текст сообщения
		"component", "dsl", "event", event, "comment", comment, "source", file, "line", line)
	return nil, nil
}

func eventLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "предупреждение", "warning":
		return slog.LevelWarn
	case "ошибка", "error":
		return slog.LevelError
	case "примечание", "note":
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// eventLogText обрезает событие и комментарий до eventLogCommentLimit символов
// и заменяет управляющие символы (перевод строки, возврат каретки, табуляцию)
// пробелом: запись из модуля — одна строка журнала.
func eventLogText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) <= eventLogCommentLimit {
		return s
	}
	return string([]rune(s)[:eventLogCommentLimit]) + "…"
}
