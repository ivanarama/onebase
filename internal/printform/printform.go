package printform

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// RichTextFields возвращает множество имён richtext-полей сущности (в нижнем
// регистре) для RenderContext.RichTextFields — печатная форма/предпросмотр
// выводят такие поля как HTML (форматирование+картинки), план 65 этап 3.
// richtext допустим только в реквизитах шапки (валидация запрещает его в ТЧ),
// поэтому ТЧ не обходим. Возвращает nil, если richtext-полей нет.
func RichTextFields(entity *metadata.Entity) map[string]bool {
	if entity == nil {
		return nil
	}
	var set map[string]bool
	for _, f := range entity.Fields {
		if metadata.IsRichText(f.Type) {
			if set == nil {
				set = make(map[string]bool)
			}
			set[strings.ToLower(f.Name)] = true
		}
	}
	return set
}

// PrintForm describes a declarative print form loaded from YAML.
type PrintForm struct {
	Name     string        `yaml:"name"`
	Document string        `yaml:"document"`
	Title    string        `yaml:"title"`
	Header   string        `yaml:"header"`
	Table    *TableSection `yaml:"table"`
	Footer   string        `yaml:"footer"`

	// External помечает форму, пришедшую из внешнего контура (таблица
	// _ext_printforms), а не из конфигурации проекта. Заполняется
	// программно при загрузке; в YAML не сериализуется. UI показывает у
	// таких форм пометку «(внешняя)».
	External bool `yaml:"-"`
}

type TableSection struct {
	Source  string      `yaml:"source"`
	Columns []Column    `yaml:"columns"`
	Totals  []TotalSpec `yaml:"totals"`
}

type Column struct {
	Field  string `yaml:"field"`
	Label  string `yaml:"label"`
	Width  string `yaml:"width"`
	Align  string `yaml:"align"`
	Format string `yaml:"format"`
}

type TotalSpec struct {
	Field string `yaml:"field"`
	Sum   bool   `yaml:"sum"`
	Label string `yaml:"label"`
}

// RenderContext holds all data needed to render a print form.
type RenderContext struct {
	EntityName string                      // имя сущности; разрешает {{Сущность.Поле}} для корневой записи
	Document   map[string]any              // fields of the document/catalog record
	TableParts map[string][]map[string]any // table part name → rows
	Constants  map[string]any              // global constants
	Refs       map[string]map[string]any   // field name → expanded reference fields

	// RichTextFields — множество имён полей сущности типа richtext (в нижнем
	// регистре), план 65 этап 3. Заполняется loadPrintContext из метаданных
	// сущности. Когда параметр ячейки привязан к richtext-полю, BuildSheet
	// кладёт санитизированный HTML в Cell.RichHTML (вместо экранируемого Text),
	// чтобы печатная форма выводила форматирование и картинки. nil/пусто →
	// поведение прежнее (всё идёт в Text).
	RichTextFields map[string]bool

	// sumCache мемоизирует Итог.<ТЧ>.<Поле> в пределах одного рендера: при
	// использовании Итог внутри repeat-строки наивный sumColumn пересчитывал бы
	// сумму на каждой строке (O(N²)). Лениво инициализируется в sumColumn;
	// контекст создаётся заново на каждый рендер, поэтому глобального состояния нет.
	sumCache map[string]float64
}

// isRichTextField сообщает, является ли поле expr (после отбрасывания формата)
// richtext-полем сущности. Регистронезависимо. Только простое имя поля документа
// считается richtext — выражения со ссылкой (Поле.ПодПоле), Итог., Константы.,
// @row к richtext не относятся.
func (ctx *RenderContext) isRichTextField(expr string) bool {
	if ctx == nil || len(ctx.RichTextFields) == 0 {
		return false
	}
	field, _ := splitExprFormat(expr)
	if field == "" || strings.ContainsAny(field, ".|") || strings.HasPrefix(field, "@") {
		return false
	}
	return ctx.RichTextFields[strings.ToLower(field)]
}
