package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

const maxChoiceSourcesJSON = 16 << 10

// managedChoiceContext is server-produced metadata for the ordinary reference
// picker. Source values are intentionally absent: ui.js reads the named form
// controls at request time, while the server restores Field/Op from the form in
// the registry and never trusts them from the browser.
type managedChoiceContext struct {
	FormEntity string            `json:"form_entity"`
	Form       string            `json:"form"`
	Element    string            `json:"element"`
	Sources    map[string]string `json:"sources,omitempty"` // full metadata path -> browser control name
}

type resolvedChoiceRequest struct {
	Predicates []storage.ChoicePredicate
	Empty      bool
	Selected   *uuid.UUID
}

func findManagedFormByName(owner *metadata.Entity, name string) *metadata.FormModule {
	if owner == nil {
		return nil
	}
	for _, form := range owner.Forms {
		if form != nil && form.IsManaged() && strings.EqualFold(form.Name, name) {
			return form
		}
	}
	return nil
}

func findChoiceElementByID(form *metadata.FormModule, id string) *metadata.FormElement {
	if form == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	var found *metadata.FormElement
	form.Walk(func(element *metadata.FormElement) bool {
		if found == nil && element != nil && element.ID == id {
			found = element
		}
		return found == nil
	})
	return found
}

func formChoicePath(path string) (root, name string, ok bool) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) != 2 {
		return "", "", false
	}
	root, name = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	return root, name, root != "" && name != ""
}

func formChoiceTypeEntity(typeRef string) string {
	typeRef = strings.TrimSpace(typeRef)
	separator := strings.Index(typeRef, ".")
	if separator <= 0 || separator == len(typeRef)-1 {
		return ""
	}
	prefix := typeRef[:separator]
	if !strings.EqualFold(prefix, "CatalogRef") && !strings.EqualFold(prefix, "DocumentRef") {
		return ""
	}
	return strings.TrimSpace(typeRef[separator+1:])
}

func formChoiceRefEntity(owner *metadata.Entity, form *metadata.FormModule, path string) string {
	root, name, ok := formChoicePath(path)
	if !ok {
		return ""
	}
	switch {
	case strings.EqualFold(root, "Объект"):
		if field, exists := entityFieldByName(owner, name); exists {
			return strings.TrimSpace(field.RefEntity)
		}
	case strings.EqualFold(root, "Форма"):
		if form == nil {
			return ""
		}
		for _, attr := range form.Attributes {
			if attr != nil && strings.EqualFold(attr.Name, name) {
				return formChoiceTypeEntity(attr.TypeRef)
			}
		}
	}
	return ""
}

func choiceSourceControls(element *metadata.FormElement) map[string]string {
	if element == nil {
		return nil
	}
	controls := make(map[string]string)
	for _, condition := range element.ChoiceFilter {
		path := strings.TrimSpace(condition.From)
		if path == "" {
			continue
		}
		// Браузер снимает значение ВЕДУЩЕГО элемента формы — и для глубокого
		// источника тоже: реквизит за ссылкой на форме не лежит, его читает
		// сервер под правами пользователя.
		if source, ok := metadata.ParseFormChoiceSource(path); ok {
			controls[path] = source.Field
		}
	}
	if len(controls) == 0 {
		return nil
	}
	return controls
}

// choicePredicates converts only server-owned metadata into storage
// predicates. Missing known source values fail closed with Empty=true;
// unknown source names and malformed UUIDs are request errors. Значение
// глубокого источника сервер читает сам: из браузера приходит только ссылка
// ведущего поля.
func (s *Server) choicePredicates(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, target *metadata.Entity, element *metadata.FormElement, sources map[string]string) ([]storage.ChoicePredicate, bool, error) {
	if element == nil || len(element.ChoiceFilter) == 0 {
		return nil, false, fmt.Errorf("choice_filter is not declared")
	}
	if target == nil {
		return nil, false, fmt.Errorf("choice target is unknown")
	}
	allowed := choiceSourceControls(element)
	for path := range sources {
		if _, ok := allowed[path]; !ok {
			return nil, false, fmt.Errorf("unknown choice source %q", path)
		}
	}

	predicates := make([]storage.ChoicePredicate, 0, len(element.ChoiceFilter))
	targetDecisions := s.fieldDecisions(ctx, target)
	for _, condition := range element.ChoiceFilter {
		// The filtered result and its total reveal a target field even when the
		// field itself is hidden from the response. Keep mask and hide identical.
		if choiceAttrMasked(targetDecisions, condition.Field) {
			return nil, true, nil
		}
		predicate := storage.ChoicePredicate{Field: condition.Field, Op: condition.Op}
		if condition.Value != nil {
			predicate.Value = *condition.Value
			predicates = append(predicates, predicate)
			continue
		}
		path := strings.TrimSpace(condition.From)
		raw := strings.TrimSpace(sources[path])
		if raw == "" {
			return nil, true, nil
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return nil, false, fmt.Errorf("invalid value for choice source %q", path)
		}
		source, ok := metadata.ParseFormChoiceSource(path)
		if !ok {
			return nil, false, fmt.Errorf("invalid choice source %q", path)
		}
		if !source.Deep() {
			predicate.Value = id
			predicates = append(predicates, predicate)
			continue
		}
		value, found, err := s.deepChoiceSourceValue(ctx, owner, form, source, id)
		if err != nil {
			return nil, false, err
		}
		if !found {
			return nil, true, nil
		}
		predicate.Value = value
		predicates = append(predicates, predicate)
	}
	return predicates, false, nil
}

// deepChoiceSourceValue читает единственный разрешённый переход по ссылке:
// реквизит записи, выбранной в ведущем поле формы (план 183, срез B1).
//
// found=false означает «отбирать нечем» и даёт пустую выдачу. Так выглядят все
// причины сразу: записи нет, она закрыта строковым доступом, реквизит закрыт
// полевой политикой или просто пуст. Различать их в ответе нельзя — иначе
// пустой подбор рассказывал бы, существует ли запись и что в ней лежит.
func (s *Server) deepChoiceSourceValue(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, source metadata.FormChoiceSource, id uuid.UUID) (uuid.UUID, bool, error) {
	lead := s.reg.GetEntity(formChoiceRefEntity(owner, form, source.Root+"."+source.Field))
	if lead == nil {
		return uuid.Nil, false, fmt.Errorf("unknown choice source entity")
	}
	attr, exists := entityFieldByName(lead, source.Attr)
	if !exists || strings.TrimSpace(attr.RefEntity) == "" {
		return uuid.Nil, false, fmt.Errorf("choice source attribute %q is not a reference", source.Attr)
	}
	if choiceAttrMasked(s.fieldDecisions(ctx, lead), attr.Name) {
		return uuid.Nil, false, nil
	}
	params, err := s.rowFilterFor(ctx, lead, "read", storage.ListParams{})
	if err != nil {
		return uuid.Nil, false, nil // нет доступа к посреднику — отбирать нечем
	}
	rows, err := s.store.GetFieldsByIDsFiltered(ctx, lead, []uuid.UUID{id}, []metadata.Field{attr}, params.RowFilter)
	if err != nil {
		return uuid.Nil, false, err
	}
	row, ok := rows[id.String()]
	if !ok {
		return uuid.Nil, false, nil
	}
	target, parseErr := uuid.Parse(strings.TrimSpace(refValueString(row[attr.Name])))
	if parseErr != nil || target == uuid.Nil {
		return uuid.Nil, false, nil
	}
	return target, true, nil
}

// choiceAttrMasked — закрыт ли реквизит полевой политикой. Ключи
// решений канонизированы по метаданным, но сверка без учёта регистра дешевле
// предположения: незамеченная маска означала бы отбор по значению, которого
// пользователю видеть нельзя.
func choiceAttrMasked(decisions map[string]access.FieldDecision, name string) bool {
	if len(decisions) == 0 {
		return false
	}
	if decision, ok := decisions[name]; ok {
		return decision.Masked()
	}
	for field, decision := range decisions {
		if strings.EqualFold(field, name) {
			return decision.Masked()
		}
	}
	return false
}

func decodeChoiceSources(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}, nil
	}
	if len(raw) > maxChoiceSourcesJSON {
		return nil, fmt.Errorf("choice sources are too large")
	}
	var sources map[string]string
	if err := json.Unmarshal([]byte(raw), &sources); err != nil || sources == nil {
		return nil, fmt.Errorf("invalid choice sources")
	}
	if len(sources) > 8 {
		return nil, fmt.Errorf("too many choice sources")
	}
	return sources, nil
}

func oneQueryValue(query map[string][]string, name string, required bool) (string, error) {
	values, present := query[name]
	if !present {
		if required {
			return "", fmt.Errorf("missing %s", name)
		}
		return "", nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("duplicate %s", name)
	}
	value := strings.TrimSpace(values[0])
	if required && value == "" {
		return "", fmt.Errorf("empty %s", name)
	}
	return value, nil
}

// resolveChoiceRequest restores the form and element from registry metadata.
// nil means a legacy request with no form context and preserves the old API.
func (s *Server) resolveChoiceRequest(r *http.Request, target *metadata.Entity) (*resolvedChoiceRequest, error) {
	query := r.URL.Query()
	// selected_id alone is not form context: the legacy endpoint historically
	// ignored unknown query parameters, so adding it must not turn an otherwise
	// context-free request into a 400. Identity/source parameters do opt in and
	// therefore require the complete trusted metadata context below.
	contextKeys := []string{"form_entity", "form", "element", "sources"}
	contextPresent := false
	for _, key := range contextKeys {
		if _, ok := query[key]; ok {
			contextPresent = true
			break
		}
	}
	if !contextPresent {
		return nil, nil
	}

	ownerName, err := oneQueryValue(query, "form_entity", true)
	if err != nil {
		return nil, err
	}
	formName, err := oneQueryValue(query, "form", true)
	if err != nil {
		return nil, err
	}
	elementID, err := oneQueryValue(query, "element", true)
	if err != nil {
		return nil, err
	}
	rawSources, err := oneQueryValue(query, "sources", false)
	if err != nil {
		return nil, err
	}
	selectedRaw, err := oneQueryValue(query, "selected_id", false)
	if err != nil {
		return nil, err
	}

	owner := s.reg.GetEntity(ownerName)
	if owner == nil {
		return nil, fmt.Errorf("unknown form entity")
	}
	form := findManagedFormByName(owner, formName)
	if form == nil {
		return nil, fmt.Errorf("unknown managed form")
	}
	element := findChoiceElementByID(form, elementID)
	if element == nil || len(element.ChoiceFilter) == 0 {
		return nil, fmt.Errorf("unknown choice element")
	}
	targetName := formChoiceRefEntity(owner, form, element.DataPath)
	if target == nil || targetName == "" || !strings.EqualFold(target.Name, targetName) {
		return nil, fmt.Errorf("choice target does not match route")
	}
	sources, err := decodeChoiceSources(rawSources)
	if err != nil {
		return nil, err
	}
	predicates, empty, err := s.choicePredicates(r.Context(), owner, form, target, element, sources)
	if err != nil {
		return nil, err
	}

	resolved := &resolvedChoiceRequest{Predicates: predicates, Empty: empty}
	if selectedRaw != "" {
		id, parseErr := uuid.Parse(selectedRaw)
		if parseErr != nil || id == uuid.Nil {
			return nil, fmt.Errorf("invalid selected_id")
		}
		resolved.Selected = &id
	}
	return resolved, nil
}

func (s *Server) choiceSelectedAllowed(ctx context.Context, target *metadata.Entity, id uuid.UUID, predicates []storage.ChoicePredicate) (bool, error) {
	return s.choiceSelectedAllowedWithParams(ctx, target, id, storage.ListParams{ChoicePredicates: predicates})
}

func (s *Server) choiceSelectedAllowedWithParams(ctx context.Context, target *metadata.Entity, id uuid.UUID, params storage.ListParams) (bool, error) {
	base := s.refListParamsForMode(target, refOptionsChoice)
	base.Filters = params.Filters
	base.ChoicePredicates = params.ChoicePredicates
	var err error
	base, err = s.rowFilterFor(ctx, target, "read", base)
	if err != nil {
		return false, err
	}
	return s.store.ListContainsID(ctx, target.Name, target, id, base)
}

func formValueForPath(values any, path string) string {
	_, name, ok := formChoicePath(path)
	if !ok {
		return ""
	}
	switch typed := values.(type) {
	case map[string]string:
		if value, exists := typed[name]; exists {
			return strings.TrimSpace(value)
		}
		for key, value := range typed {
			if strings.EqualFold(key, name) {
				return strings.TrimSpace(value)
			}
		}
	case map[string]any:
		if value, exists := typed[name]; exists {
			return refValueString(value)
		}
		for key, value := range typed {
			if strings.EqualFold(key, name) {
				return refValueString(value)
			}
		}
	}
	return ""
}

func markOutsideChoice(rows []map[string]any, selected string) {
	for _, row := range rows {
		if refValueString(row["id"]) == selected {
			row["_choice_outside_filter"] = true
		}
	}
}

// Оба ограничения складываются здесь: подчинение справочника (owner) даёт
// params, условия choice_filter — predicates. Значение глубокого источника
// читает сервер, поэтому нужны owner-сущность, форма и цель.
func (s *Server) initialChoiceOptions(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, target *metadata.Entity, element *metadata.FormElement, sources map[string]string, selected, ownerID string, ownerAsked bool) ([]map[string]any, error) {
	predicates, empty, err := s.choicePredicates(ctx, owner, form, target, element, sources)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	params, ownerOK := ownerFilterParams(target, ownerID, ownerAsked, storage.ListParams{Limit: refPickerDefaultLimit, ChoicePredicates: predicates})
	if !empty && ownerOK {
		rows, err = s.referenceOptionsWithParams(ctx, target, refOptionsChoice, params)
		if err != nil {
			return nil, err
		}
	}
	rows = s.appendSelectedRefOptions(ctx, rows, target, []string{selected})
	if selected == "" {
		return rows, nil
	}
	allowed := false
	if !empty && ownerOK {
		if id, parseErr := uuid.Parse(selected); parseErr == nil && id != uuid.Nil {
			allowed, err = s.choiceSelectedAllowedWithParams(ctx, target, id, params)
			if err != nil {
				return nil, err
			}
		}
	}
	if !allowed {
		markOutsideChoice(rows, selected)
	}
	return rows, nil
}

// applyManagedChoiceFilters replaces only the options of elements that opted
// into choice_filter and publishes opaque picker context for ui.js. Other
// managed and all autogen reference fields keep the legacy path unchanged.
func (s *Server) applyManagedChoiceFilters(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, data map[string]any) {
	if owner == nil || form == nil || data == nil {
		return
	}
	options := make(map[string][]map[string]any)
	contexts := make(map[string]string)
	form.Walk(func(element *metadata.FormElement) bool {
		if element == nil || len(element.ChoiceFilter) == 0 || strings.TrimSpace(element.ID) == "" {
			return true
		}
		targetName := formChoiceRefEntity(owner, form, element.DataPath)
		target := s.reg.GetEntity(targetName)
		if target == nil {
			options[element.ID] = nil // invalid runtime metadata stays fail-closed
			return true
		}
		controls := choiceSourceControls(element)
		sources := make(map[string]string, len(controls))
		for path := range controls {
			source, ok := metadata.ParseFormChoiceSource(path)
			if !ok {
				continue
			}
			// The form holds the leading reference, not the attribute behind it.
			sources[path] = formValueForPath(data["Values"], source.Root+"."+source.Field)
		}
		selected := formValueForPath(data["Values"], element.DataPath)
		ownerID, ownerAsked := "", false
		if hf, ok := ownerHolderField(owner, target.Owner); ok {
			ownerID, ownerAsked = formValueForPath(data["Values"], "Объект."+hf.Name), true
		}
		rows, err := s.initialChoiceOptions(ctx, owner, form, target, element, sources, selected, ownerID, ownerAsked)
		if err == nil {
			options[element.ID] = rows
		} else {
			options[element.ID] = nil // never fall back to an unfiltered catalog
		}
		encoded, err := json.Marshal(managedChoiceContext{
			FormEntity: owner.Name,
			Form:       form.Name,
			Element:    element.ID,
			Sources:    controls,
		})
		if err == nil {
			contexts[element.ID] = string(encoded)
		}
		return true
	})
	if len(options) > 0 {
		data["ManagedChoiceOptions"] = options
		data["ManagedChoiceContexts"] = contexts
	}
}
