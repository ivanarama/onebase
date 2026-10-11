package onec_forms

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/token"
)

// Для частично поддерживаемых конструкций важна сигнатура, а не подстрока.
// Лексер сохраняет строки исходного BSL, пропускает комментарии и не принимает
// текст внутри строковых литералов за вызовы. Проверка не выводит типы переменных:
// динамическое выражение может вернуть строку имён для Структуры.
func scanBSLSignatures(p *BSLProcedure) []Warning {
	l := lexer.New(p.Body, "")
	var tokens []token.Token
	for t := l.NextToken(); t.Type != token.EOF; t = l.NextToken() {
		tokens = append(tokens, t)
	}
	var warnings []Warning
	for pos, t := range tokens {
		namePos := pos
		constructor := t.Type == token.NEW && pos+1 < len(tokens) && tokens[pos+1].Type == token.IDENT
		if constructor {
			namePos++
		} else if t.Type != token.IDENT || pos > 0 && tokens[pos-1].Type == token.DOT {
			continue
		}
		name := strings.ToLower(tokens[namePos].Literal)
		field, note := bslSignatureAdvice(name, constructor)
		if note == "" {
			continue
		}
		hasParens := namePos+1 < len(tokens) && tokens[namePos+1].Type == token.LPAREN
		// ВызватьИсключение без скобок — штатный оператор, включая rethrow.
		// Остальные функции проверяются только в позиции вызова.
		if !constructor && !hasParens {
			continue
		}
		var args [][]token.Token
		complete := true
		if hasParens {
			args, _, complete = bslCallArguments(tokens, namePos+1)
		}
		if complete && bslSignatureSupported(name, constructor, args) {
			continue
		}
		warnings = append(warnings, Warning{
			Severity: SeverityWarn, Code: W040_BSLNotInDSL, Element: p.Name,
			Field: field, Line: p.StartLine + t.Line,
			Message: fmt.Sprintf("сигнатура BSL %q требует адаптации для DSL OneBase", field), Suggest: note,
		})
	}
	return warnings
}

func bslSignatureAdvice(name string, constructor bool) (field, note string) {
	if constructor {
		switch name {
		case "запрос":
			return "Новый Запрос", "Запрос поддерживается без аргументов; текст конструктора игнорируется — задайте свойство Текст"
		case "массив":
			return "Новый Массив", "Массив поддерживается без аргументов; размеры конструктора игнорируются — заполните массив через Добавить()"
		case "соответствие":
			return "Новый Соответствие", "Соответствие поддерживается без аргументов; аргументы конструктора игнорируются — заполните через Вставить()"
		case "таблицазначений":
			return "Новый ТаблицаЗначений", "ТаблицаЗначений поддерживается без аргументов; колонки и строки добавляются методами коллекции"
		case "структура":
			return "Новый Структура", "Структура принимает строку имён полей и значения; копирование готовой структуры конструктором не поддерживается"
		}
		return "", ""
	}
	switch name {
	case "нстр":
		return "НСтр(", "НСтр поддерживает 1–2 аргумента: строку локализации и необязательный код языка"
	case "стршаблон":
		return "СтрШаблон(", "СтрШаблон требует шаблон первым аргументом, затем значения для подстановки"
	case "вызватьисключение":
		return "ВызватьИсключение", "ВызватьИсключение принимает одно описание ошибки; без аргумента перебрасывает исключение внутри блока Исключение"
	case "сообщить":
		return "Сообщить(", "Сообщить поддерживает текст сообщения; статус сообщения не поддерживается, доставка зависит от окружения исполнения"
	case "начатьтранзакцию":
		return "НачатьТранзакцию(", "НачатьТранзакцию поддерживается без аргументов в окружении с транзакционными функциями"
	case "зафиксироватьтранзакцию":
		return "ЗафиксироватьТранзакцию(", "ЗафиксироватьТранзакцию поддерживается без аргументов в окружении с транзакционными функциями"
	case "отменитьтранзакцию":
		return "ОтменитьТранзакцию(", "ОтменитьТранзакцию поддерживается без аргументов в окружении с транзакционными функциями"
	}
	return "", ""
}

func bslSignatureSupported(name string, constructor bool, args [][]token.Token) bool {
	if constructor {
		if name != "структура" {
			return len(args) == 0
		}
		if len(args) == 0 {
			return true
		}
		if len(args[0]) == 0 {
			return false
		}
		// Явное значение другого типа не является строкой ключей. Неизвестные
		// выражения остаются на проверку исполнения, как и прочие аргументы BSL.
		first := args[0]
		for len(first) >= 2 && first[0].Type == token.LPAREN {
			_, close, ok := bslCallArguments(first, 0)
			if !ok || close != len(first)-1 {
				break
			}
			first = first[1:close]
		}
		if len(first) == 0 {
			return false
		}
		if len(first) == 1 {
			switch first[0].Type {
			case token.NUMBER, token.DATE, token.TRUE, token.FALSE:
				return false
			}
			return !strings.EqualFold(first[0].Literal, "Неопределено")
		}
		if first[0].Type == token.NEW && first[1].Type == token.IDENT {
			if len(first) == 2 {
				return false
			}
			if first[2].Type == token.LPAREN {
				_, close, ok := bslCallArguments(first, 2)
				if ok && close == len(first)-1 {
					return false
				}
			}
		}
		return true
	}
	switch name {
	case "нстр":
		return len(args) >= 1 && len(args) <= 2 && len(args[0]) > 0
	case "стршаблон":
		return len(args) >= 1 && len(args[0]) > 0
	case "вызватьисключение":
		return len(args) <= 1
	case "сообщить":
		return len(args) == 1 && len(args[0]) > 0
	default:
		return len(args) == 0 // транзакционные функции
	}
}

// bslCallArguments разделяет аргументы только на верхнем уровне скобок.
// Пустые позиции сохраняются: пропуски не должны превращать неподдерживаемую
// сигнатуру в поддерживаемую. Незакрытые/несогласованные скобки не дают снять W040.
func bslCallArguments(tokens []token.Token, open int) ([][]token.Token, int, bool) {
	var args [][]token.Token
	var stack []token.Type
	start := open + 1
	for pos := start; pos < len(tokens); pos++ {
		switch tokens[pos].Type {
		case token.ILLEGAL, token.SEMICOLON:
			return nil, 0, false
		case token.LPAREN:
			stack = append(stack, token.RPAREN)
		case token.LBRACKET:
			stack = append(stack, token.RBRACKET)
		case token.RPAREN, token.RBRACKET:
			if len(stack) == 0 {
				if tokens[pos].Type != token.RPAREN {
					return nil, 0, false
				}
				if pos > start || len(args) > 0 {
					args = append(args, tokens[start:pos])
				}
				return args, pos, true
			}
			if stack[len(stack)-1] != tokens[pos].Type {
				return nil, 0, false
			}
			stack = stack[:len(stack)-1]
		case token.COMMA:
			if len(stack) == 0 {
				args = append(args, tokens[start:pos])
				start = pos + 1
			}
		}
	}
	return nil, 0, false
}
