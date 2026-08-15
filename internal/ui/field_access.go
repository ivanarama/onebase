package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// fieldDecisions returns the effective per-field masking decisions for reading
// entity as the request user (nil/empty ⇒ nothing to mask).
func (s *Server) fieldDecisions(ctx context.Context, entity *metadata.Entity) map[string]access.FieldDecision {
	if entity == nil {
		return nil
	}
	return s.fieldDecisionsFor(ctx, string(entity.Kind), entity.Name, entity)
}

// fieldDecisionsFor is the shared field-policy adapter for objects that are not
// represented by a persisted metadata.Entity (information registers and
// heterogeneous journal rows). The synthetic metadata still canonicalises
// policy names to the exact row keys.
func (s *Server) fieldDecisionsFor(ctx context.Context, kind, name string, meta *metadata.Entity) map[string]access.FieldDecision {
	return access.FieldDecisions(auth.UserFromContext(ctx), kind, name, meta)
}

// maskRecord masks/hides sensitive fields of one record in place before it is
// rendered or serialised. Shared chokepoint for every UI read path.
func (s *Server) maskRecord(ctx context.Context, entity *metadata.Entity, row map[string]any) {
	access.MaskRecord(s.fieldDecisions(ctx, entity), row)
}

// maskRecords masks a list of records in place.
func (s *Server) maskRecords(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	access.MaskRecords(s.fieldDecisions(ctx, entity), rows)
}

// maskInfoRegRecords applies field_access.inforegs at the handler boundary,
// before reference UUIDs are resolved into labels or rows are rendered. Period
// has a second transport representation (period_key) used by delete forms; it
// must inherit the same decision or the protected value would remain in HTML.
func (s *Server) maskInfoRegRecords(ctx context.Context, ir *metadata.InfoRegister, rows []map[string]any) {
	if ir == nil {
		return
	}
	decisions := s.fieldDecisionsFor(ctx, "inforeg", ir.Name, storage.InfoRegisterPredicateEntity(ir))
	access.MaskRecords(decisions, rows)
	periodDecision, protected := fieldDecisionByName(decisions, "period")
	if !protected || !periodDecision.Masked() {
		return
	}
	for _, row := range rows {
		key, ok := rowKeyByName(row, "period_key")
		if !ok {
			continue
		}
		if periodDecision.Hidden() {
			delete(row, key)
			continue
		}
		row[key] = access.MaskValue(periodDecision.Strategy, periodDecision.Keep, row[key])
	}
}

// maskRegisterRecords applies field_access.registers at the handler boundary —
// до разрешения ссылочных UUID в представления и до рендера.
//
// #767 вывел маскирование на границы журналов и регистров СВЕДЕНИЙ, а списки
// регистров накопления остались без него: права на объект и строковый отбор
// применялись, а маска полей — нет. При этом та же политика честно работала в
// запросах DSL, то есть данные, скрытые от роли в отчёте, показывались ей же
// в /ui/register/* (#859).
func (s *Server) maskRegisterRecords(ctx context.Context, reg *metadata.Register, rows []map[string]any) {
	if reg == nil {
		return
	}
	access.MaskRecords(s.registerFieldDecisions(ctx, reg), rows)
}

// registerFieldDecisions centralises the synthetic accumulation-register
// metadata used by every UI and DSL read boundary. Keeping the decision map
// separate from MaskRecords also lets callers reject operations that would use
// a protected value as a filter or GROUP BY key before storage sees it.
func (s *Server) registerFieldDecisions(ctx context.Context, reg *metadata.Register) map[string]access.FieldDecision {
	if reg == nil {
		return nil
	}
	return s.fieldDecisionsFor(ctx, "register", reg.Name, storage.RegisterPredicateEntity(reg))
}

func registerFieldProtected(decisions map[string]access.FieldDecision, name string) bool {
	decision, ok := fieldDecisionByName(decisions, name)
	return ok && decision.Masked()
}

func registerFieldHidden(decisions map[string]access.FieldDecision, name string) bool {
	decision, ok := fieldDecisionByName(decisions, name)
	return ok && decision.Hidden()
}

func registerHasProtectedDimension(decisions map[string]access.FieldDecision, reg *metadata.Register) bool {
	if reg == nil {
		return false
	}
	for _, field := range reg.Dimensions {
		if registerFieldProtected(decisions, field.Name) {
			return true
		}
	}
	return false
}

// registerBalancesProtected covers every protected input that drives the
// aggregate: dimensions are GROUP BY keys and вид_движения chooses the sign of
// each resource. Masking only the final cells cannot close either oracle.
func registerBalancesProtected(decisions map[string]access.FieldDecision, reg *metadata.Register) bool {
	return registerHasProtectedDimension(decisions, reg) || registerFieldProtected(decisions, "вид_движения")
}

func unprotectedRegisterDimensions(decisions map[string]access.FieldDecision, fields []metadata.Field) []metadata.Field {
	result := make([]metadata.Field, 0, len(fields))
	for _, field := range fields {
		if !registerFieldProtected(decisions, field.Name) {
			result = append(result, field)
		}
	}
	return result
}

func visibleRegisterFields(decisions map[string]access.FieldDecision, fields []metadata.Field) []metadata.Field {
	result := make([]metadata.Field, 0, len(fields))
	for _, field := range fields {
		if !registerFieldHidden(decisions, field.Name) {
			result = append(result, field)
		}
	}
	return result
}

type registerMovementColumns struct {
	ShowLineNumber bool
	ShowKind       bool
	ShowRecorder   bool
}

func registerMovementColumnsFor(decisions map[string]access.FieldDecision) registerMovementColumns {
	return registerMovementColumns{
		ShowLineNumber: !registerFieldHidden(decisions, "line_number"),
		ShowKind:       !registerFieldHidden(decisions, "вид_движения"),
		// The rendered registrar is a compound value. Hiding either source must
		// remove the compound column rather than reconstruct it from the other.
		ShowRecorder: !registerFieldHidden(decisions, "recorder") &&
			!registerFieldHidden(decisions, "recorder_type"),
	}
}

// protectedRegisterFilterRequested closes the inference channel where a
// caller probes a masked dimension (or period) and observes whether the result
// set changes. Check the raw query before parsing or issuing any storage query:
// even a syntactically invalid protected filter must not be treated as absent.
func protectedRegisterFilterRequested(r *http.Request, reg *metadata.Register, decisions map[string]access.FieldDecision) bool {
	if r == nil || reg == nil {
		return false
	}
	query := r.URL.Query()
	for _, field := range reg.Dimensions {
		if registerFieldProtected(decisions, field.Name) && strings.TrimSpace(query.Get("flt_"+field.Name)) != "" {
			return true
		}
	}
	return registerFieldProtected(decisions, "period") &&
		(strings.TrimSpace(query.Get("from")) != "" || strings.TrimSpace(query.Get("to")) != "")
}

// maskJournalRecords maps each journal output column back to the source fields
// of the concrete document row, then applies that document's field policy to
// the output alias. This mapping is essential for explicit map/fallback journal
// columns: masking row[jcol.Field] directly would miss a protected source whose
// name differs from the journal alias.
func (s *Server) maskJournalRecords(
	ctx context.Context,
	j *metadata.Journal,
	docs map[string]*metadata.Entity,
	rows []map[string]any,
) {
	if j == nil || len(rows) == 0 {
		return
	}
	byDocument := make(map[string]map[string]access.FieldDecision, len(docs))
	for name, entity := range docs {
		if entity == nil {
			continue
		}
		sourceDecisions := s.fieldDecisionsFor(ctx, string(entity.Kind), entity.Name, entity)
		outputDecisions := make(map[string]access.FieldDecision)
		for _, column := range j.Columns {
			decision, ok := journalColumnDecision(sourceDecisions, journalColumnSourceFields(column, entity))
			if ok {
				outputDecisions[column.Field] = decision
			}
		}
		if len(outputDecisions) > 0 {
			byDocument[name] = outputDecisions
		}
	}
	for _, row := range rows {
		docName := asString(row["_doc_kind"])
		access.MaskRecord(byDocument[docName], row)
	}
}

// journalColumnSourceFields mirrors storage.colExprForDoc resolution order.
// Multiple fallback fields become COALESCE in SQL, so all are returned: without
// result provenance, the output must use the most restrictive applicable
// decision to avoid exposing whichever fallback happened to be non-NULL.
func journalColumnSourceFields(column metadata.JournalColumn, entity *metadata.Entity) []string {
	if entity == nil {
		return nil
	}
	if mapped, ok := column.Map[entity.Name]; ok && mapped != "" {
		if field, found := entityFieldByName(entity, mapped); found {
			return []string{field.Name}
		}
		return nil
	}
	if field, found := entityFieldByName(entity, column.Field); found {
		return []string{field.Name}
	}
	seen := make(map[string]bool)
	var fields []string
	for _, fallback := range column.Fallback {
		field, found := entityFieldByName(entity, fallback)
		if !found {
			continue
		}
		key := strings.ToLower(field.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		fields = append(fields, field.Name)
	}
	return fields
}

func journalColumnDecision(
	decisions map[string]access.FieldDecision,
	sources []string,
) (access.FieldDecision, bool) {
	var selected access.FieldDecision
	found := false
	for _, source := range sources {
		decision, ok := fieldDecisionByName(decisions, source)
		if !ok || !decision.Masked() {
			continue
		}
		decision.Strategy = journalMaskStrategy(decision.Strategy)
		if !found {
			selected = decision
			found = true
			continue
		}
		selected = combineJournalDecisions(selected, decision)
	}
	return selected, found
}

// combineJournalDecisions is deliberately not a total ordering. mask_city and
// mask_tail protect different shapes and cannot safely be applied to one
// another's values (mask_city may return a phone without commas unchanged).
// Ambiguous COALESCE provenance therefore collapses distinct masks to mask_all.
func combineJournalDecisions(current, candidate access.FieldDecision) access.FieldDecision {
	if current.Hidden() || candidate.Hidden() {
		return access.FieldDecision{Strategy: access.FieldHide}
	}
	if current.Strategy != candidate.Strategy {
		return access.FieldDecision{Strategy: access.FieldMaskAll}
	}
	if current.Strategy == access.FieldMaskTail && candidate.Keep < current.Keep {
		current.Keep = candidate.Keep
	}
	return current
}

func journalMaskStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case access.FieldHide:
		return access.FieldHide
	case access.FieldMaskAll:
		return access.FieldMaskAll
	case access.FieldMaskCity:
		return access.FieldMaskCity
	case access.FieldMaskTail:
		return access.FieldMaskTail
	default:
		// Unknown strategies fail closed as mask_all in access.MaskValue.
		return access.FieldMaskAll
	}
}

func fieldDecisionByName(decisions map[string]access.FieldDecision, name string) (access.FieldDecision, bool) {
	for field, decision := range decisions {
		if strings.EqualFold(field, name) {
			return decision, true
		}
	}
	return access.FieldDecision{}, false
}

func rowKeyByName(row map[string]any, name string) (string, bool) {
	for key := range row {
		if strings.EqualFold(key, name) {
			return key, true
		}
	}
	return "", false
}

// maskDSLValue applies the field policy to one attribute value read from a
// STORED object through a DSL proxy — Ссылка.ПолучитьОбъект(), НайтиПоНомеру(),
// разыменование Клиент.Телефон. Without it a processing run by a masked user is
// a trivial bypass: read the object, Сообщить(Об.Телефон).
//
// Прикладной код исполняется в правах пользователя, привилегированного режима в
// платформе нет: под ролью с маской защищённый реквизит приходит в модуль
// замаскированным — не используйте ПДн в расчётах проведения. Объект,
// создаваемый самим модулем (Создать()), и `this` формы/проведения не
// маскируются: там значение принадлежит текущей операции, а не чужой записи.
func (s *Server) maskDSLValue(ctx context.Context, entity *metadata.Entity, field string, v any) any {
	dec, ok := s.fieldDecisions(ctx, entity)[canonicalDSLField(entity, field)]
	if !ok || !dec.Masked() {
		return v
	}
	if dec.Hidden() {
		return nil
	}
	return access.MaskValue(dec.Strategy, dec.Keep, v)
}

// dslFieldSearchDenied reports whether searching by this attribute would turn a
// masked field into a guessing oracle: НайтиПоРеквизиту("Телефон", …) recovers
// the exact value the mask hides. Mirrors the query gate, where a protected
// field in ГДЕ denies the whole query.
func (s *Server) dslFieldSearchDenied(ctx context.Context, entity *metadata.Entity, field string) bool {
	dec, ok := s.fieldDecisions(ctx, entity)[canonicalDSLField(entity, field)]
	return ok && dec.Masked()
}

// canonicalDSLField приводит имя реквизита из DSL к имени поля метаданных:
// решения FieldDecisions ключуются каноническим именем.
func canonicalDSLField(entity *metadata.Entity, field string) string {
	if f, ok := entityFieldByName(entity, field); ok {
		return f.Name
	}
	return strings.TrimSpace(field)
}

// maskedRecordLabel is the only safe way to derive a user-visible label from
// a freshly loaded record. Reference resolvers often read a row vertically
// (UUID → label), bypassing the list/form masking chokepoints; mask first so a
// sensitive first string field cannot leak through reports, widgets or audit.
func (s *Server) maskedRecordLabel(ctx context.Context, entity *metadata.Entity, row map[string]any) string {
	s.maskRecord(ctx, entity, row)
	return firstStringField(row, entity)
}

// queryMaskPlan is the report/widget/AI field gate (план 88E). A protected field
// in a plain projection column is masked in the result; one that drives a
// filter, grouping or aggregate — where an output mask protects nothing — still
// denies the whole query (plan.Denied).
func (s *Server) queryMaskPlan(ctx context.Context, res query.Result) access.QueryMaskPlan {
	return access.QueryMaskPlanFor(auth.UserFromContext(ctx), res, s.sourceMeta)
}

// dslQueryGuard applies the query field gate to `Новый Запрос` inside modules:
// без него обработка читает защищённые значения запросом в обход маски, которую
// тот же пользователь видит в отчёте (план 88E).
func (s *Server) dslQueryGuard(ctx context.Context, res query.Result, rows []map[string]any) error {
	plan := s.queryMaskPlan(ctx, res)
	if plan.Denied != "" {
		return fmt.Errorf("нет доступа к защищённому полю: %s", plan.Denied)
	}
	return plan.Apply(rows)
}

// sourceMeta resolves the metadata of a query source object (entity/register/
// inforeg/account register); nil if unknown.
func (s *Server) sourceMeta(kind, name string) *metadata.Entity {
	if e := s.reg.GetEntity(name); e != nil {
		return e
	}
	if r := s.reg.GetRegister(name); r != nil {
		return storage.RegisterPredicateEntity(r)
	}
	if ir := s.reg.GetInfoRegister(name); ir != nil {
		return storage.InfoRegisterPredicateEntity(ir)
	}
	if ar := s.reg.GetAccountRegister(name); ar != nil {
		return storage.AccountRegisterPredicateEntity(ar)
	}
	return nil
}

// protectMaskedFieldsOnWrite restores the real stored value for any field masked
// or hidden for this user before an update, so a user who only ever saw the mask
// cannot overwrite the real value — neither with the mask itself nor with a
// crafted request. Consistent with «нельзя изменить то, что не видно». Applied on
// update only; on create the user legitimately enters their own values.
//
// Возвращает имена ключей, которые были восстановлены или удалены. Вызывающий
// обязан учесть, что в переданной карте после этого лежит РЕАЛЬНОЕ значение: для
// DSL-объекта это тот же набор, который читает модуль, и без снятия признака
// «присвоено» защита записи сама стала бы каналом раскрытия.
func (s *Server) protectMaskedFieldsOnWrite(ctx context.Context, entity *metadata.Entity, id uuid.UUID, fields map[string]any) ([]string, error) {
	dec := s.fieldDecisions(ctx, entity)
	if len(dec) == 0 || fields == nil {
		return nil, nil
	}
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		return nil, err
	}
	var restored []string
	for field := range dec {
		key, ok := maskCIKey(fields, field)
		if !ok {
			continue // field not submitted → nothing to overwrite
		}
		if v, present := maskCIKeyValue(row, field); present {
			fields[key] = v
		} else {
			delete(fields, key)
		}
		restored = append(restored, key)
	}
	return restored, nil
}

// discloseField serves POST /ui/{kind}/{entity}/{id}/disclose with form fields
// {field, reason}. It enforces the object-level `disclose` right, records an
// audit event without the value (план 88, CC-SEC-004) and returns the full value
// inline as JSON so the form can reveal it without a reload.
func (s *Server) discloseField(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	field, ok := entityFieldByName(entity, r.FormValue("field"))
	if !ok {
		http.Error(w, "unknown field", http.StatusNotFound)
		return
	}
	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusNotFound)
		return
	}
	if !s.rowAllowed(w, r, entity, "read", row) {
		return // rowAllowed already rendered 403
	}
	_, masked := s.fieldDecisions(r.Context(), entity)[field.Name]
	if masked {
		if !s.can(r, string(entity.Kind), entity.Name, "disclose") {
			s.renderForbidden(w, r)
			return
		}
		reason := strings.TrimSpace(r.FormValue("reason"))
		if reason == "" {
			http.Error(w, "reason required", http.StatusBadRequest)
			return
		}
		// Fail-closed (CC-SEC-004): раскрытие ПДн без успешной записи в аудит
		// недопустимо — если журнал недоступен, значение клиенту не выдаём.
		if err := s.store.LogDisclose(r.Context(), string(entity.Kind), entity.Name, id.String(), field.Name, reason); err != nil {
			http.Error(w, "audit failed", http.StatusInternalServerError)
			return
		}
	}
	full, _ := maskCIKeyValue(row, field.Name)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"field":     field.Name,
		"value":     full,
		"disclosed": masked,
	})
}

func entityFieldByName(entity *metadata.Entity, name string) (metadata.Field, bool) {
	name = strings.TrimSpace(name)
	if entity == nil || name == "" {
		return metadata.Field{}, false
	}
	for _, f := range entity.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return metadata.Field{}, false
}

func maskCIKey(m map[string]any, field string) (string, bool) {
	for k := range m {
		if strings.EqualFold(k, field) {
			return k, true
		}
	}
	return "", false
}

func maskCIKeyValue(m map[string]any, field string) (any, bool) {
	for k, v := range m {
		if strings.EqualFold(k, field) {
			return v, true
		}
	}
	return nil, false
}

// maskedInfoRegDimensions перечисляет ИЗМЕРЕНИЯ регистра сведений, которые
// политика полей скрывает или маскирует для текущего пользователя.
//
// Измерения — это ключ записи. Пока хоть одно из них закрыто, роль физически не
// может назвать удаляемую строку: то, что она видит, — не ключ, а маска.
// Поэтому вызывающий обязан отказать в операции, а не пытаться удалить по
// маскированному значению (#861).
func (s *Server) maskedInfoRegDimensions(ctx context.Context, ir *metadata.InfoRegister) []string {
	if ir == nil {
		return nil
	}
	decisions := s.fieldDecisionsFor(ctx, "inforeg", ir.Name, storage.InfoRegisterPredicateEntity(ir))
	if len(decisions) == 0 {
		return nil
	}
	var masked []string
	for _, dim := range ir.Dimensions {
		if d, ok := fieldDecisionByName(decisions, dim.Name); ok && d.Masked() {
			masked = append(masked, dim.Name)
		}
	}
	return masked
}
