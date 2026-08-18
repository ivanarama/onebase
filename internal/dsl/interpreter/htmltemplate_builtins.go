package interpreter

// HTML-шаблоны в DSL (план 125).
//
// До этого любой HTML, который конфигурация отдаёт наружу (письмо, тело ответа
// HTTP-сервиса, страница сайта), собирался конкатенацией строк — без единой
// функции экранирования в языке. Наименование товара с «<» ломало вёрстку, а с
// «<script>» исполнялось в браузере получателя.
//
// Основа — html/template: экранирование КОНТЕКСТНОЕ (в тексте узла, в значении
// атрибута, в URL и внутри <script> одно и то же значение экранируется
// по-разному). Именно поэтому отдельной функции «ЭкранироватьHTML» здесь нет:
// она провоцирует ручное экранирование не в том контексте, что хуже, чем его
// отсутствие.

import (
	"fmt"
	"html/template"
	"strings"
	"text/template/parse"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/richtext"
)

const (
	// maxTemplateSourceLen — предел размера текста шаблона в байтах.
	maxTemplateSourceLen = 1 << 20 // 1 МиБ
	// maxTemplateOutputLen — предел размера результата. Защита от «range по
	// 100 000 строк», который иначе съест память процесса.
	maxTemplateOutputLen = 16 << 20 // 16 МиБ
)

func init() {
	builtins["безопасныйhtml"] = safeHTMLBuiltin
	builtins["safehtml"] = safeHTMLBuiltin
}

// БезопасныйHTML(Строка) — пропускает строку через санитайзер richtext и
// помечает результат как готовый HTML: теги остаются тегами, <script>,
// onerror= и javascript: вырезаются.
//
// Санитизация здесь обязательна. Прямая обёртка template.HTML без очистки была
// бы дырой с успокаивающим названием: «БезопасныйHTML(ПолеИзФормы)» напишет
// каждый, и это ровно XSS. «Безопасный» значит проверенный, а не объявленный.
func safeHTMLBuiltin(args []any, _ string, _ int) (any, error) {
	return template.HTML(richtext.Sanitize(strArg(args, 0))), nil //nolint:gosec // G203: значение прошло санитайзер richtext — в этом и смысл функции
}

// dslHTMLTemplate — разобранный HTML-шаблон. Неизменяемый объект: текст
// разбирается один раз в конструкторе.
type dslHTMLTemplate struct {
	tmpl *template.Template
	src  string
}

// NewHTMLTemplateObject — конструктор «Новый ШаблонHTML(Текст)».
func NewHTMLTemplateObject(args []any) any {
	src := strArg(args, 0)
	if len(src) > maxTemplateSourceLen {
		RaiseUserError(fmt.Sprintf("ШаблонHTML: текст шаблона больше %d байт", maxTemplateSourceLen))
	}
	tmpl, err := template.New("dsl").
		Funcs(htmlTemplateFuncs()).
		// Пропуск необязательного поля не должен ронять всю страницу: без этого
		// «{{.НетТакого}}» даёт ошибку исполнения. Размен осознанный — опечатка
		// в имени поля тоже даст пустоту, поэтому шаблон стоит держать под
		// тестом конфигурации.
		Option("missingkey=zero").
		Parse(src)
	if err != nil {
		RaiseUserError("ШаблонHTML: ошибка разбора шаблона: " + err.Error())
	}
	lowerFieldNames(tmpl)
	return &dslHTMLTemplate{tmpl: tmpl, src: src}
}

// lowerFieldNames приводит имена полей в дереве шаблона к нижнему регистру.
//
// Структура DSL хранит ключи в нижнем регистре (Struct.Set), а конфигуратор
// пишет «{{.Наименование}}» — точного ключа в данных нет, и поле молча
// оказалось бы пустым. Правка идёт по дереву разбора, а не текстовой заменой:
// замена по тексту задела бы строковые литералы внутри шаблона.
func lowerFieldNames(t *template.Template) {
	for _, tm := range t.Templates() {
		if tm.Tree != nil && tm.Tree.Root != nil {
			lowerFieldNamesNode(tm.Tree.Root)
		}
	}
}

func lowerFieldNamesNode(n parse.Node) {
	switch v := n.(type) {
	case nil:
		return
	case *parse.FieldNode:
		for i := range v.Ident {
			v.Ident[i] = strings.ToLower(v.Ident[i])
		}
	case *parse.ChainNode:
		for i := range v.Field {
			v.Field[i] = strings.ToLower(v.Field[i])
		}
		lowerFieldNamesNode(v.Node)
	case *parse.VariableNode:
		// «{{$стр.Имя}}»: Ident[0] — имя самой переменной, дальше — поля.
		for i := 1; i < len(v.Ident); i++ {
			v.Ident[i] = strings.ToLower(v.Ident[i])
		}
	case *parse.ListNode:
		if v == nil {
			return
		}
		for _, c := range v.Nodes {
			lowerFieldNamesNode(c)
		}
	case *parse.ActionNode:
		lowerFieldNamesNode(v.Pipe)
	case *parse.PipeNode:
		if v == nil {
			return
		}
		for _, c := range v.Cmds {
			lowerFieldNamesNode(c)
		}
	case *parse.CommandNode:
		for _, a := range v.Args {
			lowerFieldNamesNode(a)
		}
	case *parse.IfNode:
		lowerBranch(&v.BranchNode)
	case *parse.RangeNode:
		lowerBranch(&v.BranchNode)
	case *parse.WithNode:
		lowerBranch(&v.BranchNode)
	case *parse.TemplateNode:
		lowerFieldNamesNode(v.Pipe)
	}
}

func lowerBranch(b *parse.BranchNode) {
	lowerFieldNamesNode(b.Pipe)
	lowerFieldNamesNode(b.List)
	lowerFieldNamesNode(b.ElseList)
}

func (t *dslHTMLTemplate) String() string   { return "ШаблонHTML" }
func (t *dslHTMLTemplate) TypeName() string { return "ШаблонHTML" }

func (t *dslHTMLTemplate) Get(field string) any {
	switch strings.ToLower(field) {
	case "текст", "source":
		return t.src
	}
	return nil
}

// Set — no-op: шаблон после разбора не меняется.
func (t *dslHTMLTemplate) Set(string, any) {}

func (t *dslHTMLTemplate) CallMethod(name string, args []any) any {
	switch strings.ToLower(name) {
	case "заполнить", "fill":
		var data any
		if len(args) > 0 {
			data = dslValueToTemplateData(args[0])
		}
		w := &limitedBuilder{limit: maxTemplateOutputLen}
		if err := t.tmpl.Execute(w, data); err != nil {
			// Частичный вывод не отдаём: полустраница, выглядящая рабочей,
			// хуже честной ошибки.
			RaiseUserError("ШаблонHTML.Заполнить: " + err.Error())
		}
		return w.b.String()
	}
	// Молчаливое Неопределено спрятало бы опечатку в имени метода — падаем,
	// как остальные объекты интерпретатора.
	RaiseUserError("ШаблонHTML: неизвестный метод «" + name + "»")
	return nil
}

// limitedBuilder прерывает вывод, когда результат превысил предел.
type limitedBuilder struct {
	b     strings.Builder
	limit int
}

func (w *limitedBuilder) Write(p []byte) (int, error) {
	if w.b.Len()+len(p) > w.limit {
		return 0, fmt.Errorf("результат больше %d байт", w.limit)
	}
	return w.b.Write(p)
}

// htmlTemplateFuncs — фиксированный набор функций шаблона. Произвольные
// функции конфигурации сюда не пробрасываются: шаблон остаётся вёрсткой, а не
// вторым языком внутри языка.
func htmlTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"дата":   templateFuncDate,
		"date":   templateFuncDate,
		"число":  templateFuncNumber,
		"number": templateFuncNumber,
	}
}

// дата(Значение, Формат) — формат по образцу Go («02.01.2006 15:04»).
//
// Значение приходит не только объектом даты: реквизит типа date на SQLite
// читается строкой. Возвращать при этом пустоту — худший вариант: в вёрстке
// появляется пустой <time>, и причину видно только по исходнику шаблона.
// Поэтому строка сперва разбирается по типовым форматам, а если не разобралась
// — отдаётся как есть.
func templateFuncDate(v any, layout string) string {
	if layout == "" {
		layout = "02.01.2006"
	}
	switch t := v.(type) {
	case nil:
		return ""
	case time.Time:
		return t.Format(layout)
	case string:
		if parsed, ok := parseTemplateDate(t); ok {
			return parsed.Format(layout)
		}
		return t
	}
	return ""
}

// parseTemplateDate разбирает строковое представление даты. Порядок форматов —
// от самого частого (хранение в базе) к вводу пользователя.
func parseTemplateDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
		"02.01.2006 15:04:05",
		"02.01.2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// число(Значение, ЗнаковПослеЗапятой) — фиксированное число знаков, разделитель
// разрядов — пробел: тот же, что у платформенного Формат() по умолчанию
// (builtins_ext.go, sep := " "), чтобы числа в письме и в отчёте выглядели
// одинаково.
func templateFuncNumber(v any, digits int) string {
	var d decimal.Decimal
	switch n := v.(type) {
	case decimal.Decimal:
		d = n
	case float64:
		d = decimal.NewFromFloat(n)
	case int:
		d = decimal.NewFromInt(int64(n))
	case int64:
		d = decimal.NewFromInt(n)
	default:
		return ""
	}
	if digits < 0 {
		digits = 0
	}
	s := d.StringFixed(int32(digits)) //nolint:gosec // G115: digits ограничено сверху вызывающим шаблоном, отрицательное приведено к 0
	return groupThousands(s)
}

// groupThousands расставляет разделители разрядов в уже отформатированном
// числе («1234.50» → «1 234.50»).
func groupThousands(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(" ")
		}
		b.WriteRune(c)
	}
	out := sign + b.String()
	if hasFrac {
		out += "." + frac
	}
	return out
}
