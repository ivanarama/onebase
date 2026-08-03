package onec_forms

import (
	"bufio"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"os"
	"regexp"
	"strings"
)

// BSLProcedure — одна процедура/функция, выделенная из Module.bsl.
// Body — исходный текст «как есть», без разбора выражений. Тело DSL
// OneBase близко к BSL, но не идентично; имеет смысл копировать дословно
// и предупреждать о подозрительных конструкциях (см. ScanCompatibilityWarnings).
type BSLProcedure struct {
	Name      string   // имя процедуры
	Params    []string // параметры со скобками (raw): "Команда", "Знач Х : Число"
	Body      string   // содержимое между Процедура...КонецПроцедуры
	Directive string   // "&НаСервере" / "&НаКлиенте" / ... (либо "")
	Comments  []string // комментарии, расположенные непосредственно перед Процедурой
	IsFunc    bool     // true для Функция, false для Процедура
	IsExport  bool     // объявлена ли как Экспорт
	StartLine int      // 1-based номер строки, на которой стоит "Процедура"
}

// ReadBSL читает Module.bsl и возвращает список процедур + предупреждения
// по подозрительным конструкциям. Файл может отсутствовать — тогда
// возвращается (nil, nil, nil): форма может не иметь модуля.
func ReadBSL(path string) ([]*BSLProcedure, []Warning, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	defer oblog.CloseQuiet("onec_forms", "файл", f)

	var lines []string
	sc := bufio.NewScanner(f)
	// Module.bsl у УТ11 — большие; разрешаем строки до 1 МБ.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}

	procs := splitProcedures(lines)
	warns := ScanCompatibilityWarnings(procs)
	return procs, warns, nil
}

// procStartRegex — заголовок Процедуры/Функции. Учитываем регистронезависимость
// (BSL допускает любой регистр ключевых слов), русские буквы — обычные.
var procStartRegex = regexp.MustCompile(`(?i)^\s*(Процедура|Функция)\s+([\p{L}_][\p{L}\p{N}_]*)\s*\(([^)]*)\)\s*(Экспорт)?\s*$`)

// procEndRegex — закрывающие ключевые слова.
var procEndRegex = regexp.MustCompile(`(?i)^\s*(КонецПроцедуры|КонецФункции)\s*$`)

// directiveRegex — компиляционные директивы перед Процедурой/Функцией.
var directiveRegex = regexp.MustCompile(`^\s*(&\p{L}[\p{L}\p{N}_]*)`)

// splitProcedures режет файл на процедуры. State-машина с двумя состояниями:
// outside (вне процедуры) и inside (внутри).
func splitProcedures(lines []string) []*BSLProcedure {
	var procs []*BSLProcedure

	var (
		pendingDirective string   // последняя встретившаяся директива
		pendingComments  []string // последние комментарии перед процедурой
	)

	inside := false
	var cur *BSLProcedure
	var body strings.Builder

	flushOutsideBuffers := func() {
		// Если есть «висящие» директива/комментарии и встретилась не-процедура,
		// они привязываются к следующей процедуре — поэтому здесь ничего не очищаем.
	}
	_ = flushOutsideBuffers

	for i, raw := range lines {
		lineNo := i + 1
		stripped := strings.TrimSpace(raw)

		if !inside {
			if stripped == "" {
				// Пустая строка между процедурами: сбрасывает комментарии,
				// но не директиву (директива непосредственно перед Процедурой).
				pendingComments = pendingComments[:0]
				continue
			}
			if strings.HasPrefix(stripped, "//") {
				pendingComments = append(pendingComments, stripped)
				continue
			}
			if m := directiveRegex.FindStringSubmatch(stripped); m != nil && !procStartRegex.MatchString(stripped) {
				pendingDirective = m[1]
				continue
			}
			if m := procStartRegex.FindStringSubmatch(stripped); m != nil {
				cur = &BSLProcedure{
					Name:      m[2],
					Params:    parseParams(m[3]),
					Directive: pendingDirective,
					Comments:  append([]string(nil), pendingComments...),
					IsFunc:    strings.EqualFold(m[1], "Функция"),
					IsExport:  m[4] != "",
					StartLine: lineNo,
				}
				pendingDirective = ""
				pendingComments = pendingComments[:0]
				inside = true
				body.Reset()
				continue
			}
			// иначе — top-level код (например `Перем Х;`). Игнорируем для
			// целей CLI (они в форму не превращаются), но сохраняем директиву
			// чтобы не потерять её для следующей процедуры.
			if !strings.HasPrefix(stripped, "&") {
				pendingDirective = ""
			}
			pendingComments = pendingComments[:0]
			continue
		}

		// inside procedure
		if procEndRegex.MatchString(stripped) {
			cur.Body = strings.TrimRight(body.String(), "\n")
			procs = append(procs, cur)
			cur = nil
			inside = false
			continue
		}
		body.WriteString(raw)
		body.WriteByte('\n')
	}

	// Незакрытая процедура — сохраняем что есть с предупреждением через тело.
	if inside && cur != nil {
		cur.Body = strings.TrimRight(body.String(), "\n") + "\n// ПРЕДУПРЕЖДЕНИЕ: КонецПроцедуры не найден"
		procs = append(procs, cur)
	}

	return procs
}

// parseParams принимает строку из скобок "Парам1, Знач Парам2, Парам3 = Неопределено"
// и возвращает срез по запятым с TrimSpace.
func parseParams(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := splitTopLevelCommas(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitTopLevelCommas — разбивает строку по запятым, игнорируя те что внутри скобок/строк.
// Для параметров уровня BSL этого достаточно.
func splitTopLevelCommas(s string) []string {
	var out []string
	depth := 0
	start := 0
	inString := false
	for i, r := range s {
		switch r {
		case '(', '[':
			if !inString {
				depth++
			}
		case ')', ']':
			if !inString && depth > 0 {
				depth--
			}
		case '"':
			inString = !inString
		case ',':
			if depth == 0 && !inString {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// bslIncompatPatterns — список 20+ BSL-конструкций, у которых нет
// прямого аналога в DSL OneBase (или синтаксис отличается). При импорте
// тело процедуры копируется как есть, но пользователь предупреждается
// о необходимости ручной адаптации.
//
// Каждая запись — (фрагмент, человекочитаемое описание).
var bslIncompatPatterns = []struct {
	Frag string
	Note string
}{
	{"Новый Запрос", "конструктор Запрос — в OneBase используется конструктор запросов из DSL"},
	{"Новый ТаблицаЗначений", "ТаблицаЗначений — в OneBase используется коллекция (массив структур)"},
	{"Новый ХранилищеЗначения", "ХранилищеЗначения отсутствует в OneBase"},
	{"Новый ОписаниеОповещения", "ОписаниеОповещения — клиентский диалог, не поддерживается"},
	{"Новый Структура", "Новый Структура — в OneBase используется map/Структура DSL"},
	{"Новый Соответствие", "Новый Соответствие — в OneBase используется map"},
	{"Новый Массив", "Новый Массив — в OneBase используется массив DSL"},
	{"ПоказатьВопрос(", "клиентский диалог — не поддерживается в серверном DSL OneBase"},
	{"ПоказатьПредупреждение(", "клиентский диалог — не поддерживается"},
	{"ПоказатьВводЗначения(", "клиентский диалог — не поддерживается"},
	{"Состояние(", "Состояние — клиентский индикатор прогресса, нет аналога"},
	{"Сообщить(", "Сообщить — в OneBase используется ВыводСообщения() / Сообщение"},
	{"ВызватьИсключение", "ВызватьИсключение — в OneBase используется ВыброситьИсключение"},
	{"СтатусСообщения", "СтатусСообщения — нет аналога"},
	{"ОбщегоНазначения.", "общий модуль ОбщегоНазначения отсутствует"},
	{"ОбщегоНазначенияКлиент.", "общий модуль ОбщегоНазначенияКлиент отсутствует"},
	{"Метаданные.", "Метаданные — рефлексия по метаданным, требует адаптации"},
	{"Документы.", "менеджеры объектов — в OneBase есть Документы, но методы могут отличаться"},
	{"Справочники.", "менеджеры объектов — есть, но методы могут отличаться"},
	{"РегистрыСведений.", "менеджеры объектов — есть, но методы могут отличаться"},
	{"РегистрыНакопления.", "менеджеры объектов — есть, но методы могут отличаться"},
	{"НСтр(", "НСтр — функция локализации, нет аналога"},
	{"СтрШаблон(", "СтрШаблон — в OneBase используется конкатенация / fmt.Sprintf-аналог"},
	{"ЭтоАдресВременногоХранилища(", "временные хранилища — нет аналога"},
	{"ПолучитьИзВременногоХранилища(", "временные хранилища — нет аналога"},
	{"НачатьТранзакцию(", "Транзакции — в OneBase есть, но через Транзакция.Начать()"},
	{"ЗафиксироватьТранзакцию(", "Транзакции — в OneBase через Транзакция.Зафиксировать()"},
	{"ОтменитьТранзакцию(", "Транзакции — в OneBase через Транзакция.Отменить()"},
	{"ХранилищеОбщихНастроекСохранить", "хранилище общих настроек — нет аналога"},
	{"ХранилищеОбщихНастроекЗагрузить", "хранилище общих настроек — нет аналога"},
}

// ScanCompatibilityWarnings проходит по телам всех процедур и эмитит
// W040 для каждой найденной несовместимой BSL-конструкции. Один и тот же
// фрагмент в разных строках = несколько предупреждений (со ссылкой на строку).
func ScanCompatibilityWarnings(procs []*BSLProcedure) []Warning {
	var warns Warnings
	for _, p := range procs {
		if p.Body == "" {
			continue
		}
		bodyLines := strings.Split(p.Body, "\n")
		for li, line := range bodyLines {
			// игнорируем закомментированные строки
			stripped := strings.TrimSpace(line)
			if strings.HasPrefix(stripped, "//") {
				continue
			}
			for _, pat := range bslIncompatPatterns {
				if strings.Contains(line, pat.Frag) {
					warns.Add(Warning{
						Severity: SeverityWarn,
						Code:     W040_BSLNotInDSL,
						Element:  p.Name,
						Field:    pat.Frag,
						Line:     p.StartLine + li + 1,
						Message:  fmt.Sprintf("конструкция BSL %q без прямого аналога в DSL OneBase", pat.Frag),
						Suggest:  pat.Note,
					})
				}
			}
		}
	}
	return []Warning(warns)
}

// EmitDSLSource собирает .form.os из списка процедур. Каждая процедура
// предварена аннотацией `// @directive=...`, чтобы при экспорте обратно
// в BSL восстановить исходную директиву.
func EmitDSLSource(procs []*BSLProcedure) string {
	var sb strings.Builder
	sb.WriteString("// Этот файл сгенерирован конвертером onebase forms convert-from-1c.\n")
	sb.WriteString("// Тела процедур скопированы как есть из Module.bsl — DSL OneBase близок к BSL,\n")
	sb.WriteString("// но не идентичен; ищите // TODO-метки и предупреждения W040 в отчёте импорта.\n\n")
	for i, p := range procs {
		if i > 0 {
			sb.WriteByte('\n')
		}
		for _, c := range p.Comments {
			sb.WriteString(c)
			sb.WriteByte('\n')
		}
		if p.Directive != "" {
			sb.WriteString("// @directive=")
			sb.WriteString(p.Directive)
			sb.WriteByte('\n')
		}
		kw := "Процедура"
		closeKw := "КонецПроцедуры"
		if p.IsFunc {
			kw = "Функция"
			closeKw = "КонецФункции"
		}
		sb.WriteString(kw)
		sb.WriteByte(' ')
		sb.WriteString(p.Name)
		sb.WriteByte('(')
		sb.WriteString(strings.Join(p.Params, ", "))
		sb.WriteByte(')')
		if p.IsExport {
			sb.WriteString(" Экспорт")
		}
		sb.WriteByte('\n')
		sb.WriteString(p.Body)
		if !strings.HasSuffix(p.Body, "\n") {
			sb.WriteByte('\n')
		}
		sb.WriteString(closeKw)
		sb.WriteByte('\n')
	}
	return sb.String()
}
