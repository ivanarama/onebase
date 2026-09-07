package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormEventDispatch возвращает НЕблокирующие предупреждения об обработчиках,
// объявленных на события, которые движок не вызывает (#1153).
//
// Словарь событий формы шире обоих диспетчеров: он один на managed-формы,
// конвертер 1С и редактор, поэтому имя вроде ПередЗакрытием платформа знает,
// `onebase check` принимает молча, а вызвать его некому. Симптом для
// конфигуратора — «обработчик не работает и ошибки нет»: самый дорогой класс,
// потому что искать нечего, всё зелёное.
//
// Предупреждение, а не ошибка, сознательно: такие обработчики уже лежат в
// рабочих конфигурациях (в том числе приехали импортом из 1С, где события
// настоящие) — сделать их ошибкой значит уронить `check` на конфигурации,
// которая работает ровно так же, как работала. Задача диагностики здесь —
// назвать мёртвый обработчик, а не запретить его.
//
// Перечень невызываемых берётся из metadata.UncalledFormEvents, где он
// ВЫЧИСЛЯЕТСЯ как «известные минус диспетчеризуемые». Поэтому в день, когда
// событие реализуют, вычёркивать его отсюда не нужно: предупреждение исчезнет
// само, а не переживёт свою причину.
func CheckFormEventDispatch(proj *project.Project) []Issue {
	uncalled := make(map[metadata.FormEventType]bool)
	for _, event := range metadata.UncalledFormEvents() {
		uncalled[event] = true
	}
	if len(uncalled) == 0 {
		return nil
	}

	var warns []Issue
	report := func(label, object string, form *metadata.FormModule) {
		add := func(where string, handlers map[metadata.FormEventType]string) {
			// Порядок сообщений не должен зависеть от обхода карты: две подряд
			// проверки одной конфигурации обязаны печатать одно и то же.
			for _, event := range metadata.UncalledFormEvents() {
				proc, ok := handlers[event]
				if !ok || strings.TrimSpace(proc) == "" {
					continue
				}
				warns = append(warns, Issue{
					File:   label,
					Object: object,
					Kind:   "Управляемая форма",
					Code:   "form.event-not-dispatched",
					Message: fmt.Sprintf("%s: обработчик %q объявлен на событие %q, которое движок не вызывает —"+
						" имя известно платформе (его знает конвертер 1С), но вызывающей стороны у него нет,"+
						" поэтому процедура не выполнится ни разу", where, strings.TrimSpace(proc), event),
					SuggestedFix: uncalledEventFix(event),
				})
			}
		}
		add("форма целиком", form.Handlers)
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			add(fmt.Sprintf("элемент %q", formElementName(el)), el.Handlers)
		})
	}

	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			report(formFileLabel(ent, form), ent.Name, form)
		}
	}
	for _, proc := range proj.Processors {
		for _, form := range proc.Forms {
			name := form.Name
			if name == "" {
				name = "объекта"
			}
			report("forms/"+strings.ToLower(proc.Name)+"/"+name+".form.yaml", proc.Name, form)
		}
	}
	return warns
}

// uncalledEventFix подсказывает ближайшее живое событие там, где оно есть.
// Совет даётся только по существу: для закрытия и активации формы замены нет
// вовсе, и выдумывать её вредно — конфигуратор потратит день на «эквивалент»,
// которого не существует.
func uncalledEventFix(event metadata.FormEventType) string {
	const server = "Серверные события формы — ПриЧтенииНаСервере, ПередЗаписью, ПриЗаписи, ПослеЗаписи;" +
		" браузерные перечислены в AGENTS.md, раздел «События управляемых форм»."
	switch event {
	case metadata.FormEventOnCreate:
		return "Заполнение нового объекта делает хук ПриСозданииНового модуля объекта (не формы)," +
			" значения по умолчанию — ключ default поля. " + server
	case metadata.FormEventItemChoice:
		return "Результат диалога подбора приходит в событие Выбор (билтин ПоказатьПодбор). " + server
	case metadata.FormEventBeforeClose, metadata.FormEventOnClose, metadata.FormEventOnActivate:
		return "Закрытие и активация формы платформой пока не отслеживаются, замены этому событию нет:" +
			" перенесите логику в запись (ПередЗаписью/ПослеЗаписи) или в открытие (ПриОткрытии)," +
			" либо уберите обработчик. " + server
	default:
		return "Уберите обработчик или перенесите логику в событие, у которого есть вызывающая сторона. " + server
	}
}
