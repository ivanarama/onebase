package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
)

// Порядок колонок таблиц управляемой формы — за пользователем, как в 1С:
// перетащил колонку мышью — форма открывается с тем же порядком. Таблица
// адресуется ключом «вид/объект/форма/элемент» (processor/ПоискЗаявок/
// ФормаОбъекта/ТЧНайденныеЗаявки): сервер подставляет сохранённый порядок
// при отрисовке (без перестройки после загрузки), клиент сохраняет новый
// POST /ui/_grid-order.

const (
	gridOrderMaxColumns = 200
	gridOrderMaxIDLen   = 200
)

// gridLayoutBase — «вид/объект/форма» отрисовываемой управляемой формы.
func gridLayoutBase(data map[string]any, form *metadata.FormModule) string {
	if form == nil || form.Name == "" {
		return ""
	}
	if proc, _ := data["Processor"].(*processorpkg.Processor); proc != nil {
		return "processor/" + proc.Name + "/" + form.Name
	}
	if ent, _ := data["Entity"].(*metadata.Entity); ent != nil {
		return strings.ToLower(string(ent.Kind)) + "/" + ent.Name + "/" + form.Name
	}
	return ""
}

// prepareGridOrders кладёт в данные формы ключ таблиц и сохранённые порядки
// колонок текущего пользователя (только для таблиц, где он задан).
func (s *Server) prepareGridOrders(ctx context.Context, data map[string]any, form *metadata.FormModule) {
	base := gridLayoutBase(data, form)
	if base == "" || s.store == nil {
		return
	}
	data["GridLayoutBase"] = base
	user := ""
	if u := auth.UserFromContext(ctx); u != nil {
		user = u.Login
	}
	orders := map[string]string{}
	widths := map[string]string{}
	for _, el := range gridTableElements(form.Elements, nil) {
		if order := s.store.GetFormGridOrder(ctx, base+"/"+el.Name, user); len(order) > 0 {
			if raw, err := json.Marshal(order); err == nil {
				orders[el.Name] = string(raw)
			}
		}
		if w := s.store.GetFormGridWidths(ctx, base+"/"+el.Name, user); len(w) > 0 {
			if raw, err := json.Marshal(w); err == nil {
				widths[el.Name] = string(raw)
			}
		}
	}
	data["GridOrders"] = orders
	data["GridWidths"] = widths
}

// gridTableElements — элементы-таблицы формы (на любой глубине групп и страниц).
func gridTableElements(elements []*metadata.FormElement, out []*metadata.FormElement) []*metadata.FormElement {
	for _, el := range elements {
		if el == nil {
			continue
		}
		if el.Kind == metadata.FormElementTablePart {
			out = append(out, el)
		}
		out = gridTableElements(el.Children, out)
	}
	return out
}

// gridLayoutAttr / gridOrderAttr — значения data-sg-layout и data-sg-order для
// шаблона. Через функции, а не index: страницу рисуют и без
// prepareManagedFormData (предпросмотр, тесты), тогда ключей нет.
func gridLayoutAttr(ctx any, elName string) string {
	m, _ := ctx.(map[string]any)
	base, _ := m["GridLayoutBase"].(string)
	if base == "" || elName == "" {
		return ""
	}
	return base + "/" + elName
}

func gridOrderAttr(ctx any, elName string) string {
	m, _ := ctx.(map[string]any)
	orders, _ := m["GridOrders"].(map[string]string)
	return orders[elName]
}

// gridWidthsAttr — значение data-sg-widths: сохранённые ширины колонок.
func gridWidthsAttr(ctx any, elName string) string {
	m, _ := ctx.(map[string]any)
	widths, _ := m["GridWidths"].(map[string]string)
	return widths[elName]
}

// Ширина колонки, которую принимает сервер: уже 20 px колонку не ухватить
// мышью, шире 2000 px — не ширина, а ошибка или подбор ключа.
const (
	gridWidthMin = 20
	gridWidthMax = 2000
)

// gridWidthsSave — POST /ui/_grid-widths: grid=<вид/объект/форма/элемент>,
// widths=<JSON-объект «id колонки → ширина»>; пустой widths — ширины формы.
// Ключ проверяется так же, как у порядка колонок.
func (s *Server) gridWidthsSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	grid := strings.TrimSpace(r.FormValue("grid"))
	if !s.gridOrderKeyAllowed(r, grid) {
		http.Error(w, "таблица формы не найдена", http.StatusNotFound)
		return
	}
	var widths map[string]int
	if raw := strings.TrimSpace(r.FormValue("widths")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &widths); err != nil {
			http.Error(w, "widths: ожидался JSON-объект «колонка → ширина»", http.StatusBadRequest)
			return
		}
	}
	if len(widths) > gridOrderMaxColumns {
		http.Error(w, "widths: слишком много колонок", http.StatusBadRequest)
		return
	}
	for id, px := range widths {
		if id == "" || len(id) > gridOrderMaxIDLen || px < gridWidthMin || px > gridWidthMax {
			http.Error(w, "widths: некорректная колонка или ширина", http.StatusBadRequest)
			return
		}
	}
	if err := s.store.SaveFormGridWidths(r.Context(), grid, currentUserLogin(r), widths); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// gridOrderSave — POST /ui/_grid-order: grid=<вид/объект/форма/элемент>,
// order=<JSON-массив id колонок>; пустой order — стандартный порядок.
// Ключ проверяется по метаданным (форма и таблица существуют, у
// пользователя есть доступ к объекту): иначе в _settings можно было бы писать
// произвольные ключи.
func (s *Server) gridOrderSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	grid := strings.TrimSpace(r.FormValue("grid"))
	if !s.gridOrderKeyAllowed(r, grid) {
		http.Error(w, "таблица формы не найдена", http.StatusNotFound)
		return
	}
	var order []string
	if raw := strings.TrimSpace(r.FormValue("order")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &order); err != nil {
			http.Error(w, "order: ожидался JSON-массив строк", http.StatusBadRequest)
			return
		}
	}
	if len(order) > gridOrderMaxColumns {
		http.Error(w, "order: слишком много колонок", http.StatusBadRequest)
		return
	}
	for _, id := range order {
		if id == "" || len(id) > gridOrderMaxIDLen {
			http.Error(w, "order: некорректный id колонки", http.StatusBadRequest)
			return
		}
	}
	if err := s.store.SaveFormGridOrder(r.Context(), grid, currentUserLogin(r), order); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) gridOrderKeyAllowed(r *http.Request, grid string) bool {
	parts := strings.Split(grid, "/")
	if len(parts) != 4 {
		return false
	}
	kind, name, formName, elName := parts[0], parts[1], parts[2], parts[3]
	var form *metadata.FormModule
	switch kind {
	case "processor":
		proc := s.reg.GetProcessor(name)
		if proc == nil || !s.can(r, "processor", proc.Name, "run") {
			return false
		}
		form = proc.ManagedForm()
	default:
		ent := s.reg.GetEntity(name)
		if ent == nil || strings.ToLower(string(ent.Kind)) != kind || !s.can(r, string(ent.Kind), ent.Name, "read") {
			return false
		}
		for _, fm := range ent.Forms {
			if fm != nil && fm.Name == formName {
				form = fm
				break
			}
		}
	}
	if form == nil || form.Name != formName {
		return false
	}
	for _, el := range gridTableElements(form.Elements, nil) {
		if el.Name == elName {
			return true
		}
	}
	return false
}
