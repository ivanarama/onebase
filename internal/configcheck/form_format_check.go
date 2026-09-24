package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormFieldFormat возвращает НЕблокирующие предупреждения о реквизитах форм,
// которые задают format/display_format, не применяемый рантаймом. Главный случай —
// kind: ПолеДаты (issue #219): нативный <input type="date"> всегда показывает дату
// по локали браузера (в ru — дд.ММ.гггг) и хранит значение в ISO; произвольный
// формат строкой задать нельзя. Раньше такой ключ молча проглатывался (yaml.v3
// игнорирует неизвестные поля, а unvalidated-key печатается лишь под --lint),
// создавая «ложную уверенность»: check проходил, а формат не работал. Теперь поля
// распознаются загрузчиком, а эта проверка ЯВНО и по умолчанию сообщает, что они
// ничего не делают.
func CheckFormFieldFormat(proj *project.Project) []Issue {
	var warns []Issue
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			label := formFileLabel(ent, form)
			walkFormElements(form.Elements, func(el *metadata.FormElement) {
				if el.Format == "" && el.DisplayFormat == "" {
					return
				}
				attr := "format"
				if el.Format == "" {
					attr = "display_format"
				}
				var msg, fix string
				if el.Kind == metadata.FormElementDatePicker {
					msg = fmt.Sprintf("ПолеДаты %q задаёт %s, но он не применяется: нативный выбор даты "+
						"показывает её по локали браузера (в ru — дд.ММ.гггг), а значение всегда хранится "+
						"в ISO (issue #219)", formElementName(el), attr)
					fix = "Уберите format/display_format у ПолеДаты — для ru-локали дата и так показывается " +
						"как дд.ММ.гггг; произвольный формат нативный контрол не поддерживает."
				} else {
					msg = fmt.Sprintf("реквизит %q (%s) задаёт %s, но рантайм его не применяет",
						formElementName(el), el.Kind, attr)
					fix = "Уберите format/display_format: платформа сейчас не форматирует поля формы по этому атрибуту."
				}
				warns = append(warns, Issue{
					File:         label,
					Object:       ent.Name,
					Kind:         "Управляемая форма",
					Code:         "form.format-ignored",
					Message:      msg,
					SuggestedFix: fix,
				})
			})
		}
	}
	return warns
}

// formFileLabel возвращает локатор формы для предупреждения check.
//
// Основной источник — путь файла, из которого форма прочитана. Синтезировать
// его из `form.Name` нельзя: имя формы и имя файла совпадать не обязаны, и для
// `name: ФормаОбъекта` в `объекта.form.yaml` получался путь
// `forms/инвентаризация/ФормаОбъекта.form.yaml` — файла с таким именем на диске
// нет, и локатор не открывался (#1356). `onebase check` — основной инструмент
// отладки конфигурации, и ненажимаемый путь обесценивает вывод ровно там, где
// им пользуются.
func formFileLabel(ent *metadata.Entity, form *metadata.FormModule) string {
	return formLabelOrSynthetic(ent.Name, form)
}

// procFormFileLabel — то же для формы обработки: формы обработок читает тот же
// загрузчик и кладёт в тот же каталог `forms/<имя>/`.
func procFormFileLabel(procName string, form *metadata.FormModule) string {
	return formLabelOrSynthetic(procName, form)
}

// formLabelOrSynthetic предпочитает фактический путь файла, а когда файла нет
// вовсе — синтезирует прежнее имя по соглашению.
//
// Пустой SourcePath — это не сбой: так выглядят автоформы из `src/*.form.os` и
// формы, собранные в памяти (редактором, тестами). Открывать там нечего, и
// прежний синтезированный вид остаётся лучшим, что можно показать.
func formLabelOrSynthetic(ownerName string, form *metadata.FormModule) string {
	if form == nil {
		return "forms/" + strings.ToLower(ownerName)
	}
	if form.SourcePath != "" {
		return form.SourcePath
	}
	name := form.Name
	if name == "" {
		name = "объекта"
	}
	return "forms/" + strings.ToLower(ownerName) + "/" + name + ".form.yaml"
}

// formElementName возвращает осмысленное имя реквизита для сообщения: имя, иначе
// data_path, иначе вид элемента.
func formElementName(el *metadata.FormElement) string {
	if el.Name != "" {
		return el.Name
	}
	if el.DataPath != "" {
		return el.DataPath
	}
	return string(el.Kind)
}

// walkFormElements обходит дерево элементов формы (включая вложенные children).
func walkFormElements(elements []*metadata.FormElement, fn func(*metadata.FormElement)) {
	for _, el := range elements {
		if el == nil {
			continue
		}
		fn(el)
		walkFormElements(el.Children, fn)
	}
}
