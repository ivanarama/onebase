package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func (s *Server) getEntity(w http.ResponseWriter, r *http.Request) *metadata.Entity {
	raw := chi.URLParam(r, "entity")
	// chi may return the raw percent-encoded path segment — decode it
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		decoded = raw
	}
	if e := s.reg.GetEntityBySlug(decoded); e != nil {
		return e
	}
	http.Error(w, "unknown entity: "+raw, 404)
	return nil
}

// EnumOption — опция выпадающего списка enum: Value=имя значения (БД),
// Label=перевод для текущего языка (ValueTitle).
type EnumOption struct{ Value, Label string }

// enumValueLabels строит карту value → перевод(lang) для одного перечисления.
// Используется в buildEnumLabels и buildTPEnumLabels для устранения дублирования.
// Порядок значений не гарантирован (карта); для dropdown используйте loadEnumOptions,
// который итерирует en.Values напрямую.
func enumValueLabels(en *metadata.Enum, lang string) map[string]string {
	m := make(map[string]string, len(en.Values))
	for _, v := range en.Values {
		m[v] = en.ValueTitle(v, lang)
	}
	return m
}

// loadEnumOptions returns enum options for each enum-type field of the entity.
// Label is the translated display name for the given lang; Value is the
// canonical identifier stored in the database.
// Порядок значений сохраняется (итерация по en.Values), что важно для dropdown.
func (s *Server) loadEnumOptions(entity *metadata.Entity, lang string) map[string][]EnumOption {
	opts := make(map[string][]EnumOption)
	for _, f := range entity.Fields {
		if f.EnumName == "" {
			continue
		}
		en := s.reg.GetEnum(f.EnumName)
		if en == nil {
			continue
		}
		list := make([]EnumOption, 0, len(en.Values))
		for _, v := range en.Values {
			list = append(list, EnumOption{Value: v, Label: en.ValueTitle(v, lang)})
		}
		opts[f.Name] = list
	}
	return opts
}

// loadChoiceOptions returns declarative value-list options (аналог 1С СписокВыбора)
// for each managed-form element that carries a `choices` block. Ключ — имя элемента
// (список значений живёт на элементе формы, а не на поле сущности). Label —
// локализованная подпись с откатом на Value; порядок следует объявлению в YAML.
func loadChoiceOptions(form *metadata.FormModule, lang string) map[string][]EnumOption {
	opts := make(map[string][]EnumOption)
	if form == nil {
		return opts
	}
	form.Walk(func(el *metadata.FormElement) bool {
		if len(el.Choices) > 0 {
			list := make([]EnumOption, 0, len(el.Choices))
			for _, c := range el.Choices {
				list = append(list, EnumOption{Value: c.Value, Label: c.ChoiceLabel(lang)})
			}
			opts[el.Name] = list
		}
		return true
	})
	return opts
}

func (s *Server) usersForSelection(ctx context.Context) []map[string]any {
	if s.authRepo == nil {
		return nil
	}
	users, err := s.authRepo.ListForSelection(ctx)
	if err != nil {
		return nil
	}
	rows := make([]map[string]any, 0, len(users))
	for _, u := range users {
		label := u.Login
		if u.FullName != "" {
			label = u.FullName
		}
		rows = append(rows, map[string]any{"id": u.ID, "_label": label})
	}
	return rows
}

type refOptionsMode int

const (
	refOptionsChoice refOptionsMode = iota
	refOptionsFilter
)

func (s *Server) refListParamsForMode(refEntity *metadata.Entity, mode refOptionsMode) storage.ListParams {
	params := storage.ListParams{}
	if mode == refOptionsChoice && refEntity != nil && refEntity.Activity != nil && refEntity.Activity.HideFromChoice {
		params.ActivityScope = metadata.ActivityScopeActive
	}
	return params
}

func (s *Server) referenceOptions(ctx context.Context, refEntity *metadata.Entity, mode refOptionsMode) ([]map[string]any, error) {
	if refEntity == nil {
		return nil, nil
	}
	rows, err := s.store.List(ctx, refEntity.Name, refEntity, s.refListParamsForMode(refEntity, mode))
	if err != nil {
		return nil, err
	}
	rows = filterOutFolders(rows)
	for _, row := range rows {
		row["_label"] = firstStringField(row, refEntity)
	}
	return rows, nil
}

func (s *Server) loadRefOptions(ctx context.Context, entity *metadata.Entity) (map[string][]map[string]any, error) {
	return s.loadRefOptionsWithMode(ctx, entity, refOptionsChoice)
}

func (s *Server) loadRefFilterOptions(ctx context.Context, entity *metadata.Entity) (map[string][]map[string]any, error) {
	return s.loadRefOptionsWithMode(ctx, entity, refOptionsFilter)
}

func (s *Server) loadRefOptionsWithMode(ctx context.Context, entity *metadata.Entity, mode refOptionsMode) (map[string][]map[string]any, error) {
	opts := make(map[string][]map[string]any)
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		// Special handling: _users is not a catalog entity, but a system table.
		if f.RefEntity == "_users" {
			opts[f.Name] = s.usersForSelection(ctx)
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		rows, err := s.referenceOptions(ctx, refEntity, mode)
		if err != nil {
			return nil, err
		}
		opts[f.Name] = rows
	}
	return opts, nil
}

// loadTPRefOptions returns select options for reference fields in all table parts.
// Result: tpName → fieldName → [{id, _label, ...}]
func (s *Server) loadTPRefOptions(ctx context.Context, entity *metadata.Entity) (map[string]map[string][]map[string]any, error) {
	result := make(map[string]map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		tpOpts := make(map[string][]map[string]any)
		for _, f := range tp.Fields {
			if f.RefEntity == "" {
				continue
			}
			// Always mark the field as a reference (even if catalog empty or missing)
			tpOpts[f.Name] = []map[string]any{}
			refEntity := s.reg.GetEntity(f.RefEntity)
			if refEntity == nil {
				continue
			}
			rows, err := s.referenceOptions(ctx, refEntity, refOptionsChoice)
			if err != nil {
				continue
			}
			tpOpts[f.Name] = rows
		}
		// Always add TP entry so JS knows which fields are references
		result[tp.Name] = tpOpts
	}
	return result, nil
}

// resolveRegisterRows enriches register movement rows with human-readable values:
// recorder_label = "TypeName №Num от Date", dimension UUID values → catalog names.
// refCol описывает колонку строки, значение которой — UUID объекта RefEntity и
// должно быть заменено на наименование. Пустой RefEntity → поиск по всем сущностям
// (legacy string-колонки, хранящие UUID).
type refCol struct {
	Key       string
	RefEntity string
}

// resolveRefColumns заменяет UUID-значения в указанных колонках строк на
// наименования соответствующих объектов. Общее ядро для регистров накопления и
// регистра бухгалтерии (субконто).
//
// Если labelSuffix == "" — замена происходит in-place (прежнее поведение).
// Если labelSuffix != "" — наименование записывается в row[key+labelSuffix],
// а оригинальное значение (UUID) остаётся нетронутым.
func (s *Server) resolveRefColumns(ctx context.Context, rows []map[string]any, cols []refCol, labelSuffix string) {
	// Build lookup: RefEntity → set of UUIDs to resolve
	entityUUIDs := make(map[string]map[string]string) // entityName → {uuid: label}
	for _, row := range rows {
		for _, c := range cols {
			v := asString(row[c.Key])
			if v == "" {
				continue
			}
			if _, err := uuid.Parse(v); err != nil {
				continue
			}
			if entityUUIDs[c.RefEntity] == nil {
				entityUUIDs[c.RefEntity] = make(map[string]string)
			}
			entityUUIDs[c.RefEntity][v] = ""
		}
	}

	// Resolve UUIDs: for known RefEntity — targeted lookup; for unknown — scan all.
	uuidToLabel := make(map[string]string)
	for entName, uuids := range entityUUIDs {
		var entities []*metadata.Entity
		if entName != "" {
			if e := s.reg.GetEntity(entName); e != nil {
				entities = []*metadata.Entity{e}
			}
		}
		if len(entities) == 0 {
			entities = s.reg.Entities()
		}
		for idStr := range uuids {
			if _, done := uuidToLabel[idStr]; done {
				continue
			}
			id, _ := uuid.Parse(idStr)
			for _, entity := range entities {
				refRow, err := s.store.GetByID(ctx, entity.Name, id, entity)
				if err == nil {
					uuidToLabel[idStr] = firstStringField(refRow, entity)
					break
				}
			}
		}
	}

	for _, row := range rows {
		for _, c := range cols {
			v := asString(row[c.Key])
			if v == "" {
				continue
			}
			if label, found := uuidToLabel[v]; found && label != "" {
				if labelSuffix == "" {
					row[c.Key] = label
				} else {
					row[c.Key+labelSuffix] = label
				}
			}
		}
	}
}

// asString returns a string from row values that may be string or []byte
// (SQLite drivers differ in what they return for TEXT columns).
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	}
	return ""
}

func regFmtDate(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.Format("02.01.2006")
	}
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Format("02.01.2006")
		}
	}
	return fmt.Sprintf("%v", v)
}

func (s *Server) renderForbidden(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusForbidden)
	s.render(w, r, "page-forbidden", map[string]any{})
}

// can reports whether the current request may perform op on (kind, entity).
// A nil user means auth is not configured or no users exist → open access
// (mirrors the IsAdmin defaulting used elsewhere). Admins pass via User.Has,
// which returns true for IsAdmin.
func (s *Server) can(r *http.Request, kind, entity, op string) bool {
	return s.canCtx(r.Context(), kind, entity, op)
}

// canCtx — версия can для путей без *http.Request (ИИ-инструменты), берущая
// пользователя из контекста. nil-пользователь (нет пользователей / открытый
// деплой) проходит; админ проходит через User.Has.
func (s *Server) canCtx(ctx context.Context, kind, entity, op string) bool {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return true
	}
	return u.Has(kind, entity, op)
}

// requirePerm renders the 403 page and returns false when op is not allowed.
func (s *Server) requirePerm(w http.ResponseWriter, r *http.Request, kind, entity, op string) bool {
	if s.can(r, kind, entity, op) {
		return true
	}
	s.renderForbidden(w, r)
	return false
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data == nil {
		data = make(map[string]any)
	}
	if _, ok := data["Cfg"]; !ok {
		data["Cfg"] = s.cfg
	}
	if _, ok := data["Lang"]; !ok {
		data["Lang"] = s.resolveLang(r)
	}
	if _, ok := data["AvailableLangs"]; !ok {
		if s.cfg.Bundle != nil {
			data["AvailableLangs"] = s.cfg.Bundle.Available()
		}
	}
	if _, ok := data["Nav"]; !ok {
		// Нейтральный старт: подсистему не подставляем — на /ui/ ничего не
		// подсвечено, активна «Главная», сайдбар плоский (см. buildNav).
		sub := r.URL.Query().Get("subsystem")
		data["Nav"] = s.buildNav(r, sub)
		data["Subsystems"] = s.reg.Subsystems()
		data["CurrentSubsystem"] = sub
	}
	if _, ok := data["CollapsibleNav"]; !ok {
		data["CollapsibleNav"] = s.store.GetNavCollapsible(r.Context())
	}
	if _, ok := data["IsAdmin"]; !ok {
		data["IsAdmin"] = s.isAdmin(r)
	}
	if _, ok := data["FormOpenMode"]; !ok {
		login := currentUserLogin(r)
		data["FormOpenMode"] = s.store.EffectiveFormOpenMode(r.Context(), login)
		data["FormOpenModePersonal"] = s.store.GetUserFormOpenMode(r.Context(), login)
	}
	// Default per-entity permission flags so partial render paths (e.g. validation
	// errors) still show the right action buttons.
	if ent, ok := data["Entity"].(*metadata.Entity); ok {
		kind := string(ent.Kind)
		if _, ok := data["CanWrite"]; !ok {
			data["CanWrite"] = s.can(r, kind, ent.Name, "write")
		}
		if _, ok := data["CanDelete"]; !ok {
			data["CanDelete"] = s.can(r, kind, ent.Name, "delete")
		}
		if _, ok := data["CanPost"]; !ok {
			data["CanPost"] = s.can(r, kind, ent.Name, "post")
		}
		if _, ok := data["CanUnpost"]; !ok {
			data["CanUnpost"] = s.can(r, kind, ent.Name, "unpost")
		}
	}
	// Same for info-register views, which key off "InfoReg" instead of "Entity".
	if ir, ok := data["InfoReg"].(*metadata.InfoRegister); ok {
		if _, ok := data["CanWrite"]; !ok {
			data["CanWrite"] = s.can(r, "inforeg", ir.Name, "write")
		}
		if _, ok := data["CanDelete"]; !ok {
			data["CanDelete"] = s.can(r, "inforeg", ir.Name, "delete")
		}
	}
	if _, ok := data["HasAuth"]; !ok {
		u := auth.UserFromContext(r.Context())
		data["HasAuth"] = s.authRepo != nil && u != nil
		if u != nil {
			data["DenyPasswdChange"] = u.DenyPasswdChange
		}
	}
	t := s.tmpl
	if t == nil {
		t = tmpl
	}
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, s.errText(r, err), 500)
	}
}

func (s *Server) allFunctions(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	var catalogs, documents []*metadata.Entity
	for _, e := range s.reg.Entities() {
		if e.Kind == metadata.KindCatalog {
			catalogs = append(catalogs, e)
		} else {
			documents = append(documents, e)
		}
	}
	s.render(w, r, "page-all-functions", map[string]any{
		"Catalogs":      catalogs,
		"Documents":     documents,
		"Registers":     s.reg.Registers(),
		"InfoRegisters": s.reg.InfoRegisters(),
		"Enums":         s.reg.Enums(),
		"Reports":       s.reg.Reports(),
		"Processors":    s.reg.Processors(),
		"Constants":     s.reg.Constants(),
	})
}

// tpRefMeta строит карту tpName → fieldName → {entity, allowCreate} для
// JS-помощника addTpRow: динамически добавленные строки ТЧ рендерят кнопку
// «+ Создать» с правильным целевым справочником, а allowCreate решает
// показывать ли кнопку (дефолт в ТЧ — false, переопределяется в YAML).
func tpRefMeta(entity *metadata.Entity) map[string]map[string]any {
	out := make(map[string]map[string]any, len(entity.TableParts))
	for _, tp := range entity.TableParts {
		m := map[string]any{}
		for _, f := range tp.Fields {
			if f.RefEntity != "" {
				m[f.Name] = map[string]any{
					"entity":      f.RefEntity,
					"allowCreate": f.InlineCreateEnabled(true),
				}
			}
		}
		out[tp.Name] = m
	}
	return out
}

// asBool converts DB boolean values to Go bool.
// SQLite stores booleans as int64(0/1); PostgreSQL returns bool directly.
func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	}
	return false
}

// filterOutFolders removes rows where is_folder=true (hierarchical catalog groups).
// Used to prevent selecting groups in reference fields of documents/table parts.
func filterOutFolders(rows []map[string]any) []map[string]any {
	out := rows[:0:len(rows)]
	for _, row := range rows {
		if asBool(row["is_folder"]) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func firstStringField(row map[string]any, e *metadata.Entity) string {
	for _, f := range e.Fields {
		if f.Type == metadata.FieldTypeString {
			if v, ok := row[f.Name]; ok && v != nil {
				return fmt.Sprintf("%v", v)
			}
		}
	}
	return fmt.Sprintf("%v", row["id"])
}

func formToFields(r *http.Request, entity *metadata.Entity) map[string]any {
	fields := make(map[string]any)
	for _, f := range entity.Fields {
		val := r.FormValue(f.Name)
		if val == "" {
			fields[f.Name] = nil
			continue
		}
		switch f.Type {
		case metadata.FieldTypeDate:
			parsed := false
			for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
				if t, err := time.ParseInLocation(layout, val, time.Local); err == nil {
					fields[f.Name] = t
					parsed = true
					break
				}
			}
			if !parsed {
				fields[f.Name] = val
			}
		case metadata.FieldTypeBool:
			fields[f.Name] = val == "true"
		case metadata.FieldTypeNumber:
			if n, err := strconv.ParseFloat(val, 64); err == nil {
				fields[f.Name] = n
			} else {
				fields[f.Name] = val
			}
		case metadata.FieldTypeRichText:
			// Санитизация на ЗАПИСИ: вырезаем script/on*/внешние src ещё до
			// сохранения (на выводе санитизируем повторно — defense-in-depth).
			fields[f.Name] = richtext.Sanitize(val)
		default:
			fields[f.Name] = val
		}
	}
	return fields
}

// checkRichTextLimits проверяет, что ни одно richtext-поле формы не превышает
// richtext.MaxBytes. Проверка по сырому FormValue (до санитайза). Возвращает
// локализуемую ошибку формы при превышении.
func checkRichTextLimits(r *http.Request, entity *metadata.Entity) error {
	for _, f := range entity.Fields {
		if !metadata.IsRichText(f.Type) {
			continue
		}
		if len(r.FormValue(f.Name)) > richtext.MaxBytes {
			return i18nerr.Errorf("поле %s: превышен размер richtext (%d МБ)", f.Name, richtext.MaxBytes>>20)
		}
	}
	return nil
}

func formValues(r *http.Request, entity *metadata.Entity) map[string]string {
	vals := make(map[string]string)
	for _, f := range entity.Fields {
		vals[f.Name] = r.FormValue(f.Name)
	}
	return vals
}

// resolveRefs replaces UUID values of reference fields with the display name
// of the referenced entity (first string field). Modifies rows in place.
func (s *Server) resolveRefs(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		// collect unique IDs referenced in this field
		seen := map[string]bool{}
		for _, row := range rows {
			if v := row[f.Name]; v != nil {
				seen[fmt.Sprintf("%v", v)] = true
			}
		}
		// resolve each unique ID to a display label
		labels := make(map[string]string, len(seen))
		for idStr := range seen {
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			refRow, err := s.store.GetByID(ctx, refEntity.Name, id, refEntity)
			if err != nil {
				continue
			}
			labels[idStr] = firstStringField(refRow, refEntity)
		}
		// replace UUIDs with labels in all rows
		for _, row := range rows {
			if v := row[f.Name]; v != nil {
				if label, ok := labels[fmt.Sprintf("%v", v)]; ok {
					row[f.Name] = label
				}
			}
		}
	}
}

// enrichTPRowsWithRefs replaces UUID strings in reference fields of table-part rows
// with interpreter.Ref{UUID, Name} so that DSL Строка(ref) returns the display name
// while UUID-based map lookups and SQL parameters still work correctly.
func (s *Server) enrichTPRowsWithRefs(ctx context.Context, tp metadata.TablePart, rows []map[string]any) {
	for _, f := range tp.Fields {
		if f.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		// collect unique IDs
		seen := map[string]bool{}
		for _, row := range rows {
			if v := row[f.Name]; v != nil {
				seen[fmt.Sprintf("%v", v)] = true
			}
		}
		// resolve each unique ID to a display label
		labels := make(map[string]string, len(seen))
		for idStr := range seen {
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			refRow, err := s.store.GetByID(ctx, refEntity.Name, id, refEntity)
			if err != nil {
				continue
			}
			labels[idStr] = firstStringField(refRow, refEntity)
		}
		// replace plain UUID strings with *interpreter.Ref{UUID, Name, Manager}
		mgr := s.refManagerFor(refEntity, ctx)
		for _, row := range rows {
			if v := row[f.Name]; v != nil {
				idStr := fmt.Sprintf("%v", v)
				if name, ok := labels[idStr]; ok {
					row[f.Name] = &interpreter.Ref{UUID: idStr, Name: name, Type: refEntity.Name, Manager: mgr}
				}
			}
		}
	}
}

// enrichHeaderRefs заменяет UUID-строки в ссылочных полях ШАПКИ объекта на
// *interpreter.Ref{UUID, Name} — симметрично enrichTPRowsWithRefs для строк
// табличных частей. Без этого ссылки шапки (например Склад) приходят в
// ОбработкаПроведения сырым UUID, и Строка(this.Склад) даёт UUID; фильтр по
// string-измерению (ГДЕ Склад = Строка(this.Склад)) не совпадает с движениями,
// записанными по имени из обработок/сидов. После обогащения шапка ведёт себя
// как при создании из обработки. Ref-параметры и reference-измерения остаются
// корректными: unwrapArrayParams приводит *Ref к UUID. См. П.37.
func (s *Server) enrichHeaderRefs(ctx context.Context, entity *metadata.Entity, obj *runtime.Object) {
	low := strings.ToLower
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		// Find the actual map key (PascalCase or lowercase) and replace in-place.
		var matchKey string
		var matchVal any
		for k, v := range obj.Fields {
			if low(k) == low(f.Name) {
				matchKey = k
				matchVal = v
				break
			}
		}
		if matchKey == "" || matchVal == nil {
			continue
		}
		if _, isRef := matchVal.(*interpreter.Ref); isRef {
			continue
		}
		idStr := fmt.Sprintf("%v", matchVal)
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		refRow, err := s.store.GetByID(ctx, refEntity.Name, id, refEntity)
		if err != nil {
			continue
		}
		obj.Fields[matchKey] = &interpreter.Ref{
			UUID:    idStr,
			Name:    firstStringField(refRow, refEntity),
			Type:    refEntity.Name,
			Manager: s.refManagerFor(refEntity, ctx),
		}
	}
}

// buildHierarchyBreadcrumbs returns the ancestor chain from root to parentID (inclusive).
func (s *Server) buildHierarchyBreadcrumbs(ctx context.Context, entity *metadata.Entity, parentID string) []map[string]string {
	id, err := uuid.Parse(parentID)
	if err != nil {
		return nil
	}
	chain, err := s.store.GetAncestorIDs(ctx, metadata.TableName(entity.Name), id)
	if err != nil {
		return nil
	}
	var crumbs []map[string]string
	for _, ancestorID := range chain {
		row, err := s.store.GetByID(ctx, entity.Name, ancestorID, entity)
		if err != nil {
			continue
		}
		crumbs = append(crumbs, map[string]string{
			"ID":    ancestorID.String(),
			"Label": firstStringField(row, entity),
		})
	}
	return crumbs
}

// loadFolderOptions returns all folder items for a hierarchical catalog (for parent select).
func (s *Server) loadFolderOptions(ctx context.Context, entity *metadata.Entity) []map[string]any {
	rows, err := s.store.List(ctx, entity.Name, entity, storage.ListParams{})
	if err != nil {
		return nil
	}
	var folders []map[string]any
	for _, row := range rows {
		if asBool(row["is_folder"]) {
			row["_label"] = firstStringField(row, entity)
			folders = append(folders, row)
		}
	}
	return folders
}

func listURL(entity *metadata.Entity) string {
	return fmt.Sprintf("/ui/%s/%s", strings.ToLower(string(entity.Kind)), strings.ToLower(entity.Name))
}

func capitalize(s string) string {
	if dec, err := url.PathUnescape(s); err == nil {
		s = dec
	}
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}

// sortKeys returns map keys in sorted order (for deterministic template output).
func sortKeys(m map[string]storage.FilterValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// filterValue returns the FilterValue for a field from ListParams, or empty.
func filterValue(params storage.ListParams, fieldName string) storage.FilterValue {
	if params.Filters == nil {
		return storage.FilterValue{}
	}
	return params.Filters[fieldName]
}
