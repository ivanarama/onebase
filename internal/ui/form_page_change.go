package ui

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// addPageChangeContext кладёт в обработчик события ПриСменеСтраницы имя
// открытой страницы — переменную ТекущаяСтраница (параметр ТекущаяСтраница
// ПриСменеСтраницы панели 1С). Имя приходит из браузера, поэтому принимается
// только страница ТОГО ЖЕ набора СтраницыФормы: чужое или выдуманное имя —
// отказ, а не произвольная строка в обработчике.
func addPageChangeContext(eventName, page string, target browserFormEventTarget, vars map[string]any) error {
	if eventName != string(metadata.FormEventOnPageChange) {
		return nil
	}
	if target.element == nil || target.element.Kind != metadata.FormElementPages {
		return fmt.Errorf("событие %q отправляет только набор страниц", eventName)
	}
	page = strings.TrimSpace(page)
	if page != "" {
		for _, child := range target.element.Children {
			if child != nil && child.Kind == metadata.FormElementPage && child.Name == page {
				vars["ТекущаяСтраница"] = page
				vars["CurrentPage"] = page
				return nil
			}
		}
	}
	return fmt.Errorf("страница %q не входит в набор страниц %q", page, target.element.Name)
}
