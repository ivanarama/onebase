package metadata

import "sort"

// Разрыв «словарь знает — движок не вызывает» (#1153). Словарь
// knownFormEventTypes описывает всё, что конфигурация ВПРАВЕ объявить: он один
// на managed-формы, конвертер 1С и редактор. Диспетчеров же два, и оба уже
// словаря: браузерный (browserFormEventRules и соседи в form_browser_events.go)
// и серверный — он описан здесь. Событие, не попавшее ни в один, объявляется
// без ошибки, `onebase check` его принимает, а обработчик не запускается
// никогда: симптом «не работает и ошибки нет».
//
// Перечень невызываемых намеренно НЕ хранится списком, а вычисляется как
// «известные минус диспетчеризуемые». Рукописный список пришлось бы вычёркивать
// руками в тот день, когда событие реализуют, и забытая строка заставила бы
// `onebase check` ругаться на живое событие — предупреждение пережило бы свою
// причину. Здесь этот день закрывается сам: событие, добавленное в браузерную
// таблицу или в serverFormEventTypes, из UncalledFormEvents исчезает.
//
// Остаточный риск ровно один и он именованный: serverFormEventTypes — единственное
// место, где «диспетчер есть» записано словами, а не выведено из таблицы.
// Расхождение с internal/ui сторожит TestServerFormEvents_ВызываютсяСервером там же.

// serverFormEventTypes — события уровня формы, которые запускает сервер, а не
// браузер: ПриЧтенииНаСервере на подготовке HTML (internal/ui/form_server_events.go,
// formReadHook) и триада записи там же (runPreSaveFormHooks, runAfterWriteFormHook).
// В browserFormEventRules их нет и быть не должно — та таблица описывает
// закрытую модель того, что вправе прислать браузер.
var serverFormEventTypes = []FormEventType{
	FormEventOnReadAtServer,
	FormEventBeforeWrite,
	FormEventOnWrite,
	FormEventAfterWrite,
}

// ServerFormEvents возвращает копию перечня событий формы, которые запускает
// сервер.
func ServerFormEvents() []FormEventType {
	return append([]FormEventType(nil), serverFormEventTypes...)
}

// IsDispatchedFormEventType сообщает, есть ли у события вызывающая сторона:
// браузерная точка входа либо серверный хук.
func IsDispatchedFormEventType(event FormEventType) bool {
	for _, e := range BrowserFormEvents() {
		if e == event {
			return true
		}
	}
	for _, e := range serverFormEventTypes {
		if e == event {
			return true
		}
	}
	return false
}

// IsUncalledFormEventType — событие известно словарю платформы, но не
// вызывается ни браузером, ни сервером. Такое имя приняли конвертер 1С и
// редактор, обработчик на него пишется и проходит проверку, но не исполняется.
func IsUncalledFormEventType(event FormEventType) bool {
	return IsKnownFormEventType(event) && !IsDispatchedFormEventType(event)
}

// UncalledFormEvents — отсортированный перечень известных событий без
// диспетчера. Порядок устойчив: перечень вычисляется по карте, а сообщения
// проверки и текст руководства обязаны быть воспроизводимыми.
func UncalledFormEvents() []FormEventType {
	out := make([]FormEventType, 0, len(knownFormEventTypes))
	for event := range knownFormEventTypes {
		if !IsDispatchedFormEventType(event) {
			out = append(out, event)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
