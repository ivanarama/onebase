package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/widget"
)

// План 182D: интерактивные фильтры list-виджета. Значение приходит только по
// именаосванным ключам w.<base64url(виджет)>.<base64url(фильтр)> — два виджета
// страницы не делят параметры, точки/пробелы/Unicode в именах однозначны.
// Тип и границы сервер перечитывает из метаданных; текст запроса значениями не
// правится — только CompileOpts.Params.

// widgetFilterKey строит именаосванный ключ URL фильтра.
func widgetFilterKey(widgetName, filterName string) string {
	return "w." + base64.RawURLEncoding.EncodeToString([]byte(widgetName)) +
		"." + base64.RawURLEncoding.EncodeToString([]byte(filterName))
}

// widgetFilterKeys возвращает множество допустимых ключей запроса виджета.
func widgetFilterKeys(w *metadata.Widget) map[string]metadata.WidgetFilter {
	out := make(map[string]metadata.WidgetFilter, len(w.Filters))
	for _, f := range w.Filters {
		out[widgetFilterKey(w.Name, f.Name)] = f
	}
	return out
}

// errWidgetFilter — граница 400 для partial endpoint: любое нарушение контракта
// значений фильтров неотличимо для клиента.
var errWidgetFilter = errors.New("unsupported widget parameter")

// parseWidgetFilterParams разбирает значения фильтров виджета из запроса.
// Отсутствующий и пустой ключ одинаково дают типизированный nil. Ошибка
// возвращается на лишний (для этого виджета) именаосванный ключ и на значение,
// не прошедшее типовую проверку.
func parseWidgetFilterParams(r *http.Request, w *metadata.Widget) (map[string]any, error) {
	keys := widgetFilterKeys(w)
	if len(keys) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(keys))
	seen := make(map[string]bool, len(keys))
	for key, vals := range r.URL.Query() {
		f, ok := keys[key]
		if !ok {
			if strings.HasPrefix(key, "w.") {
				// Именаосванный ключ чужого/устаревшего фильтра — строгий отказ:
				// контроллер такие ключи не присылает.
				return nil, errWidgetFilter
			}
			continue // не наш формат (например subsystem)
		}
		seen[key] = true
		raw := ""
		if len(vals) > 0 {
			raw = vals[0]
		}
		v, err := widget.ParseFilterValue(f, raw)
		if err != nil {
			return nil, errWidgetFilter
		}
		out[f.Param] = v
	}
	for key, f := range keys {
		if !seen[key] {
			out[f.Param] = nil
		}
	}
	return out, nil
}

// widgetFilterRawValues возвращает каноничные сырые значения фильтров виджета
// из запроса (для сборки PartialURL и предзаполнения контролов). Полная
// страница устойчива к мусору в URL: непарсируемое значение трактуется как
// пустой отбор, страница продолжает работать.
func widgetFilterRawValues(r *http.Request, w *metadata.Widget) map[string]string {
	keys := widgetFilterKeys(w)
	out := make(map[string]string, len(keys))
	for key, f := range keys {
		vals := r.URL.Query()[key]
		if len(vals) == 0 || vals[0] == "" {
			continue
		}
		if _, err := widget.ParseFilterValue(f, vals[0]); err != nil {
			continue
		}
		out[key] = vals[0]
	}
	return out
}

// filterControls строит презентационные модели контролов фильтров для карточки.
// Options нужны только селектам: bool — три состояния, select — объявленные
// значения, reference — первая страница RLS-aware опций плюс текущее значение,
// если оно не вошло в страницу.
func (s *Server) filterControls(ctx context.Context, lang string, w *metadata.Widget, current map[string]string, runner *widget.Runner) ([]widget.FilterControl, error) {
	if len(w.Filters) == 0 {
		return nil, nil
	}
	controls := make([]widget.FilterControl, 0, len(w.Filters))
	for _, f := range w.Filters {
		key := widgetFilterKey(w.Name, f.Name)
		ctl := widget.FilterControl{
			Key:     key,
			Name:    f.Name,
			Label:   f.DisplayLabel(lang),
			Current: current[key],
		}
		switch {
		case f.ReferenceEntity() != "":
			ctl.Kind = "reference"
			opts, err := s.referenceFilterOptions(ctx, lang, f, ctl.Current, runner)
			if err != nil {
				return nil, err
			}
			ctl.Options = opts
		case f.Type == "bool":
			ctl.Kind = "bool"
			ctl.Options = []widget.FilterOption{
				{Value: "", Label: s.tr(lang, "Все")},
				{Value: "true", Label: s.tr(lang, "Да")},
				{Value: "false", Label: s.tr(lang, "Нет")},
			}
		case f.Type == "select":
			ctl.Kind = "select"
			for _, v := range f.Values {
				ctl.Options = append(ctl.Options, widget.FilterOption{Value: v.Value, Label: v.DisplayLabel(lang)})
			}
		default:
			ctl.Kind = f.Type
		}
		controls = append(controls, ctl)
	}
	return controls, nil
}

// referenceFilterOptions возвращает опции reference-фильтра: первая страница
// RLS-aware опций сущности; выбранное значение, не попавшее в страницу,
// дозапрашивается отдельно и не раскрывает недоступные записи.
func (s *Server) referenceFilterOptions(ctx context.Context, lang string, f metadata.WidgetFilter, currentID string, runner *widget.Runner) ([]widget.FilterOption, error) {
	entity := s.reg.GetEntity(f.ReferenceEntity())
	if entity == nil {
		return nil, nil
	}
	rows, err := s.referenceOptions(ctx, entity, refOptionsChoice)
	if err != nil {
		return nil, err
	}
	const maxOptions = 50
	if len(rows) > maxOptions {
		rows = rows[:maxOptions]
	}
	opts := make([]widget.FilterOption, 0, len(rows)+1)
	opts = append(opts, widget.FilterOption{Value: "", Label: s.tr(lang, "Все")})
	for _, row := range rows {
		id, _ := row["id"].(string)
		if id == "" {
			continue
		}
		opts = append(opts, widget.FilterOption{Value: id, Label: firstStringField(row, entity)})
	}
	// Текущее значение вне первой страницы дозапрашивается отдельно: ссылку
	// показываем подписью только если она read-доступна этому пользователю
	// (права + RLS); недоступная и несуществующая не отличимы — просто нет
	// подписи, выбор всё равно серверно проверяется при исполнении.
	if currentID != "" {
		found := false
		for _, o := range opts {
			if o.Value == currentID {
				found = true
				break
			}
		}
		if !found && runner != nil && runner.ReferenceAllowed(ctx, entity.Name, currentID) {
			if uid, perr := uuid.Parse(currentID); perr == nil {
				if row, gerr := s.store.GetByID(ctx, entity.Name, uid, entity); gerr == nil && row != nil {
					s.maskRecords(ctx, entity, []map[string]any{row})
					opts = append(opts, widget.FilterOption{Value: currentID, Label: firstStringField(row, entity)})
				}
			}
		}
	}
	return opts, nil
}

// widgetPartialURL собирает адрес partial-endpoint карточки с учётом
// подсистемы и текущих значений фильтров.
func widgetPartialURL(name string, w *metadata.Widget, subsystem string, rawValues map[string]string) string {
	q := make(url.Values)
	if subsystem != "" {
		q.Set("subsystem", subsystem)
	}
	for key, val := range rawValues {
		q.Set(key, val)
	}
	u := "/ui/_widget/" + url.PathEscape(name)
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}
	return u
}

// lenientWidgetFilterParams возвращает типизированные значения фильтров виджета
// из запроса для ПОЛНОЙ страницы: мусорное значение трактуется как пустой
// отбор (в отличие от partial-endpoint, где контракт строгий и даёт 400).
// Отсутствующие фильтры получают типизированный nil — канонический шаблон
// «&Параметр ЕСТЬ ПУСТО ИЛИ ...» ожидает параметр всегда.
func lenientWidgetFilterParams(r *http.Request, w *metadata.Widget) map[string]any {
	if len(w.Filters) == 0 {
		return nil
	}
	raw := widgetFilterRawValues(r, w)
	out := make(map[string]any, len(w.Filters))
	for _, f := range w.Filters {
		key := widgetFilterKey(w.Name, f.Name)
		if v, ok := raw[key]; ok {
			if typed, err := widget.ParseFilterValue(f, v); err == nil {
				out[f.Param] = typed
				continue
			}
		}
		out[f.Param] = nil
	}
	return out
}
