package onec_forms

import (
	"regexp"
	"strings"
)

// EmitBSLFromDSL принимает текст .form.os (DSL OneBase, который мы ранее
// сгенерировали из BSL через EmitDSLSource или который был отредактирован
// пользователем) и пишет обратно Module.bsl для 1С.
//
// Ключевые операции:
//  1. Восстановление директив &НаСервере / &НаКлиенте из аннотаций
//     `// @directive=...` над процедурой. Если аннотации нет — добавляется
//     &НаСервере по умолчанию (это безопасно: на стороне 1С большинство
//     серверных операций работает; warning W042 информирует пользователя).
//  2. Тело процедуры копируется как есть — DSL OneBase близок к BSL,
//     но не идентичен. ScanDSLNotInBSL ищет 10+ известных OneBase-конструкций
//     без аналога в BSL (Транзакция.Начать, JSON.Decode, HTTP.GET, ...)
//     и эмитит W041 с указанием строки.
//
// Возвращает (bslSource, warnings).
func EmitBSLFromDSL(dslSource string) (string, []Warning) {
	var warns Warnings
	var sb strings.Builder

	dslLines := strings.Split(dslSource, "\n")
	// Состояние state-машины. Вне процедуры мы накапливаем:
	//   pendingDirective — последняя встретившаяся `// @directive=…`,
	//   pendingComments  — последовательность // -комментариев,
	//   pendingBlanks    — пустые строки (для сохранения формата).
	// Эти три буфера принадлежат «следующему ближайшему» заголовку процедуры.
	// При встрече top-level кода (Перем …) буфера выводятся как есть и сбрасываются.
	var pendingDirective string
	var pendingComments []string
	pendingBlanks := 0
	inside := false
	procName := ""

	// emitPreface печатает накопленные комментарии + директиву ровно один раз.
	// Используется перед заголовком процедуры. Пустые строки выводятся,
	// сохраняя визуальные разрывы между блоками.
	emitPreface := func(defaultDirective string, defaultLine int) {
		for i := 0; i < pendingBlanks; i++ {
			sb.WriteByte('\n')
		}
		pendingBlanks = 0
		for _, c := range pendingComments {
			sb.WriteString(c)
			sb.WriteByte('\n')
		}
		pendingComments = pendingComments[:0]
		if pendingDirective != "" {
			sb.WriteString(pendingDirective)
			sb.WriteByte('\n')
		} else if defaultDirective != "" {
			sb.WriteString(defaultDirective)
			sb.WriteByte('\n')
			warns.Add(Warning{
				Severity: SeverityInfo, Code: W042_DirectiveMissing,
				Element: procName, Line: defaultLine,
				Message: "директива отсутствует, использована " + defaultDirective + " по умолчанию",
			})
		}
		pendingDirective = ""
	}

	// flushAsPlain — выкинуть всё, что было накоплено, как обычный текст
	// (без преобразования директивы). Используется когда встретили top-level
	// код, не являющийся заголовком процедуры.
	flushAsPlain := func() {
		for i := 0; i < pendingBlanks; i++ {
			sb.WriteByte('\n')
		}
		pendingBlanks = 0
		for _, c := range pendingComments {
			sb.WriteString(c)
			sb.WriteByte('\n')
		}
		pendingComments = pendingComments[:0]
		if pendingDirective != "" {
			sb.WriteString(pendingDirective)
			sb.WriteByte('\n')
			pendingDirective = ""
		}
	}

	for i, raw := range dslLines {
		lineNo := i + 1
		stripped := strings.TrimSpace(raw)

		if !inside {
			// Распознать // @directive=...
			if m := dirAnnotationRegex.FindStringSubmatch(stripped); m != nil {
				pendingDirective = m[1]
				continue
			}
			// Пустые строки — копим (визуальные разрывы между блоками).
			if stripped == "" {
				pendingBlanks++
				continue
			}
			// Комментарии — копим до Процедура/Функция.
			if strings.HasPrefix(stripped, "//") {
				pendingComments = append(pendingComments, raw)
				continue
			}
			// Заголовок процедуры?
			if m := procStartRegex.FindStringSubmatch(stripped); m != nil {
				procName = m[2]
				emitPreface("&НаСервере", lineNo)
				sb.WriteString(raw)
				sb.WriteByte('\n')
				inside = true
				continue
			}
			// Прочее (Перем, top-level выражения) — переносим как есть,
			// предварительно сбросив накопленное.
			flushAsPlain()
			sb.WriteString(raw)
			sb.WriteByte('\n')
			continue
		}

		// inside procedure
		if procEndRegex.MatchString(stripped) {
			sb.WriteString(raw)
			sb.WriteByte('\n')
			inside = false
			procName = ""
			continue
		}
		// Тело — копируем + сканируем на OneBase-only конструкции.
		sb.WriteString(raw)
		sb.WriteByte('\n')
		if !strings.HasPrefix(stripped, "//") {
			for _, pat := range dslNotInBSLPatterns {
				if strings.Contains(raw, pat.Frag) {
					warns.Add(Warning{
						Severity: SeverityWarn,
						Code:     W041_DSLNotInBSL,
						Element:  procName,
						Field:    pat.Frag,
						Line:     lineNo,
						Message:  "конструкция OneBase без прямого аналога в BSL",
						Suggest:  pat.Note,
					})
				}
			}
		}
	}

	// Хвостовые комментарии и директива «висят» — выпишем их как есть.
	flushAsPlain()

	return sb.String(), []Warning(warns)
}

// dirAnnotationRegex — синтаксис аннотации директивы: `// @directive=&Имя`.
var dirAnnotationRegex = regexp.MustCompile(`(?i)^//\s*@directive\s*=\s*(&[\p{L}_][\p{L}\p{N}_]*)\s*$`)

// dslNotInBSLPatterns — OneBase-конструкции без прямого аналога в BSL.
// Список значительно меньше bslIncompatPatterns: OneBase молодой проект,
// и большая часть его DSL — стандартный BSL.
var dslNotInBSLPatterns = []struct {
	Frag string
	Note string
}{
	{"Транзакция.Начать", "BSL использует НачатьТранзакцию()"},
	{"Транзакция.Зафиксировать", "BSL использует ЗафиксироватьТранзакцию()"},
	{"Транзакция.Отменить", "BSL использует ОтменитьТранзакцию()"},
	{"JSON.Decode", "BSL: ПрочитатьJSON(ЧтениеJSON, …) с предварительным открытием потока"},
	{"JSON.Encode", "BSL: ЗаписатьJSON(ЗаписьJSON, …)"},
	{"HTTP.GET", "BSL: HTTPСоединение + ВызватьМетод"},
	{"HTTP.POST", "BSL: HTTPСоединение + ВызватьМетод"},
	{"ВыброситьИсключение", "BSL: ВызватьИсключение"},
	{"ВыводСообщения", "BSL: Сообщить(…)"},
	{"ТекущийПользователь", "BSL: ПараметрыСеанса.ТекущийПользователь или ПользователиИнформационнойБазы.ТекущийПользователь()"},
	{"ПолучитьИзБД", "BSL: специфический менеджер объектов"},
}
