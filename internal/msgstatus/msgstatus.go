// Package msgstatus переносит статус сообщения Сообщить(Текст, Статус) по
// путям, где сообщение — просто строка.
//
// Сообщения модулей во всём движке ходят срезом строк: от хуков записи и
// проведения через entityservice, документы из DSL, обработки, регламентные
// задания до ответа формы, REST и командной строки. Тип с полем статуса
// пришлось бы протянуть через несколько десятков сигнатур, и любое забытое
// место молча теряло бы статус. Поэтому статус едет в самой строке —
// невидимым префиксом из U+2063 (INVISIBLE SEPARATOR), завершённым U+2064
// (INVISIBLE PLUS); число U+2063 — номер статуса. Точки выдачи человеку
// (ответ формы, страница, панель сообщений, REST, CLI) снимают префикс через
// Split/Strip. Если какая-то выдача префикс не сняла, текст всё равно
// читается как обычный: оба символа невидимы.
package msgstatus

import (
	"fmt"
	"strings"
)

// Статусы как в 1С (СтатусСообщения). Пустая строка — без статуса (Обычное).
const (
	Ordinary      = ""
	Information   = "information"
	Attention     = "attention"
	Important     = "important"
	VeryImportant = "very_important"
)

const (
	countRune = '⁣'
	endRune   = '⁤'
)

// order — номер статуса в префиксе; индекс 0 (Обычное) префикса не имеет.
var order = []string{Ordinary, Information, Attention, Important, VeryImportant}

// Encode добавляет к тексту метку статуса. Обычный и неизвестный статус —
// текст без изменений.
func Encode(text, status string) string {
	text = Strip(text)
	for i, s := range order {
		if i > 0 && s == status {
			return strings.Repeat(string(countRune), i) + string(endRune) + text
		}
	}
	return text
}

// Split возвращает текст без метки и статус сообщения.
func Split(s string) (text, status string) {
	n := 0
	for _, r := range s {
		if r != countRune {
			break
		}
		n++
	}
	rest := s[n*len(string(countRune)):]
	if n == 0 || n >= len(order) || !strings.HasPrefix(rest, string(endRune)) {
		return s, Ordinary
	}
	return rest[len(string(endRune)):], order[n]
}

// Strip возвращает текст сообщения без метки статуса.
func Strip(s string) string {
	text, _ := Split(s)
	return text
}

// StripAll снимает метки со всех сообщений. Срез без меток возвращается как
// есть, без копии.
func StripAll(msgs []string) []string {
	for i, m := range msgs {
		if text, status := Split(m); status != Ordinary || text != m {
			out := make([]string, len(msgs))
			copy(out, msgs[:i])
			for j := i; j < len(msgs); j++ {
				out[j] = Strip(msgs[j])
			}
			return out
		}
	}
	return msgs
}

// IsError — статус, который интерфейс показывает как ошибку: Важное и
// ОченьВажное (как в 1С, где они выделяются красным).
func IsError(status string) bool {
	return status == Important || status == VeryImportant
}

// FromArgs собирает сообщение из аргументов Сообщить(Текст[, Статус]): текст
// с меткой статуса. ok=false — аргументов нет, сообщать нечего.
func FromArgs(args []any) (msg string, ok bool) {
	if len(args) == 0 {
		return "", false
	}
	status := Ordinary
	if len(args) > 1 {
		status = FromDSL(args[1])
	}
	return Encode(fmt.Sprintf("%v", args[0]), status), true
}

// FromDSL переводит второй аргумент Сообщить в статус. Принимает значение
// СтатусСообщения.X (строка «Важное») и английские имена; регистр не важен.
// Неизвестное и пустое — Обычное: сообщение не теряется из-за опечатки в
// статусе.
func FromDSL(v any) string {
	s, ok := v.(string)
	if !ok {
		return Ordinary
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "информация", "information":
		return Information
	case "внимание", "attention":
		return Attention
	case "важное", "important":
		return Important
	case "оченьважное", "veryimportant", "very_important":
		return VeryImportant
	}
	return Ordinary
}
