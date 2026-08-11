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

type browserFormElementVisit struct {
	element           *metadata.FormElement
	parentTablePart   *metadata.FormElement
	effectiveReadOnly bool
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

	var elements []browserFormElementVisit
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		if strings.EqualFold(visit.element.Name, elementName) {
			elements = append(elements, visit)
		}
	})
	var commands []*metadata.FormCommand
	for _, command := range unplacedCommands(form) {
		if command != nil && strings.EqualFold(command.Name, elementName) && strings.TrimSpace(command.Action) != "" {
			commands = append(commands, command)
		}
	}
	if len(elements)+len(commands) > 1 {
		return "", target, false, fmt.Errorf("имя цели события формы %q неоднозначно", elementName)
	}
	if len(elements) == 1 {
		visit := elements[0]
		el := visit.element
		target.element = el
		target.parentTablePart = visit.parentTablePart
		if visit.effectiveReadOnly {
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

	if len(commands) == 1 {
		if event != metadata.FormEventOnClick && event != metadata.FormEventOnChoice {
			return "", target, false, fmt.Errorf("команда формы %q не отправляет событие %q", elementName, eventName)
		}
		target.command = commands[0]
		return commands[0].Action, target, false, nil
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
			event == metadata.FormEventOnRowDeleted || event == metadata.FormEventOnChoice ||
			event == metadata.FormEventOnRowActivated || event == metadata.FormEventOnRowChanged ||
			event == metadata.FormEventAfterRowAdd
	default:
		return false
	}
}

func walkBrowserFormElements(form *metadata.FormModule, visit func(browserFormElementVisit)) {
	if form == nil || visit == nil {
		return
	}
	var walk func([]*metadata.FormElement, *metadata.FormElement, bool)
	walk = func(elements []*metadata.FormElement, parentTable *metadata.FormElement, parentReadOnly bool) {
		for _, element := range elements {
			if element == nil {
				continue
			}
			effectiveReadOnly := parentReadOnly || element.ReadOnly
			visit(browserFormElementVisit{
				element: element, parentTablePart: parentTable, effectiveReadOnly: effectiveReadOnly,
			})
			nextTable := parentTable
			if element.Kind == metadata.FormElementTablePart {
				nextTable = element
			}
			walk(element.Children, nextTable, effectiveReadOnly)
		}
	}
	walk(form.Elements, nil, false)
}

func effectiveFormElementReadOnly(form *metadata.FormModule, target *metadata.FormElement) bool {
	if target == nil {
		return true
	}
	// managed-element is also rendered directly by focused template tests and
	// editor previews which do not always carry the owning FormModule. Preserve
	// the element's own flag as a safe fallback; the normal page path below
	// replaces it with inherited state from the real tree.
	readOnly := target.ReadOnly
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		if visit.element == target {
			readOnly = visit.effectiveReadOnly
		}
	})
	return readOnly
}

// isProcessorExecuteFallbackButton is shared by the template and POST
// resolver. Only an editable, top-level, unbound Execute/Выполнить button can
// invoke the processor module's conventional Выполнить procedure.
func isProcessorExecuteFallbackButton(form *metadata.FormModule, el *metadata.FormElement) bool {
	if form == nil || el == nil || el.Kind != metadata.FormElementButton || effectiveFormElementReadOnly(form, el) {
		return false
	}
	name := strings.TrimSpace(el.Name)
	if !strings.EqualFold(name, "Выполнить") && !strings.EqualFold(name, "Execute") {
		return false
	}
	if strings.TrimSpace(el.Handlers[metadata.FormEventOnClick]) != "" {
		return false
	}
	var matches []browserFormElementVisit
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		if strings.EqualFold(visit.element.Name, el.Name) {
			matches = append(matches, visit)
		}
	})
	if len(matches) != 1 || matches[0].element != el || matches[0].parentTablePart != nil {
		return false
	}
	for _, command := range unplacedCommands(form) {
		if command != nil && strings.EqualFold(command.Name, el.Name) && strings.TrimSpace(command.Action) != "" {
			return false
		}
	}
	return true
}
