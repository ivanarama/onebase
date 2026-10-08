package interpreter

import (
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

// fileSandboxRoot — корень, которым ограничены файловые builtins
// (ЧтениеТекста/ЗаписьТекста и КопироватьФайл/НайтиФайлы/…). Пустая строка —
// ограничение выключено (любой путь разрешён), это поведение по умолчанию для
// доверенного desktop-режима.
//
// Значение задаётся один раз при старте сервера (SetFileSandbox) до начала
// обработки запросов и далее только читается, поэтому отдельная синхронизация
// не нужна.
var fileSandboxRoot string

// SetFileSandbox ограничивает файловые builtins каталогом root. Пустой root
// снимает ограничение. Включается, например, для demo-режима, где обработки
// исполняет недоверенный пользователь (см. cli.runServer).
func SetFileSandbox(root string) {
	if root == "" {
		fileSandboxRoot = ""
		return
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	fileSandboxRoot = filepath.Clean(abs)
}

// resolveSafePath проверяет путь p против sandbox. Если sandbox выключен —
// возвращает p как есть. Иначе относительные пути берутся от корня sandbox,
// путь очищается, и проверяется, что он не выходит за корень (через `..` или
// абсолютный путь вне корня). Возвращает абсолютный путь либо ошибку.
func resolveSafePath(p string) (string, error) {
	root := fileSandboxRoot
	if root == "" {
		return p, nil
	}
	abs := filepath.Clean(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Clean(filepath.Join(root, p))
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", i18nerr.Errorf("доступ к файлу вне рабочего каталога базы запрещён: %s", p)
	}
	return abs, nil
}

// safePathOrRaise возвращает безопасный путь либо прерывает выполнение DSL
// пользовательской ошибкой (panic userError, перехватывается Попыткой).
// op — имя операции для сообщения.
// ResolveSafePath — экспортированная обёртка resolveSafePath для файловых
// builtins вышестоящих слоёв (ui: вложения из DSL). Уважает ту же песочницу,
// что и встроенные файловые функции.
func ResolveSafePath(p string) (string, error) {
	return resolveSafePath(p)
}

// FileSandboxRoot — корень песочницы или пустая строка, если она выключена.
// Нужен вышестоящему слою, чтобы класть временные файлы двоичных параметров
// ВНУТРЬ песочницы: иначе обработка не смогла бы прочитать собственный файл.
func FileSandboxRoot() string {
	return fileSandboxRoot
}

func safePathOrRaise(op, p string) string {
	safe, err := resolveSafePath(p)
	if err != nil {
		// Сохраняем i18nerr (err) для локализации по цепочке: не-русский
		// пользователь увидит переведённое «доступ к файлу вне рабочего
		// каталога базы запрещён», а не русский текст.
		RaiseUserErrorWrap(op+": "+err.Error(), err)
	}
	return safe
}
