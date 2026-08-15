package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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
	"github.com/shopspring/decimal"
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

const (
	refPickerDefaultLimit = 50
	refPickerMaxLimit     = 100
)

func (s *Server) refListParamsForMode(refEntity *metadata.Entity, mode refOptionsMode) storage.ListParams {
	params := storage.ListParams{ExcludeFolders: true}
	if mode == refOptionsChoice && refEntity != nil && refEntity.Activity != nil && refEntity.Activity.HideFromChoice {
		params.ActivityScope = metadata.ActivityScopeActive
	}
	return params
}

func (s *Server) referenceOptions(ctx context.Context, refEntity *metadata.Entity, mode refOptionsMode) ([]map[string]any, error) {
	return s.referenceOptionsWithParams(ctx, refEntity, mode, storage.ListParams{})
}

func (s *Server) referenceOptionsWithParams(ctx context.Context, refEntity *metadata.Entity, mode refOptionsMode, extra storage.ListParams) ([]map[string]any, error) {
	if refEntity == nil {
		return nil, nil
	}
	params := s.refListParamsForMode(refEntity, mode)
	params.Filters = extra.Filters
	params.Search = extra.Search
	params.Sort = extra.Sort
	params.Dir = extra.Dir
	params.Limit = extra.Limit
	params.Offset = extra.Offset
	var err error
	params, err = s.rowFilterFor(ctx, refEntity, "read", params)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.List(ctx, refEntity.Name, refEntity, params)
	if err != nil {
		return nil, err
	}
	rows = filterOutFolders(rows)
	// План 88: picker маскирует чувствительные поля до вычисления подписи и до
	// сериализации строк — иначе замаскированное поле утекло бы в JSON выбора.
	s.maskRecords(ctx, refEntity, rows)
	for _, row := range rows {
		row["_label"] = firstStringField(row, refEntity)
	}
	return rows, nil
}

func (s *Server) referenceOptionsPage(ctx context.Context, refEntity *metadata.Entity, search string, limit, offset int) ([]map[string]any, int, error) {
	if refEntity == nil {
		return nil, 0, nil
	}
	params := storage.ListParams{
		Search: strings.TrimSpace(search),
		Limit:  limit,
		Offset: offset,
	}
	rows, err := s.referenceOptionsWithParams(ctx, refEntity, refOptionsChoice, params)
	if err != nil {
		return nil, 0, err
	}
	countParams := s.refListParamsForMode(refEntity, refOptionsChoice)
	countParams.Search = strings.TrimSpace(search)
	countParams, err = s.rowFilterFor(ctx, refEntity, "read", countParams)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountList(ctx, refEntity.Name, refEntity, countParams)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *Server) initialReferenceOptions(ctx context.Context, refEntity *metadata.Entity, mode refOptionsMode, selected []string) ([]map[string]any, error) {
	rows, err := s.referenceOptionsWithParams(ctx, refEntity, mode, storage.ListParams{Limit: refPickerDefaultLimit})
	if err != nil {
		return nil, err
	}
	return s.appendSelectedRefOptions(ctx, rows, refEntity, selected), nil
}

func (s *Server) loadRefOptions(ctx context.Context, entity *metadata.Entity) (map[string][]map[string]any, error) {
	return s.loadRefOptionsWithMode(ctx, entity, refOptionsChoice)
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
		rows, err := s.initialReferenceOptions(ctx, refEntity, mode, nil)
		if err != nil {
			return nil, err
		}
		opts[f.Name] = rows
	}
	return opts, nil
}

func (s *Server) loadInitialRefOptions(ctx context.Context, entity *metadata.Entity, values map[string]string) (map[string][]map[string]any, error) {
	opts := make(map[string][]map[string]any)
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		if f.RefEntity == "_users" {
			opts[f.Name] = s.usersForSelection(ctx)
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		rows, err := s.initialReferenceOptions(ctx, refEntity, refOptionsChoice, []string{values[f.Name]})
		if err != nil {
			return nil, err
		}
		opts[f.Name] = rows
	}
	return opts, nil
}

func (s *Server) loadInitialRefFilterOptions(ctx context.Context, entity *metadata.Entity, params storage.ListParams) (map[string][]map[string]any, error) {
	opts := make(map[string][]map[string]any)
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		rows, err := s.initialReferenceOptions(ctx, refEntity, refOptionsFilter, []string{params.Filters[f.Name].Value})
		if err != nil {
			return nil, err
		}
		opts[f.Name] = rows
	}
	return opts, nil
}

func (s *Server) loadInitialTPRefOptions(ctx context.Context, entity *metadata.Entity, tpRows map[string][]map[string]any) (map[string]map[string][]map[string]any, error) {
	result := make(map[string]map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		tpOpts := make(map[string][]map[string]any)
		for _, f := range tp.Fields {
			if f.RefEntity == "" {
				continue
			}
			tpOpts[f.Name] = []map[string]any{}
			refEntity := s.reg.GetEntity(f.RefEntity)
			if refEntity == nil {
				continue
			}
			rows, err := s.initialReferenceOptions(ctx, refEntity, refOptionsChoice, selectedTPRefIDs(tpRows[tp.Name], f.Name))
			if err != nil {
				continue
			}
			tpOpts[f.Name] = rows
		}
		result[tp.Name] = tpOpts
	}
	return result, nil
}

func selectedTPRefIDs(rows []map[string]any, field string) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		id := refValueString(row[field])
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func refValueString(v any) string {
	if v == nil {
		return ""
	}
	if ref, ok := v.(interface{ GetRefUUID() string }); ok {
		return ref.GetRefUUID()
	}
	if id, ok := v.(uuid.UUID); ok {
		return id.String()
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func (s *Server) appendSelectedRefOptions(ctx context.Context, rows []map[string]any, refEntity *metadata.Entity, selected []string) []map[string]any {
	seen := make(map[string]bool, len(rows)+len(selected))
	for _, row := range rows {
		if id := refValueString(row["id"]); id != "" {
			seen[id] = true
		}
	}
	for _, idStr := range selected {
		idStr = strings.TrimSpace(idStr)
		if idStr == "" || seen[idStr] {
			continue
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		row, err := s.store.GetByID(ctx, refEntity.Name, id, refEntity)
		if err != nil || row == nil {
			continue
		}
		if !s.rowAllowsSelected(ctx, refEntity, row) {
			continue
		}
		// План 88: замаскировать догруженную (вне первой страницы) выбранную
		// запись до вычисления подписи и сериализации — иначе ПДн из ссылки
		// утекли бы в JSON опций (HTML/DevTools) в обход маски списка.
		s.maskRecord(ctx, refEntity, row)
		row["_label"] = firstStringField(row, refEntity)
		rows = append(rows, row)
		seen[idStr] = true
	}
	return rows
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
	// Group unique referenced UUIDs by declared RefEntity ("" = unknown type,
	// legacy string columns that store a UUID). Keys are canonical id strings.
	entityUUIDs := make(map[string]map[string]uuid.UUID) // entityName → {idStr: id}
	for _, row := range rows {
		for _, c := range cols {
			v := asString(row[c.Key])
			if v == "" {
				continue
			}
			id, err := uuid.Parse(v)
			if err != nil {
				continue
			}
			if entityUUIDs[c.RefEntity] == nil {
				entityUUIDs[c.RefEntity] = make(map[string]uuid.UUID)
			}
			entityUUIDs[c.RefEntity][id.String()] = id
		}
	}

	// Resolve to labels. Known RefEntity → one batched query. Unknown type →
	// probe each entity once with the whole remaining set (one query per entity,
	// stopping as soon as every id is resolved) instead of GetByID per
	// (uuid × entity). First match wins, preserved by pruning resolved ids.
	// План 111 (P1-1).
	uuidToLabel := make(map[string]string)
	for entName, idset := range entityUUIDs {
		if entName != "" {
			if e := s.reg.GetEntity(entName); e != nil {
				s.batchReadableLabels(ctx, e, idset, uuidToLabel)
				continue
			}
		}
		remaining := make(map[string]uuid.UUID, len(idset))
		for k, id := range idset {
			if _, done := uuidToLabel[k]; !done {
				remaining[k] = id
			}
		}
		for _, e := range s.reg.Entities() {
			if len(remaining) == 0 {
				break
			}
			s.batchReadableLabels(ctx, e, remaining, uuidToLabel)
			for k := range remaining {
				if _, done := uuidToLabel[k]; done {
					delete(remaining, k)
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
			id, err := uuid.Parse(v)
			if err != nil {
				continue
			}
			if label, found := uuidToLabel[id.String()]; found && label != "" {
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
	// Параметры текущего запроса — из них шаблоны строят ссылки «то же самое,
	// но с другим X» (listURL/listHidden), не собирая строку запроса по кускам
	// и не теряя при этом чужие параметры вроде q.
	if _, ok := data["Query"]; !ok && r != nil {
		data["Query"] = r.URL.Query()
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
		data["Subsystems"] = s.visibleSubsystems(r)
		data["CurrentSubsystem"] = sub
		// Скрытая глобальная «Главная» (issue #304): убрать ведущую ссылку из
		// панели разделов на всех страницах.
		data["HideHome"] = s.hideGlobalHome()
	}
	if _, ok := data["CollapsibleNav"]; !ok {
		data["CollapsibleNav"] = s.store.GetNavCollapsible(r.Context())
	}
	if _, ok := data["IsAdmin"]; !ok {
		data["IsAdmin"] = s.isAdmin(r)
	}
	// HasStages — в конфигурации есть хотя бы один объявленный маршрут этапов
	// (план 121). Без него пункт меню «где застряло» вёл бы на пустую страницу.
	if _, ok := data["HasStages"]; !ok {
		data["HasStages"] = s.hasStages()
	}
	// Строка глобального поиска в шапке есть на каждой странице (план 82);
	// на самой странице результатов она сохраняет введённый запрос.
	if _, ok := data["SearchQuery"]; !ok {
		data["SearchQuery"] = ""
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
		s.serverError(w, r, err)
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

// firstStringField — представление объекта по строке данных. Логика общая с
// REST и глобальным поиском, поэтому живёт в metadata; здесь остаётся имя,
// которым её зовут десятки мест UI.
func firstStringField(row map[string]any, e *metadata.Entity) string {
	return metadata.RowLabel(row, e)
}

var formNumberPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:[.,][0-9]+)?|[.,][0-9]+)$`)

// parseFormNumber разбирает пользовательское представление числа. В формах
// принимаются оба привычных десятичных разделителя (12.345 и 12,345), но не
// пропускаются буквы, несколько разделителей и прочий ввод, который PostgreSQL
// затем отклонил бы как invalid input syntax for type numeric.
func parseFormNumber(raw string) (decimal.Decimal, error) {
	value := strings.TrimSpace(raw)
	if !formNumberPattern.MatchString(value) {
		return decimal.Zero, i18nerr.Errorf("некорректное число %q: используйте цифры и один разделитель — запятую или точку", raw)
	}
	value = strings.Replace(value, ",", ".", 1)
	result, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, i18nerr.Errorf("некорректное число %q", raw)
	}
	return result, nil
}

// checkFormNumberFields валидирует числовые реквизиты до запуска hook-ов и
// транзакции записи, чтобы пользователь получил понятный ответ формы, а не
// техническую ошибку драйвера PostgreSQL.
func checkFormNumberFields(r *http.Request, entity *metadata.Entity) error {
	for _, f := range entity.Fields {
		if f.Type != metadata.FieldTypeNumber {
			continue
		}
		raw := r.FormValue(f.Name)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if _, err := parseFormNumber(raw); err != nil {
			return i18nerr.Errorf("поле %q: %v", f.Name, err)
		}
	}
	return nil
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
			if n, err := parseFormNumber(val); err == nil {
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
	if entity.Hierarchical {
		vals["parent_id"] = r.FormValue("parent_id")
		vals["is_folder"] = "false"
		if r.FormValue("is_folder") == "true" {
			vals["is_folder"] = "true"
		}
	}
	return vals
}

// resolveRefs replaces UUID values of reference fields with the display name
// of the referenced entity (first string field). Modifies rows in place.
//
// Each reference field is resolved with a single batched query (via batchLabels
// → GetFieldsByIDs) instead of one GetByID per unique id — this is a hot path
// (entity/document lists and export). План 111 (P1-1).
func (s *Server) resolveRefs(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		// collect unique referenced ids in this field (canonical string keys)
		idset := map[string]uuid.UUID{}
		for _, row := range rows {
			if idStr, id, ok := uuidFromValue(row[f.Name]); ok {
				idset[idStr] = id
			}
		}
		if len(idset) == 0 {
			continue
		}
		labels := make(map[string]string, len(idset))
		s.batchLabels(ctx, refEntity, idset, labels)
		// replace UUIDs with labels in all rows
		for _, row := range rows {
			if idStr, _, ok := uuidFromValue(row[f.Name]); ok {
				if label, ok := labels[idStr]; ok {
					row[f.Name] = label
				}
			}
		}
	}
}

// refLabelBatchSize bounds how many ids go into one GetFieldsByIDs IN(...) query.
// Keeps parameter counts under SQLite's 999-parameter cap on wide exports while
// still collapsing an N+1 into O(ids/batch) queries. План 111 (P1-1).
const refLabelBatchSize = 500

// batchLabels resolves a set of unique ids (canonical string → id) against one
// entity, writing display labels into out keyed by canonical id string. Ids not
// present in the entity are simply absent from out. Runs one query per batch of
// refLabelBatchSize ids. General entity lists use this historical target-RBAC/
// RLS-unrestricted helper; register-family UI resolvers use batchReadableLabels.
func (s *Server) batchLabels(ctx context.Context, e *metadata.Entity, idset map[string]uuid.UUID, out map[string]string) {
	if e == nil || len(idset) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(idset))
	for _, id := range idset {
		ids = append(ids, id)
	}
	fields := displayField(e)
	for start := 0; start < len(ids); start += refLabelBatchSize {
		end := start + refLabelBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		refRows, err := s.store.GetFieldsByIDs(ctx, e, ids[start:end], fields)
		if err != nil {
			return
		}
		for idStr, refRow := range refRows {
			out[idStr] = s.maskedRecordLabel(ctx, e, refRow)
		}
	}
}

// readableFieldsByIDs applies target-object RBAC and RLS before a UI resolver
// reads reference fields. Trusted DSL/internal contexts retain their historical
// unrestricted behaviour; unauthenticated/admin contexts are unrestricted through
// rowDecision as before. Every error is returned so callers can fail closed.
func (s *Server) readableFieldsByIDs(ctx context.Context, e *metadata.Entity, ids []uuid.UUID, fields []metadata.Field) (map[string]map[string]any, error) {
	var predicate *storage.Predicate
	if !isTrustedDSLContext(ctx) {
		decision, err := s.rowDecision(ctx, e, "read")
		if err != nil {
			return nil, err
		}
		if !decision.Allowed {
			return nil, fmt.Errorf("forbidden")
		}
		if !decision.Unrestricted {
			predicate = decision.Predicate
		}
	}
	return s.store.GetFieldsByIDsFiltered(ctx, e, ids, fields, predicate)
}

// batchReadableLabels preserves the O(ids/batch) label-resolution contract but
// performs target RLS in SQL. A denied target object, malformed policy or read
// error resolves no labels rather than falling back to an unrestricted query.
func (s *Server) batchReadableLabels(ctx context.Context, e *metadata.Entity, idset map[string]uuid.UUID, out map[string]string) {
	if e == nil || len(idset) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(idset))
	for _, id := range idset {
		ids = append(ids, id)
	}
	fields := displayField(e)
	for start := 0; start < len(ids); start += refLabelBatchSize {
		end := start + refLabelBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		refRows, err := s.readableFieldsByIDs(ctx, e, ids[start:end], fields)
		if err != nil {
			return
		}
		for idStr, refRow := range refRows {
			out[idStr] = s.maskedRecordLabel(ctx, e, refRow)
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
		idsByString := map[string]uuid.UUID{}
		for _, row := range rows {
			if _, v, ok := lookupMapCI(row, f.Name); ok {
				idStr, id, ok := uuidFromValue(v)
				if ok {
					idsByString[idStr] = id
				}
			}
		}
		if len(idsByString) == 0 {
			continue
		}
		ids := make([]uuid.UUID, 0, len(idsByString))
		for _, id := range idsByString {
			ids = append(ids, id)
		}
		refRows, err := s.store.GetFieldsByIDs(ctx, refEntity, ids, displayField(refEntity))
		if err != nil {
			continue
		}
		labels := make(map[string]string, len(refRows))
		for idStr, refRow := range refRows {
			labels[idStr] = s.maskedRecordLabel(ctx, refEntity, refRow)
		}
		// Replace plain UUID strings and rebind existing references to a manager
		// backed by the current context. A Ref copied into a new document before
		// Записать() may still carry a manager bound to the outer processor
		// context. Keeping that manager inside OnWrite makes ПолучитьОбъект()
		// request a second SQLite connection while the current transaction owns
		// the only one.
		mgr := s.refManagerFor(refEntity, ctx)
		for _, row := range rows {
			matchKey, v, ok := lookupMapCI(row, f.Name)
			if !ok || v == nil {
				continue
			}
			if ref, isRef := v.(*interpreter.Ref); isRef {
				if ref == nil {
					continue
				}
				name := ref.Name
				if label, ok := labels[ref.UUID]; ok {
					name = label
				}
				row[matchKey] = &interpreter.Ref{
					UUID: ref.UUID, Name: name, Type: refEntity.Name, Manager: mgr,
				}
				continue
			}
			idStr, _, ok := uuidFromValue(v)
			if !ok {
				continue
			}
			if name, ok := labels[idStr]; ok {
				row[matchKey] = &interpreter.Ref{UUID: idStr, Name: name, Type: refEntity.Name, Manager: mgr}
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
		if ref, isRef := matchVal.(*interpreter.Ref); isRef {
			if ref != nil {
				obj.Fields[matchKey] = &interpreter.Ref{
					UUID:    ref.UUID,
					Name:    ref.Name,
					Type:    refEntity.Name,
					Manager: s.refManagerFor(refEntity, ctx),
				}
			}
			continue
		}
		idStr := fmt.Sprintf("%v", matchVal)
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		refRows, err := s.store.GetFieldsByIDs(ctx, refEntity, []uuid.UUID{id}, displayField(refEntity))
		if err != nil {
			continue
		}
		refRow := refRows[id.String()]
		if refRow == nil {
			continue
		}
		obj.Fields[matchKey] = &interpreter.Ref{
			UUID:    idStr,
			Name:    s.maskedRecordLabel(ctx, refEntity, refRow),
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
			"Label": s.maskedRecordLabel(ctx, entity, row),
		})
	}
	return crumbs
}

// loadFolderOptions returns a bounded folder list for a hierarchical catalog parent select.
func (s *Server) loadFolderOptions(ctx context.Context, entity *metadata.Entity, selected ...string) []map[string]any {
	params, err := s.rowFilterFor(ctx, entity, "read", storage.ListParams{
		Limit:       refPickerDefaultLimit,
		OnlyFolders: true,
	})
	if err != nil {
		return nil
	}
	rows, err := s.store.List(ctx, entity.Name, entity, params)
	if err != nil {
		return nil
	}
	var folders []map[string]any
	for _, row := range rows {
		if asBool(row["is_folder"]) {
			row["_label"] = s.maskedRecordLabel(ctx, entity, row)
			folders = append(folders, row)
		}
	}
	return s.appendSelectedFolderOptions(ctx, folders, entity, selected)
}

func (s *Server) appendSelectedFolderOptions(ctx context.Context, rows []map[string]any, entity *metadata.Entity, selected []string) []map[string]any {
	seen := make(map[string]bool, len(rows)+len(selected))
	for _, row := range rows {
		if id := refValueString(row["id"]); id != "" {
			seen[id] = true
		}
	}
	for _, idStr := range selected {
		idStr = strings.TrimSpace(idStr)
		if idStr == "" || seen[idStr] {
			continue
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		row, err := s.store.GetByID(ctx, entity.Name, id, entity)
		if err != nil || row == nil || !asBool(row["is_folder"]) {
			continue
		}
		if !s.rowAllowsSelected(ctx, entity, row) {
			continue
		}
		row["_label"] = s.maskedRecordLabel(ctx, entity, row)
		rows = append(rows, row)
		seen[idStr] = true
	}
	return rows
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

// filterValue returns the FilterValue for a field from ListParams, or empty.
func filterValue(params storage.ListParams, fieldName string) storage.FilterValue {
	if params.Filters == nil {
		return storage.FilterValue{}
	}
	return params.Filters[fieldName]
}
