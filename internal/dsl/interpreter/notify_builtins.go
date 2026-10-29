package interpreter

import (
	"fmt"
	"strings"
)

// Notifier публикует уведомление в real-time-шину «сервер → браузер» (план 74).
// Интерфейс объявлен здесь, чтобы пакет interpreter не зависел от
// internal/realtime; конкретную реализацию (адаптер над *realtime.Hub)
// инжектирует слой UI/конфигурации через dslvars.
type Notifier interface {
	// Publish доставляет событие по адресу target (логин | "роль:<Имя>" | "*").
	Publish(target, name string, data any)
}

// FormNotifier — необязательная способность издателя: оповестить открытые
// формы текущего пользователя (Оповестить 1С). Её реализует только издатель
// запроса UI, которому известен пользователь; у фоновых заданий и тестов её
// нет, и Оповестить там — тихий no-op, как Оповестить на сервере 1С не
// существует вовсе.
type FormNotifier interface {
	NotifyForms(eventName string, param, source any)
}

// NewNotifyFunctions возвращает DSL-функции публикации уведомлений
// (ОтправитьУведомление / PublishNotification). Если n == nil — функции
// остаются тихим no-op (фоновые задания, тесты, не подключённая шина),
// поэтому конфигурация с вызовом не падает там, где push недоступен.
func NewNotifyFunctions(n Notifier) map[string]any {
	publish := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("ОтправитьУведомление: ожидаются аргументы (Кому, Событие[, Данные])")
		}
		if n == nil {
			return nil, nil
		}
		var data any
		if len(args) >= 3 {
			// DSL-значение (Структура/Соответствие/Массив/Ссылка) → JSON-нативное:
			// SSE-кадр сериализуется обычным json.Marshal, а у *Struct/*Map поля
			// неэкспортируемые (без конвертации на клиент пришло бы «{}»).
			data = valueToJSON(args[2])
		}
		n.Publish(notifyArgString(args[0]), notifyArgString(args[1]), data)
		return nil, nil
	})
	// ПоказатьОповещениеПользователя(Кому, Заголовок, Текст[, Ссылка[, Важность]])
	// — сахар над ОтправитьУведомление(Кому, "ui.оповещение", …) (план 87, ступень B).
	// Первый параметр Кому — потому что вызов серверный (в 1С «УведомлениеКлиента»
	// на клиенте). Важность "важное" — тост не исчезает сам. Ссылка — Структура
	// {вид, сущность, id}: клик по тосту откроет форму объекта во вкладке.
	showNotify := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("ПоказатьОповещениеПользователя: ожидаются (Кому, Заголовок, Текст[, Ссылка[, Важность]])")
		}
		if n == nil {
			return nil, nil
		}
		payload := map[string]any{
			"заголовок": notifyArgString(args[1]),
			"текст":     notifyArgString(args[2]),
			"важность":  "обычное",
		}
		// Ссылка (Структура/Соответствие/Ссылка) → JSON-нативное значение, чтобы
		// SSE-кадр (обычный json.Marshal) сериализовался корректно.
		if len(args) >= 4 && args[3] != nil {
			if link := valueToJSON(args[3]); link != nil {
				payload["ссылка"] = link
			}
		}
		if len(args) >= 5 {
			v := strings.ToLower(strings.TrimSpace(notifyArgString(args[4])))
			if v == "важное" || v == "important" {
				payload["важность"] = "важное"
			}
		}
		n.Publish(notifyArgString(args[0]), "ui.оповещение", payload)
		return nil, nil
	})
	// Оповестить(ИмяСобытия[, Параметр[, Источник]]) — как Оповестить 1С:
	// каждая открытая форма пользователя, у которой объявлен обработчик
	// ОбработкаОповещения, выполнит его на своём текущем состоянии. Адресата
	// нет: оповещение уходит формам того, кто выполняет код (в 1С — формам
	// своего клиента).
	notifyForms := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 || len(args) > 3 {
			return nil, fmt.Errorf("ожидаются аргументы Оповестить(ИмяСобытия[, Параметр[, Источник]])")
		}
		name := strings.TrimSpace(notifyArgString(args[0]))
		if name == "" {
			return nil, fmt.Errorf("не задано ИмяСобытия у Оповестить")
		}
		fn, ok := n.(FormNotifier)
		if !ok || fn == nil {
			return nil, nil
		}
		var param, source any
		if len(args) >= 2 {
			param = args[1]
		}
		if len(args) >= 3 {
			source = args[2]
		}
		fn.NotifyForms(name, param, source)
		return nil, nil
	})
	return map[string]any{
		"Оповестить": notifyForms,
		"Notify":     notifyForms,
		"ОтправитьУведомление":           publish,
		"PublishNotification":            publish,
		"ПоказатьОповещениеПользователя": showNotify,
		"ShowUserNotification":           showNotify,
	}
}

// notifyArgString приводит адрес/имя события к строке (nil → "").
func notifyArgString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
