// Package i18nerr — ошибки платформы с локализуемым сообщением.
//
// Ключ — русский fmt-шаблон («неизвестная таблица %s»), как и все ключи
// i18n OneBase. Error() всегда рендерит по-русски (логи, CLI не меняются),
// Localize переводит сообщение на HTTP-границе: i18nerr-звенья цепочки —
// по шаблону с подстановкой аргументов, прочие ошибки — exact-match-ом
// полного текста; всё, что перевести нечем, остаётся русским.
package i18nerr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n"
)

// Error — ошибка с шаблоном-ключом и аргументами.
type Error struct {
	Key     string
	Args    []any
	wrapped error
}

// New создаёт ошибку со статическим ключом. Ключ не прогоняется через fmt,
// поэтому для сообщений с литеральным «%» используй New — не Errorf.
func New(key string) error { return &Error{Key: key} }

// Errorf создаёт ошибку с fmt-шаблоном и аргументами. Ключ — fmt-шаблон;
// литеральный % в ключе экранируй как %%.
func Errorf(key string, args ...any) error { return &Error{Key: key, Args: args} }

// Wrapf оборачивает err локализуемым префиксом: «<key с args>: <err>».
func Wrapf(err error, key string, args ...any) error {
	return &Error{Key: key, Args: args, wrapped: err}
}

func (e *Error) Error() string {
	msg := e.render()
	if e.wrapped != nil {
		return msg + ": " + e.wrapped.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.wrapped }

// render — русское сообщение без wrapped-части.
func (e *Error) render() string {
	if len(e.Args) == 0 {
		return e.Key
	}
	return fmt.Sprintf(e.Key, e.Args...)
}

// localize — перевод шаблона и подстановка аргументов.
func (e *Error) localize(b *i18n.Bundle, lang string) string {
	tpl := b.T(lang, e.Key)
	if len(e.Args) == 0 {
		return tpl
	}
	return fmt.Sprintf(tpl, e.Args...)
}

// Localize переводит сообщение об ошибке для языка lang.
func Localize(b *i18n.Bundle, lang string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if b == nil || lang == "" || i18n.IsBaseLang(lang) {
		return msg
	}
	// Статическое сообщение целиком (включая ошибки без i18nerr).
	if t := b.T(lang, msg); t != msg {
		return t
	}
	// Структурная сборка по цепочке ошибок.
	return localizeChain(b, lang, err)
}

// localizeChain собирает перевод по структуре цепочки ошибок: i18nerr-звенья
// переводятся по своему шаблону, ветки errors.Join — каждая по отдельности,
// обёртки fmt.Errorf("…: %w") сохраняют свой префикс с переводом хвоста,
// прочие ошибки переводятся exact-match-ом или остаются как есть. Структурная
// сборка (а не strings.Replace по полному тексту) исключает ложные
// подстановки, когда перевод внешнего звена содержит русский рендер
// внутреннего как подстроку.
func localizeChain(b *i18n.Bundle, lang string, err error) string {
	if e, ok := err.(*Error); ok {
		head := e.localize(b, lang)
		if e.wrapped == nil {
			return head
		}
		return head + ": " + localizeChain(b, lang, e.wrapped)
	}
	// errors.Join: несколько независимых причин одного отказа. Без этой ветки
	// errors.Unwrap вернул бы nil (у Join сигнатура Unwrap() []error), перевод
	// свёлся бы к exact-match по склеенному тексту и не находил бы ничего —
	// каждая причина по отдельности при этом переводится прекрасно. Разделитель
	// тот же, каким Join печатает свой Error().
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		if parts := multi.Unwrap(); len(parts) > 0 {
			out := make([]string, 0, len(parts))
			for _, part := range parts {
				out = append(out, localizeChain(b, lang, part))
			}
			return strings.Join(out, "\n")
		}
	}
	inner := errors.Unwrap(err)
	if inner == nil {
		return b.T(lang, err.Error())
	}
	// Не-i18nerr обёртка (fmt.Errorf "%w"): хвост переводим, префикс остаётся.
	msg := err.Error()
	innerMsg := inner.Error()
	if strings.HasSuffix(msg, innerMsg) {
		return msg[:len(msg)-len(innerMsg)] + localizeChain(b, lang, inner)
	}
	return msg // нестандартная обёртка — без перевода
}
