package metadata

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// labelNames — имена реквизитов, которыми объект принято представлять, в
// порядке предпочтения. «Код» сюда не входит намеренно: это идентификатор, а не
// название, и показывать его вместо наименования — то же, что показывать UUID.
var labelNames = []string{"Наименование", "Description", "Имя", "Name"}

// LabelFields возвращает реквизиты-кандидаты на представление объекта, в
// порядке предпочтения: канонические имена → «Номер» → прочие строковые, причём
// «Код» отодвигается в самый конец.
//
// Выбор по ИМЕНИ, а не по позиции — иначе представление зависит от порядка
// реквизитов в YAML, а порядок задают пользователь и конвертер выгрузки 1С.
// Конвертер кладёт «Код» ПЕРЕД «Наименованием», поэтому у импортированных из 1С
// конфигураций в пикерах ссылок, списках и глобальном поиске показывался код —
// вопреки 1С, где основное представление по умолчанию именно наименование
// (план 117, решение №3).
func LabelFields(e *Entity) []Field {
	if e == nil {
		return nil
	}
	// Явное указание перекрывает правило по именам целиком: если автор сказал
	// «представляй артикулом», подставлять следом наименование нельзя — это
	// вернуло бы ровно то поведение, от которого он уходит. Запасные варианты
	// задаются списком, и порядок в нём — его.
	if len(e.Presentation) > 0 {
		var out []Field
		seen := make(map[string]bool, len(e.Presentation))
		for _, name := range e.Presentation {
			for _, f := range e.Fields {
				if strings.EqualFold(f.Name, name) && f.Type == FieldTypeString {
					key := strings.ToLower(f.Name)
					if seen[key] {
						break
					}
					out = append(out, f)
					seen[key] = true
					break
				}
			}
		}
		if len(out) > 0 {
			return out
		}
		// Пустой результат означает, что все указанные реквизиты не найдены или
		// не строковые. Это ловит check; в рантайме откатываемся на правило по
		// именам, чтобы объект не остался вовсе без представления.
	}
	var named, numbers, rest, codes []Field
	for _, f := range e.Fields {
		if f.Type != FieldTypeString {
			continue
		}
		switch {
		case matchesAny(f.Name, labelNames):
			named = append(named, f)
		case strings.EqualFold(f.Name, "Номер"):
			numbers = append(numbers, f)
		case strings.EqualFold(f.Name, "Код"):
			codes = append(codes, f)
		default:
			rest = append(rest, f)
		}
	}
	// Внутри named порядок предпочтения задаёт labelNames, а не YAML.
	sortByPreference(named, labelNames)
	out := make([]Field, 0, len(named)+len(numbers)+len(rest)+len(codes))
	out = append(out, named...)
	out = append(out, numbers...)
	out = append(out, rest...)
	return append(out, codes...)
}

func matchesAny(name string, cands []string) bool {
	for _, c := range cands {
		if strings.EqualFold(name, c) {
			return true
		}
	}
	return false
}

func sortByPreference(fields []Field, order []string) {
	rank := func(name string) int {
		for i, c := range order {
			if strings.EqualFold(name, c) {
				return i
			}
		}
		return len(order)
	}
	sort.SliceStable(fields, func(i, j int) bool { return rank(fields[i].Name) < rank(fields[j].Name) })
}

// RowLabel строит представление объекта по строке данных — то, что видит
// пользователь вместо UUID: в списках, в пикерах ссылок и в выдаче глобального
// поиска. Функция общая для UI и REST специально: подпись одного и того же
// объекта не должна зависеть от того, через какую точку входа его показали.
//
// Порядок: первый непустой реквизит из LabelFields → для документа
// синтетическая подпись (Номер, иначе даты и числа) → в крайнем случае id.
func RowLabel(row map[string]any, e *Entity) string {
	if e == nil {
		return fmt.Sprintf("%v", row["id"])
	}
	// Пустое значение пропускается только при явном presentation. Список из
	// нескольких реквизитов иначе не имел бы смысла: в строке данных поле почти
	// всегда присутствует, и запасной вариант никогда бы не сработал — автор
	// написал «Артикул, иначе Наименование», а получил бы пустую подпись.
	//
	// На правило по именам это НЕ распространяется: там пустая строка сегодня
	// показывается как есть, и молча заменить её кодом значило бы поменять
	// подписи в чужих конфигурациях, которые ключ не просили (#846).
	skipEmpty := len(e.Presentation) > 0
	for _, f := range LabelFields(e) {
		v, ok := row[f.Name]
		if !ok || v == nil {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if skipEmpty && strings.TrimSpace(s) == "" {
			continue
		}
		return s
	}
	// Нет строкового реквизита — для документа не проваливаемся сразу в сырой
	// UUID (issue #361). Представление ссылки в языке запросов для документов —
	// Номер; а документы из одних reference/date/number (типовая проводка,
	// начисление) вообще не имеют текстового реквизита и раньше показывались в
	// reference-пикере голыми UUID. Синтезируем читаемую подпись без доп. запросов.
	if e.Kind == KindDocument {
		if lbl := documentFallbackLabel(row, e); lbl != "" {
			return lbl
		}
	}
	return fmt.Sprintf("%v", row["id"])
}

// documentFallbackLabel строит подпись документа, у которого нет строкового
// реквизита: сначала Номер (если такое поле есть), иначе — компактный синтез из
// значений полей-дат и чисел в порядке объявления. Ссылочные поля не включаются:
// в строке они лежат сырыми UUID и без доп. чтения не читаемы.
func documentFallbackLabel(row map[string]any, e *Entity) string {
	for _, k := range []string{"Номер", "номер"} {
		if v, ok := row[k]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" {
				return s
			}
		}
	}
	var parts []string
	for _, f := range e.Fields {
		if f.Type != FieldTypeDate && f.Type != FieldTypeNumber {
			continue
		}
		v, ok := row[f.Name]
		if !ok || v == nil {
			continue
		}
		if s := formatLabelValue(v); s != "" {
			parts = append(parts, s)
		}
		if len(parts) >= 3 { // достаточно для узнаваемости, не раздуваем подпись
			break
		}
	}
	return strings.Join(parts, " · ")
}

// formatLabelValue форматирует значение поля для синтетической подписи: даты — в
// локальном формате, всё остальное (число может прийти строкой на SQLite) — как
// есть, без лишних пробелов.
func formatLabelValue(v any) string {
	if t, ok := v.(time.Time); ok {
		if t.IsZero() {
			return ""
		}
		return t.Format("02.01.2006")
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}
