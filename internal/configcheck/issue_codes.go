package configcheck

import "strings"

const (
	CodeCheckFailed           = "CHECK_FAILED"
	CodeProjectLoadFailed     = "PROJECT_LOAD_FAILED"
	CodeYAMLInvalid           = "YAML_INVALID"
	CodeDSLParseError         = "DSL_PARSE_ERROR"
	CodeDSLUnknownFunction    = "DSL_UNKNOWN_FUNCTION"
	CodeDSLNoEffect           = "DSL_NO_EFFECT"
	CodeQueryInvalid          = "QUERY_INVALID"
	CodeQueryExecutionInvalid = "QUERY_EXECUTION_INVALID"
	CodeReferenceNotFound     = "REFERENCE_NOT_FOUND"
	CodeUnsupportedKey        = "CONFIG_UNSUPPORTED_KEY"
	CodeNameCollision         = "NAME_COLLISION"
	CodePrintformLegacy       = "PRINTFORM_LEGACY_FORMAT"
	CodePrintformEmpty        = "PRINTFORM_EMPTY"
)

// NormalizeIssues fills machine-readable code/suggestedFix fields for issues
// that were produced by older validators without explicit structured metadata.
func NormalizeIssues(in []Issue) []Issue {
	if len(in) == 0 {
		return nil
	}
	out := make([]Issue, len(in))
	for i, is := range in {
		if is.Code == "" || is.SuggestedFix == "" {
			code, fix := classifyIssue(is)
			if is.Code == "" {
				is.Code = code
			}
			if is.SuggestedFix == "" {
				is.SuggestedFix = fix
			}
		}
		out[i] = is
	}
	return out
}

func classifyIssue(is Issue) (code, fix string) {
	msg := strings.ToLower(is.Message)
	kind := strings.ToLower(is.Kind)
	file := strings.ToLower(is.File)

	switch {
	case strings.Contains(msg, "project.load"):
		return CodeProjectLoadFailed, "Исправьте ошибку загрузки проекта: обычно это битый YAML, дубли имён или неверная ссылка в метаданных."
	case strings.Contains(msg, "коллизия имён") || strings.Contains(kind, "имя таблицы"):
		return CodeNameCollision, "Переименуйте один из объектов так, чтобы имена таблиц не совпадали после нормализации."
	case strings.Contains(msg, "устаревший формат печатной формы"):
		return CodePrintformLegacy, "Мигрируйте печатную форму командой onebase printforms migrate или перепишите её в layout v2."
	case strings.Contains(msg, "форма пустая"):
		return CodePrintformEmpty, "Задайте поддерживаемые поля печатной формы: title, header, table или footer."
	case strings.Contains(msg, "неизвестная функция"):
		return CodeDSLUnknownFunction, "Проверьте имя функции, добавьте процедуру в модуль или замените вызов на поддерживаемый builtin из ai-guide."
	case strings.Contains(msg, "выражение без эффекта"):
		return CodeDSLNoEffect, "Добавьте скобки вызова функции или удалите выражение, если оно не должно выполняться."
	case strings.Contains(kind, "исполнение запроса"):
		return CodeQueryExecutionInvalid, "Проверьте имена таблиц/полей и SQL, который получится после компиляции; для отчёта используйте onebase report explain."
	case strings.Contains(kind, "запрос") || strings.Contains(msg, "запрос"):
		return CodeQueryInvalid, "Исправьте текст запроса: проверьте источники, поля, параметры &Имя и поддерживаемый синтаксис OneBase."
	case strings.Contains(msg, "не найден") || strings.Contains(msg, "не найдена"):
		return CodeReferenceNotFound, "Создайте отсутствующий объект/поле или исправьте имя ссылки в YAML, роли, подсистеме, форме или странице."
	case strings.Contains(msg, "не поддерживается"):
		return CodeUnsupportedKey, "Удалите неподдерживаемый ключ или замените его поддерживаемой структурой конфигурации."
	case strings.Contains(kind, "dsl") || strings.HasSuffix(file, ".os"):
		return CodeDSLParseError, "Исправьте синтаксис встроенного языка в указанной строке и повторите onebase check."
	case strings.HasSuffix(file, ".yaml") || isYAMLKind(kind):
		return CodeYAMLInvalid, "Проверьте YAML-синтаксис и набор поддерживаемых полей для этого типа объекта."
	default:
		return CodeCheckFailed, "Откройте указанный файл, исправьте сообщение проверки и повторите onebase check."
	}
}

func isYAMLKind(kind string) bool {
	for _, part := range []string{
		"справочник", "документ", "регистр", "перечисление", "константы",
		"виджет", "отчёт", "роль", "страница", "сервис", "подсистема",
		"журнал", "печатная форма", "обработка", "главная страница",
	} {
		if strings.Contains(kind, part) {
			return true
		}
	}
	return false
}
