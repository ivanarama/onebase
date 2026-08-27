package cli

// Разделы «События управляемых форм» и «Пустые значения в DSL» (#1135).
//
// Оба описывают поведение движка, а не конвенцию, поэтому проверять «текст на
// месте» мало: устаревшая документация вреднее отсутствующей — её не
// перепроверяют. Здесь имена из руководства сверяются с теми словарями
// платформы, которые для них и заведены: набором событий форм
// (metadata.IsKnownFormEventType) и словарём контекста события табличной части
// (metadata.FormTablePartContextVars). Переименовали событие или переменную —
// тест назовёт разъехавшееся имя, а не молча оставит руководство врать.

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestAIGuide_РазделыСобытийИПустыхЗначений(t *testing.T) {
	g := generateAIGuide("")
	for _, want := range []string{
		"## События управляемых форм",
		"## Пустые значения в DSL",
		// Событие клиентское, а серверное — соседнее и отдельное: путаница между
		// ними и есть причина, по которой RLS на чтение писали в ПриОткрытии.
		"`ПриЧтенииНаСервере`",
		// Ровно те факты, ради которых заявка заведена.
		"debounce 250 мс",
		"ТОЛЬКО `ИмяТабличнойЧасти`",
		"readonly",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("в guide нет ожидаемого фрагмента: %q", want)
		}
	}
}

// TestAIGuide_ИменаСобытийФормЖивые — каждое событие, названное в руководстве,
// платформа обязана знать. Иначе конфигурация, написанная по руководству, тихо
// не сработает: незнакомый ключ events: обработчиком не станет.
func TestAIGuide_ИменаСобытийФормЖивые(t *testing.T) {
	guide := generateAIGuide("")
	section := guideSection(t, guide, "## События управляемых форм")
	for _, name := range backquoted(section) {
		// В разделе упомянуты и не-события: виды элементов, ключи YAML,
		// переменные. Проверяем только те имена, что заявлены как события.
		if !looksLikeFormEventName(name) {
			continue
		}
		if !metadata.IsKnownFormEventType(metadata.FormEventType(name)) {
			t.Errorf("руководство называет событие %q, которого нет в словаре платформы", name)
		}
	}
	// И наоборот: события, которые браузер действительно отправляет, обязаны
	// быть перечислены. Список закрытый — это те же пары, что разрешает
	// resolveBrowserFormEvent; выпавшее из таблицы событие никто не найдёт.
	for _, event := range []metadata.FormEventType{
		metadata.FormEventOnOpen, metadata.FormEventOnClick, metadata.FormEventOnChange,
		metadata.FormEventStartChoice, metadata.FormEventOnChoice,
		metadata.FormEventOnRowAdded, metadata.FormEventAfterRowAdd,
		metadata.FormEventOnRowDeleted, metadata.FormEventOnRowChanged,
		metadata.FormEventOnRowActivated,
	} {
		if !strings.Contains(section, "`"+string(event)+"`") {
			t.Errorf("раздел о событиях не упоминает %q, а браузер его отправляет", event)
		}
	}
}

// TestAIGuide_ПеременныеКонтекстаТЧИзСловаря — таблица переменных обработчика
// сверяется со словарём metadata.FormTablePartContextVars. Он же сторожит
// совпадение с рантаймом, поэтому опечатка в руководстве ловится здесь, а не
// на живой конфигурации, где переменная просто окажется Неопределено.
func TestAIGuide_ПеременныеКонтекстаТЧИзСловаря(t *testing.T) {
	section := guideSection(t, generateAIGuide(""), "## События управляемых форм")
	known := make(map[string]bool)
	for _, v := range metadata.FormTablePartContextVars() {
		known[strings.ToLower(v)] = true
	}
	// Переменные вне словаря контекста ТЧ: их кладёт не addValidatedTPEventContext.
	for _, extra := range []string{"объект", "этотобъект", "подборрезультат"} {
		known[extra] = true
	}
	for _, name := range backquoted(section) {
		if !looksLikeContextVarName(name) {
			continue
		}
		if !known[strings.ToLower(name)] {
			t.Errorf("руководство называет переменную обработчика %q, которой нет ни в словаре"+
				" контекста табличной части, ни среди переменных формы", name)
		}
	}
	// Переменные, названные в заявке #1135 как непознаваемые без чтения
	// исходников, обязаны быть в таблице.
	for _, want := range []string{"ИмяТабличнойЧасти", "НомерСтроки", "ТекущаяКолонка", "ТекущаяСтрока"} {
		if !strings.Contains(section, "`"+want+"`") {
			t.Errorf("в таблице переменных нет %q", want)
		}
	}
}

// guideSection вырезает раздел руководства от заголовка до следующего «## ».
func guideSection(t *testing.T, guide, heading string) string {
	t.Helper()
	start := strings.Index(guide, heading)
	if start < 0 {
		t.Fatalf("в guide нет раздела %q", heading)
	}
	rest := guide[start+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// backquoted собирает имена, взятые в руководстве в обратные кавычки.
func backquoted(text string) []string {
	var out []string
	parts := strings.Split(text, "`")
	for i := 1; i < len(parts); i += 2 {
		if name := strings.TrimSpace(parts[i]); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// looksLikeFormEventName — имя из руководства, которое читатель примет за
// событие: русское слово-примета в начале и без разделителей пути/вызова.
func looksLikeFormEventName(name string) bool {
	if strings.ContainsAny(name, " .(:<>/=") {
		return false
	}
	for _, prefix := range []string{"При", "Перед", "После", "Нажатие", "Выбор", "Начало", "Авто", "Обработка", "Выполнить"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// looksLikeContextVarName — имя, поданное в руководстве как переменная
// обработчика. Событий среди них нет: у них своя проверка выше.
func looksLikeContextVarName(name string) bool {
	if strings.ContainsAny(name, " .(:<>/=") || looksLikeFormEventName(name) {
		return false
	}
	for _, prefix := range []string{
		"Имя", "Индекс", "Номер", "Текущая", "Выделенные", "Подбор", "Объект", "ЭтотОбъект",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
