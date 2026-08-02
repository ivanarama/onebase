package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// unplacedCommands исключает команды, уже размещённые вручную элементом kind:Кнопка
// (по совпадению Action с обработчиком «Нажатие»), и возвращает только те, для
// которых нужна автоматическая командная панель (фикс A). Так формы, где команда
// продублирована кнопкой (examples/trade), не получают двойных кнопок.
func TestUnplacedCommands(t *testing.T) {
	form := &metadata.FormModule{
		Commands: []*metadata.FormCommand{
			{Name: "Размещённая", Action: "ПроцA"},
			{Name: "Неразмещённая", Action: "ПроцB"},
			{Name: "БезAction"},
		},
		Elements: []*metadata.FormElement{
			{
				Kind:     metadata.FormElementType("Кнопка"),
				Name:     "КнА",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ПроцA"},
			},
		},
	}
	got := unplacedCommands(form)
	if len(got) != 1 || got[0].Name != "Неразмещённая" {
		t.Fatalf("ожидалась одна неразмещённая команда «Неразмещённая», получено %+v", got)
	}
}

// resolveHandlerProc резолвит fire-click авто-кнопки (нет элемента в дереве) по
// имени команды на её процедуру-Action (фикс A).
func TestResolveHandlerProc_CommandFallback(t *testing.T) {
	form := &metadata.FormModule{
		Commands: []*metadata.FormCommand{{Name: "МояКоманда", Action: "МояПроцНажатие"}},
	}
	if proc := resolveHandlerProc(form, "МояКоманда", "Нажатие"); proc != "МояПроцНажатие" {
		t.Fatalf("ожидалась «МояПроцНажатие», получено %q", proc)
	}
	// Неизвестное имя — пусто.
	if proc := resolveHandlerProc(form, "Нет", "Нажатие"); proc != "" {
		t.Fatalf("для неизвестной команды ожидалась пустая строка, получено %q", proc)
	}
}

// attrRefEntityName извлекает сущность из ссылочного типа реквизита формы (фикс B).
func TestAttrRefEntityName(t *testing.T) {
	cases := map[string]string{
		"CatalogRef.Клиент":    "Клиент",
		"DocumentRef.Заявка":   "Заявка",
		"string(40)":           "",
		"enum:СостояниеЗвонка": "",
		"":                     "",
	}
	for in, want := range cases {
		if got := attrRefEntityName(in); got != want {
			t.Errorf("attrRefEntityName(%q)=%q, ожидалось %q", in, got, want)
		}
	}
}
