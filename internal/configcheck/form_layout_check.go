package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormLayout предупреждает о раскладке элемента формы, которую рантайм не
// применит: значение выравнивания не из словаря, размер вне разумных границ,
// ширина вместе с halign: stretch.
//
// Проверка появилась вместе с самим контрактом раскладки (#1185). До него
// width/height/halign/valign молча не действовали вообще — и именно так это и
// нашли: чтением шаблона рендера, а не сообщением инструмента. Класс ошибки тот
// же самый, поэтому опечатку в значении («halign: centre», «valign: middle» с
// лишним пробелом, «width: -10») называть обязан check, а не наблюдение
// «почему-то не сдвинулось».
//
// Предупреждение, а не ошибка: ключи годами принимались молча, и конфигурации с
// мусором в них уже написаны — валить им сборку задним числом значит наказать за
// прежнее молчание движка.
func CheckFormLayout(proj *project.Project) []Issue {
	var warns []Issue
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			label := formFileLabel(ent, form)
			walkFormElements(form.Elements, func(el *metadata.FormElement) {
				for _, d := range layoutDiagnostics(el) {
					warns = append(warns, Issue{
						File:         label,
						Object:       ent.Name,
						Kind:         "Управляемая форма",
						Code:         d.code,
						Message:      fmt.Sprintf("реквизит %q: %s", formElementName(el), d.msg),
						SuggestedFix: d.fix,
					})
				}
			})
		}
	}
	return warns
}

type layoutDiagnostic struct{ code, msg, fix string }

func layoutDiagnostics(el *metadata.FormElement) []layoutDiagnostic {
	var out []layoutDiagnostic

	if _, ok := metadata.NormalizeFormHAlign(el.HorizontalAlign); !ok {
		out = append(out, layoutDiagnostic{
			code: "form.layout-align",
			msg: fmt.Sprintf("halign: %q — значение не из словаря, выравнивание не применится",
				strings.TrimSpace(el.HorizontalAlign)),
			fix: "Допустимые значения: " + strings.Join(metadata.FormHAlignValues, ", ") + ".",
		})
	}
	if _, ok := metadata.NormalizeFormVAlign(el.VerticalAlign); !ok {
		out = append(out, layoutDiagnostic{
			code: "form.layout-align",
			msg: fmt.Sprintf("valign: %q — значение не из словаря, выравнивание не применится",
				strings.TrimSpace(el.VerticalAlign)),
			fix: "Допустимые значения: " + strings.Join(metadata.FormVAlignValues, ", ") + " (middle — синоним center).",
		})
	}
	out = append(out, layoutSizeDiagnostic("width", el.Width)...)
	out = append(out, layoutSizeDiagnostic("height", el.Height)...)

	// Виды, у которых собственного блока в форме нет: колонка — это описание
	// колонки таблицы, командную панель рантайм разворачивает в тулбар над
	// формой. Раскладку к ним приложить некуда, и сказать об этом обязаны мы:
	// иначе ключ снова «есть и молчит» — та самая ошибка, из-за которой заведена
	// #1185, только одним видом элемента ниже.
	if layoutKindWithoutBlock(el.Kind) && (el.Width != 0 || el.Height != 0 || el.HorizontalAlign != "" || el.VerticalAlign != "") {
		out = append(out, layoutDiagnostic{
			code: "form.layout-unsupported-kind",
			msg: fmt.Sprintf("раскладка (width/height/halign/valign) на элементе вида %q не применяется: своего блока в форме у него нет",
				el.Kind),
			fix: layoutKindFix(el.Kind),
		})
	}

	// stretch и явная ширина противоречат друг другу: рантайм оставляет
	// растяжение, а число молча пропадает. Сказать об этом дешевле, чем дать
	// человеку гадать, почему «ширина 200» не сработала.
	if h, ok := metadata.NormalizeFormHAlign(el.HorizontalAlign); ok && h == "stretch" && el.Width > 0 {
		out = append(out, layoutDiagnostic{
			code: "form.layout-size",
			msg:  fmt.Sprintf("width: %d задан вместе с halign: stretch — элемент растягивается на контейнер, ширина не действует", el.Width),
			fix:  "Оставьте либо halign: stretch, либо width.",
		})
	}
	return out
}

// layoutKindWithoutBlock — виды, которые managed-рендер не рисует отдельным
// блоком: `Колонка` описывает колонку таблицы, командные панели превращаются в
// тулбар формы (их кнопки рисуются в общем ряду).
func layoutKindWithoutBlock(kind metadata.FormElementType) bool {
	switch kind {
	case metadata.FormElementColumn, metadata.FormElementCommandBar, metadata.FormElementCommandBarButton:
		return true
	case "СтраницаКоманднаяПанель":
		return true
	default:
		return false
	}
}

func layoutKindFix(kind metadata.FormElementType) string {
	if kind == metadata.FormElementColumn {
		return "Ширина колонки табличной части сеткой не настраивается; уберите ключ, чтобы он не выглядел рабочим."
	}
	return "Задайте раскладку самому элементу (полю, кнопке, группе), а не командной панели."
}

func layoutSizeDiagnostic(key string, n int) []layoutDiagnostic {
	switch {
	case n < 0:
		return []layoutDiagnostic{{
			code: "form.layout-size",
			msg:  fmt.Sprintf("%s: %d — отрицательный размер, рантайм его игнорирует", key, n),
			fix:  fmt.Sprintf("Задайте %s в пикселях положительным числом или уберите ключ.", key),
		}}
	case n > metadata.FormLayoutMaxSize:
		return []layoutDiagnostic{{
			code: "form.layout-size",
			msg: fmt.Sprintf("%s: %d — больше потолка в %d px, рантайм его игнорирует; похоже на опечатку или на размер в чужих единицах",
				key, n, metadata.FormLayoutMaxSize),
			fix: fmt.Sprintf("Размер задаётся в ПИКСЕЛЯХ (не в условных единицах 1С): %s: 300.", key),
		}}
	}
	return nil
}
