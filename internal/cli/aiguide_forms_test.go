package cli

// Разделы «События управляемых форм» и «Пустые значения в DSL» (#1135).
//
// Оба описывают поведение движка, а не конвенцию, поэтому проверять «текст на
// месте» мало: устаревшая документация вреднее отсутствующей — её не
// перепроверяют. Здесь имена из руководства сверяются с теми словарями
// платформы, которые для них и заведены: набором событий форм
// (metadata.IsKnownFormEventType), перечнем событий, реально отправляемых
// браузером (metadata.BrowserFormEvents), и словарём контекста события
// табличной части (metadata.FormTablePartContextVars). Переименовали событие
// или переменную — тест назовёт разъехавшееся имя, а не молча оставит
// руководство врать.

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
	// быть перечислены. Перечень берётся из самой таблицы платформы
	// (metadata.BrowserFormEvents — её же читает маршрутизатор событий), а не
	// из рукописной копии: копия отставала бы молча, ради этого #1151 и заведена.
	events := metadata.BrowserFormEvents()
	if len(events) == 0 {
		t.Fatal("metadata.BrowserFormEvents пуст — сторож руководства сверяет пустоту")
	}
	for _, event := range events {
		if !strings.Contains(section, "`"+string(event)+"`") {
			t.Errorf("раздел о событиях не упоминает %q, а браузер его отправляет", event)
		}
	}
}

// TestAIGuide_ТаблицаСобытийПоВидамЭлементов — таблица «Элемент | События»
// сверяется с платформенной попарно, а не по общему набору имён. Проверка
// набора ловит только выпавшее событие; пара «вид элемента → событие» врёт
// иначе: событие названо в чужой строке, и на своём элементе читатель его не
// ищет. Комментарий, который заявка #1151 признала обещающим больше проверки,
// обещал именно пары.
func TestAIGuide_ТаблицаСобытийПоВидамЭлементов(t *testing.T) {
	section := guideSection(t, generateAIGuide(""), "## События управляемых форм")
	documented := make(map[metadata.FormElementType]map[metadata.FormEventType]bool)
	for _, line := range strings.Split(section, "\n") {
		cells := markdownRowCells(line)
		if len(cells) != 2 {
			continue
		}
		var kinds []metadata.FormElementType
		for _, name := range backquoted(cells[0]) {
			if kind := metadata.FormElementType(name); metadata.IsKnownFormElementType(kind) {
				kinds = append(kinds, kind)
			}
		}
		if len(kinds) == 0 {
			continue // заголовок таблицы, разделитель, строка не про элемент
		}
		for _, kind := range kinds {
			if documented[kind] == nil {
				documented[kind] = make(map[metadata.FormEventType]bool)
			}
			for _, name := range backquoted(cells[1]) {
				if event := metadata.FormEventType(name); metadata.IsKnownFormEventType(event) {
					documented[kind][event] = true
				}
			}
		}
	}

	for _, kind := range metadata.KnownFormElementTypes() {
		allowed := metadata.BrowserFormEventsFor(kind)
		if len(allowed) == 0 {
			continue // вид ничего не отправляет — в таблице руководства ему не место
		}
		if documented[kind] == nil {
			t.Errorf("в таблице руководства нет строки про %q, а элемент отправляет %v", kind, allowed)
			continue
		}
		for _, event := range allowed {
			if !documented[kind][event] {
				t.Errorf("таблица руководства не приписывает %q событие %q, а платформа его разрешает", kind, event)
			}
		}
		for event := range documented[kind] {
			if !metadata.BrowserFormEventAllowed(kind, event) {
				t.Errorf("таблица руководства приписывает %q событие %q, которого платформа не разрешает", kind, event)
			}
		}
	}
}

// markdownRowCells разбирает строку markdown-таблицы на ячейки; не-строка
// таблицы даёт nil.
func markdownRowCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}
	return strings.Split(strings.Trim(line, "|"), "|")
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
