package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Чтение регистра сведений в REST (issue #1423). Внешнему клиенту регистр был
// виден только косвенно — через отчёт, — поэтому матрицу «измерение ×
// измерение» приходилось дублировать справочником-обёрткой.
//
// Контракт сознательно read-only и только для регистра сведений: запись,
// регистры накопления, виртуальные таблицы (СрезПоследних) и константы
// остаются отдельными решениями. Права те же, что в UI: объектный RBAC
// `inforeg`, строковые политики и маскирование полей.

// inforegFilterPrefix — префикс отбора по измерению: filter[Измерение]=…,
// filter[period.from]=…, filter[period.to]=… — та же форма, что у списков
// объектов v2, чтобы клиенту не пришлось помнить два синтаксиса.
const inforegFilterPrefix = "filter["

// inforegPeriodField — имя поля периода в отборе и в выдаче.
const inforegPeriodField = "period"

func (h *handler) listInfoRegV2() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := capitalize(chi.URLParam(r, "name"))
		ir := h.reg.GetInfoRegister(name)
		if ir == nil {
			writeError(w, http.StatusNotFound, "unknown info register: "+name, "", 0)
			return
		}
		if !canREST(r.Context(), "inforeg", ir.Name, "read") {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		meta := storage.InfoRegisterPredicateEntity(ir)
		decisions := h.fieldDecisions(r.Context(), meta)

		filters, err := parseInfoRegFilters(r, ir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "", 0)
			return
		}
		// Отбор по защищённому измерению закрывается ДО запроса к базе. Иначе
		// остаётся канал вывода: значение замаскировано в выдаче, но по тому,
		// изменился ли набор строк, его можно подобрать перебором. Тот же
		// довод, что у protectedRegisterFilterRequested в UI.
		if protected := protectedInfoRegFilter(filters, decisions); protected != "" {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}

		limit, offset, page, err := parseInfoRegPaging(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "", 0)
			return
		}

		flt := filters.regFilter()
		dec, err := h.rowDecision(r.Context(), meta, "read")
		if err != nil || !dec.Allowed {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		if !dec.Unrestricted {
			flt.RowFilter = dec.Predicate
		}

		total, err := h.store.InfoRegCount(r.Context(), ir, flt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		rows, err := h.store.InfoRegPage(r.Context(), ir, flt, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		// Маскирование — на границе обработчика, как в UI: значение защищённого
		// поля не должно уехать в JSON ни одной строкой.
		access.MaskRecords(decisions, rows)
		if rows == nil {
			rows = []map[string]any{}
		}

		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		w.Header().Set("X-Limit", strconv.Itoa(limit))
		w.Header().Set("X-Offset", strconv.Itoa(offset))
		writeJSONV2(w, http.StatusOK, restV2Envelope{
			Data: rows,
			Meta: &restV2Meta{
				Total:      total,
				Page:       page,
				Limit:      limit,
				TotalPages: totalPages(total, limit),
			},
		})
	}
}

// infoRegFilters — разобранный отбор: значения измерений и границы периода.
type infoRegFilters struct {
	dims      map[string]string // точное имя измерения → строковое значение
	dimValues map[string]any    // точное имя измерения → типизированное значение
	from      *time.Time
	to        *time.Time
	period    bool // задана хотя бы одна граница периода
}

func (f infoRegFilters) regFilter() storage.RegFilter {
	return storage.RegFilter{Dims: f.dims, DimValues: f.dimValues, From: f.from, To: f.to}
}

// parseInfoRegFilters принимает только имена измерений самого регистра и
// period у периодического. Неизвестный ключ — ошибка, а не тихо пропущенный
// отбор: молча вернуть весь регистр на запрос с опечаткой хуже, чем отказать,
// потому что клиент примет полную выдачу за отфильтрованную.
func parseInfoRegFilters(r *http.Request, ir *metadata.InfoRegister) (infoRegFilters, error) {
	out := infoRegFilters{dims: map[string]string{}, dimValues: map[string]any{}}
	for key, vals := range r.URL.Query() {
		if !strings.HasPrefix(key, inforegFilterPrefix) || !strings.HasSuffix(key, "]") || len(vals) == 0 {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(key, inforegFilterPrefix), "]")
		field, attr := splitFilterKey(inner)
		value := strings.TrimSpace(vals[0])
		if strings.EqualFold(field, inforegPeriodField) {
			if !ir.Periodic {
				return out, errInfoRegUnknownFilter(field)
			}
			if value == "" {
				continue
			}
			t, ok := parseInfoRegDate(value)
			if !ok {
				return out, errInfoRegBadDate(inner)
			}
			out.period = true
			switch attr {
			case "from":
				out.from = &t
			case "to":
				// Граница «по дату» включает весь день: period <= to
				// сравнивается с TIMESTAMP. Считаем как полночь следующего дня
				// минус наносекунда — в зонах с переходом часов это вернее,
				// чем +24 часа (тот же расчёт, что в UI).
				y, mo, da := t.Date()
				end := time.Date(y, mo, da+1, 0, 0, 0, 0, t.Location()).Add(-time.Nanosecond)
				out.to = &end
			default:
				return out, errInfoRegBadDate(inner)
			}
			continue
		}
		dim, ok := infoRegDimension(ir, field)
		if !ok {
			return out, errInfoRegUnknownFilter(field)
		}
		if attr != "value" {
			// from/to осмысленны только у периода: измерение сравнивается на
			// равенство, диапазона по нему нет.
			return out, errInfoRegUnknownFilter(inner)
		}
		if value != "" {
			if dim.Type == metadata.FieldTypeDate {
				t, ok := parseInfoRegDate(value)
				if !ok {
					return out, errInfoRegBadDate(dim.Name)
				}
				out.dimValues[dim.Name] = t
			} else {
				out.dims[dim.Name] = value
			}
		}
	}
	return out, nil
}

func infoRegDimension(ir *metadata.InfoRegister, name string) (metadata.Field, bool) {
	for _, d := range ir.Dimensions {
		if strings.EqualFold(d.Name, name) {
			return d, true
		}
	}
	return metadata.Field{}, false
}

// protectedInfoRegFilter возвращает имя первого защищённого поля, по которому
// запрошен отбор (пустая строка — таких нет).
func protectedInfoRegFilter(f infoRegFilters, decisions map[string]access.FieldDecision) string {
	if len(decisions) == 0 {
		return ""
	}
	for name := range f.dims {
		if infoRegFieldMasked(decisions, name) {
			return name
		}
	}
	for name := range f.dimValues {
		if infoRegFieldMasked(decisions, name) {
			return name
		}
	}
	if f.period && infoRegFieldMasked(decisions, inforegPeriodField) {
		return inforegPeriodField
	}
	return ""
}

func infoRegFieldMasked(decisions map[string]access.FieldDecision, name string) bool {
	for field, dec := range decisions {
		if strings.EqualFold(field, name) && dec.Masked() {
			return true
		}
	}
	return false
}

// parseInfoRegDate принимает машинную дату: ISO-день или полный RFC3339.
// Формат «02.01.2006» из HTML-формы здесь намеренно не поддержан — снаружи
// он неоднозначен и зависит от локали клиента.
func parseInfoRegDate(s string) (time.Time, bool) {
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func parseInfoRegPaging(r *http.Request) (limit, offset, page int, err error) {
	q := r.URL.Query()
	limit, err = parsePositiveInt(q.Get("limit"), restDefaultLimit)
	if err != nil {
		return 0, 0, 0, errInvalidLimit
	}
	if limit > restMaxLimit {
		limit = restMaxLimit
	}
	page, err = parsePositiveInt(q.Get("page"), 1)
	if err != nil {
		return 0, 0, 0, errInvalidPage
	}
	offset = (page - 1) * limit
	if q.Get("offset") != "" {
		offset, err = parseNonNegativeInt(q.Get("offset"), 0)
		if err != nil {
			return 0, 0, 0, errInvalidOffset
		}
		page = offset/limit + 1
	}
	return limit, offset, page, nil
}

// Ошибки разбора запроса — значениями, а не fmt.Errorf на месте: текст уходит
// клиенту в теле ответа и не должен зависеть от порядка ключей в map.
var (
	errInvalidLimit  = restBadRequest("invalid limit")
	errInvalidPage   = restBadRequest("invalid page")
	errInvalidOffset = restBadRequest("invalid offset")
)

func errInfoRegUnknownFilter(field string) error {
	return restBadRequest("unknown filter: " + field)
}

func errInfoRegBadDate(field string) error {
	return restBadRequest("invalid date filter: " + field)
}

// restBadRequest — короткий конструктор ошибки разбора.
func restBadRequest(msg string) error { return &restRequestError{msg: msg} }

type restRequestError struct{ msg string }

func (e *restRequestError) Error() string { return e.msg }
