package ui

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

type browserFormEventTarget struct {
	element         *metadata.FormElement
	parentTablePart *metadata.FormElement
	command         *metadata.FormCommand
}

// resolveBrowserFormEvent is the single fail-closed server-side model of what
// managed.js/templates can emit. Metadata may describe additional server-side
// lifecycle events, but they cannot be invoked through the browser endpoint.
func resolveBrowserFormEvent(form *metadata.FormModule, elementName, eventName string, processor bool) (string, browserFormEventTarget, bool, error) {
	var target browserFormEventTarget
	if form == nil {
		return "", target, false, fmt.Errorf("managed form not found")
	}
	event := metadata.FormEventType(strings.TrimSpace(eventName))
	if !metadata.IsKnownFormEventType(event) {
		return "", target, false, fmt.Errorf("событие формы %q не поддерживается", eventName)
	}

	elementName = strings.TrimSpace(elementName)
	if elementName == "" {
		if event != metadata.FormEventOnOpen {
			return "", target, false, fmt.Errorf("событие %q нельзя вызвать на уровне формы", eventName)
		}
		proc := form.Handlers[event]
		if strings.TrimSpace(proc) == "" {
			return "", target, false, fmt.Errorf("обработчик события %q не объявлен", eventName)
		}
		return proc, target, false, nil
	}

	el, parentTP := findBrowserEventElement(form, elementName)
	if el != nil {
		target.element = el
		target.parentTablePart = parentTP
		if el.ReadOnly || parentTP != nil && parentTP.ReadOnly {
			return "", target, false, fmt.Errorf("элемент формы %q доступен только для чтения", el.Name)
		}
		if !browserEventAllowedForElement(el.Kind, event) {
			return "", target, false, fmt.Errorf("элемент формы %q не отправляет событие %q", el.Name, eventName)
		}
		if proc := strings.TrimSpace(el.Handlers[event]); proc != "" {
			return proc, target, false, nil
		}
		if processor && event == metadata.FormEventOnClick && isProcessorExecuteFallbackButton(form, el) {
			return "", target, true, nil
		}
		return "", target, false, fmt.Errorf("обработчик события %q элемента %q не объявлен", eventName, el.Name)
	}

	if event != metadata.FormEventOnClick && event != metadata.FormEventOnChoice {
		return "", target, false, fmt.Errorf("элемент формы %q не найден", elementName)
	}
	for _, command := range unplacedCommands(form) {
		if command != nil && strings.EqualFold(command.Name, elementName) && strings.TrimSpace(command.Action) != "" {
			target.command = command
			return command.Action, target, false, nil
		}
	}
	return "", target, false, fmt.Errorf("элемент или доступная команда формы %q не найдены", elementName)
}

func browserEventAllowedForElement(kind metadata.FormElementType, event metadata.FormEventType) bool {
	switch kind {
	case metadata.FormElementButton:
		return event == metadata.FormEventOnClick || event == metadata.FormEventOnChoice
	case metadata.FormElementField, metadata.FormElementCodeField,
		metadata.FormElementCheckbox, metadata.FormElementDatePicker,
		metadata.FormElementSwitch:
		return event == metadata.FormEventOnChange || event == metadata.FormEventOnChoice
	case metadata.FormElementInputList:
		return event == metadata.FormEventOnChange || event == metadata.FormEventStartChoice || event == metadata.FormEventOnChoice
	case metadata.FormElementTablePart:
		return event == metadata.FormEventOnChange || event == metadata.FormEventOnRowAdded ||
			event == metadata.FormEventOnRowDeleted || event == metadata.FormEventOnChoice
	default:
		return false
	}
}

func findBrowserEventElement(form *metadata.FormModule, name string) (*metadata.FormElement, *metadata.FormElement) {
	if form == nil {
		return nil, nil
	}
	var walk func([]*metadata.FormElement, *metadata.FormElement) (*metadata.FormElement, *metadata.FormElement)
	walk = func(elements []*metadata.FormElement, parentTP *metadata.FormElement) (*metadata.FormElement, *metadata.FormElement) {
		for _, el := range elements {
			if el == nil {
				continue
			}
			if strings.EqualFold(el.Name, name) {
				return el, parentTP
			}
			nextTP := parentTP
			if el.Kind == metadata.FormElementTablePart {
				nextTP = el
			}
			if found, parent := walk(el.Children, nextTP); found != nil {
				return found, parent
			}
		}
		return nil, nil
	}
	return walk(form.Elements, nil)
}

// isProcessorExecuteFallbackButton is shared by the template and POST
// resolver. Only an editable, top-level, unbound Execute/Выполнить button can
// invoke the processor module's conventional Выполнить procedure.
func isProcessorExecuteFallbackButton(form *metadata.FormModule, el *metadata.FormElement) bool {
	if form == nil || el == nil || el.Kind != metadata.FormElementButton || el.ReadOnly {
		return false
	}
	name := strings.TrimSpace(el.Name)
	if !strings.EqualFold(name, "Выполнить") && !strings.EqualFold(name, "Execute") {
		return false
	}
	if strings.TrimSpace(el.Handlers[metadata.FormEventOnClick]) != "" {
		return false
	}
	found, parentTP := findBrowserEventElement(form, el.Name)
	return found == el && parentTP == nil
}
