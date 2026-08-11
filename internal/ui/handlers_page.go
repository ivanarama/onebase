package ui

// Произвольные страницы на DSL (план 66): /ui/page/{name}. Метаданные —
// pages/<имя>.yaml, обработчик — src/<имя>.page.os (Процедура
// ПриФормировании(Страница, Параметры) Экспорт). Обработчик наполняет
// построитель «Страница» блоками, которые рендерятся в общую оболочку.

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/widget"
)

// pageChartData конвертирует чарт-блок страницы (план 66) в widget.ChartData,
// чтобы переиспользовать echartsJSON/EChartsOption (та же отрисовка, что у
// виджетов рабочего стола). Подключается в FuncMap как "pageChart".
func pageChartData(c *interpreter.PageChart) *widget.ChartData {
	if c == nil {
		return nil
	}
	cd := &widget.ChartData{Kind: c.Kind, XAxis: c.XAxis}
	for _, s := range c.Series {
		cd.Series = append(cd.Series, widget.ChartSeries{Name: s.Name, Data: s.Data})
	}
	return cd
}

// canSeePage сообщает, видна ли страница пользователю по ролям. Пустые roles —
// видна всем; nil-пользователь (аутентификация не настроена) — видна; иначе
// требуется одна из ролей (администратор проходит через HasAnyRole).
func (s *Server) canSeePage(r *http.Request, pg *page.Page) bool {
	if len(pg.Roles) == 0 {
		return true
	}
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return true
	}
	return u.HasAnyRole(pg.Roles)
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	// chi отдаёт сырой сегмент пути, когда Go выставил RawPath (percent-encoding
	// в нижнем регистре hex — именно такие ссылки строит меню: /ui/page/%d0%9f…).
	// Без декода GetPage не найдёт страницу → 404 из меню, хотя верхний регистр
	// (%D0%9F…) проходит. См. decodePathParam.
	name := decodePathParam(chi.URLParam(r, "name"))
	pg := s.reg.GetPage(name)
	if pg == nil {
		http.NotFound(w, r)
		return
	}

	// Роли (как у HTTP-сервисов): аутентифицированный пользователь без нужной
	// роли страницы не видит. nil-пользователь (аутентификация не настроена) —
	// открытый доступ, как и в can(). Администратор проходит (HasAnyRole).
	if len(pg.Roles) > 0 {
		if u := auth.UserFromContext(r.Context()); u != nil && !u.HasAnyRole(pg.Roles) {
			s.renderForbidden(w, r)
			return
		}
	}

	lang := s.resolveLang(r)
	title := pg.DisplayName(lang)

	proc := s.reg.GetPageProcedure(pg.Name, "ПриФормировании")
	if proc == nil {
		s.render(w, r, "page-custom", map[string]any{
			"PageTitle": title,
			"PageError": s.tr(lang, "обработчик ПриФормировании не найден в") + " src/" + strings.ToLower(pg.Name) + ".page.os",
		})
		return
	}

	var msgs []string
	builder, paramsObj, dslVars, txState := s.pageProcEnv(r, &msgs)
	defer rollbackDSLExecution(txState)

	_, runErr := s.interp.Call(proc, builder, []any{builder, paramsObj}, dslVars)
	if runErr = finishDSLExecution(txState, runErr); runErr != nil {
		s.render(w, r, "page-custom", map[string]any{
			"PageTitle": title,
			"PageError": s.errText(r, runErr),
		})
		return
	}

	blocks := builder.Blocks()
	s.localizePageBlocks(lang, blocks)
	hasChart := false
	for _, b := range blocks {
		if b.Kind == "chart" {
			hasChart = true
			break
		}
	}
	s.render(w, r, "page-custom", map[string]any{
		"PageTitle":      title,
		"PageBlocks":     blocks,
		"PageHasChart":   hasChart,
		"PageActionBase": "/ui/page/" + pg.Name + "/action/",
		"PageQuery":      pageQuery(r),
	})
}

// localizePageBlocks переводит статические авторские подписи блоков на язык
// пользователя через s.tr (i18n.Bundle, русский-как-ключ): заголовки, тексты
// абзацев, подписи показателей, заголовки таблиц/списков/графиков, заголовки
// колонок, тексты пунктов списка и кнопок (план 66, доработка 3). Данные НЕ
// трогаем — значения показателей (Value), ячейки таблиц (Rows) и данные
// графиков приходят из запросов и переводу не подлежат. Bundle.T возвращает
// непереведённый ключ как есть, поэтому для русской локали (и без словаря) это
// no-op. Динамически собранные подписи («Отчёт за » + Период) ключа не имеют и
// тоже проходят без изменений — для них автор может выделить статическую часть.
func (s *Server) localizePageBlocks(lang string, blocks []interpreter.PageBlock) {
	if s.cfg.Bundle == nil || lang == "" {
		return
	}
	for i := range blocks {
		b := &blocks[i]
		switch b.Kind {
		case "heading", "paragraph", "button":
			b.Text = s.tr(lang, b.Text)
		case "kpi":
			b.Label = s.tr(lang, b.Label)
		case "table":
			b.Title = s.tr(lang, b.Title)
			// Переводим ОТОБРАЖАЕМЫЕ заголовки (ColumnLabels); Columns — ключи
			// адресации ячеек, их трогать нельзя (см. колонки в page_builtins.go).
			for j := range b.ColumnLabels {
				b.ColumnLabels[j] = s.tr(lang, b.ColumnLabels[j])
			}
		case "list":
			b.Title = s.tr(lang, b.Title)
			for j := range b.Items {
				b.Items[j].Text = s.tr(lang, b.Items[j].Text)
			}
		case "chart":
			b.Title = s.tr(lang, b.Title)
		}
	}
}

// pageAction обрабатывает кнопку-действие (план 66): POST
// /ui/page/{name}/action/{action} вызывает серверную процедуру-действие из
// src/<имя>.page.os (любую процедуру по имени, как ПриФормировании), затем
// PRG-редиректом возвращает на GET страницы с теми же Параметрами. Сообщить()
// уже в сторе сообщений — его покажет глобальный бар. PRG исключает повторный
// запуск действия при F5 (важно для проведения/пересчёта).
func (s *Server) pageAction(w http.ResponseWriter, r *http.Request) {
	name := decodePathParam(chi.URLParam(r, "name"))
	action := decodePathParam(chi.URLParam(r, "action"))
	pg := s.reg.GetPage(name)
	if pg == nil {
		http.NotFound(w, r)
		return
	}
	// Роли — как в s.page: действие доступно только тем, кому видна страница.
	if len(pg.Roles) > 0 {
		if u := auth.UserFromContext(r.Context()); u != nil && !u.HasAnyRole(pg.Roles) {
			s.renderForbidden(w, r)
			return
		}
	}

	// Действие — это КнопкаДействие, а не lifecycle-обработчик: запрещаем дёргать
	// ПриФормировании напрямую (он отрабатывает при GET-формировании страницы),
	// чтобы его побочные эффекты нельзя было вызвать в обход назначения.
	if strings.EqualFold(action, "ПриФормировании") {
		http.NotFound(w, r)
		return
	}

	proc := s.reg.GetPageProcedure(pg.Name, action)
	if proc == nil {
		http.NotFound(w, r)
		return
	}
	// Экспорт-гейт (ревью #10): через действие страницы разрешаем вызывать ТОЛЬКО
	// процедуры, помеченные «Экспорт» — это публичные точки входа (кнопки-действия).
	// Внутренние вспомогательные процедуры модуля .page.os (без модификатора) —
	// часть реализации страницы; их побочные эффекты нельзя дёргать POST-запросом.
	// Lifecycle-процедура ПриФормировании работает на рендере (s.page) этим путём
	// не ходит и здесь уже отсечена выше.
	if !proc.Export {
		http.NotFound(w, r)
		return
	}

	var msgs []string
	builder, paramsObj, dslVars, txState := s.pageProcEnv(r, &msgs)
	defer rollbackDSLExecution(txState)
	_, runErr := s.interp.Call(proc, builder, []any{builder, paramsObj}, dslVars)
	if runErr = finishDSLExecution(txState, runErr); runErr != nil {
		// Ошибку действия кладём в стор сообщений: после редиректа страница
		// перерисуется штатно, а баннер не потеряется.
		s.messages.Push(userKeyFromRequest(r), s.errText(r, runErr))
	}

	http.Redirect(w, r, "/ui/page/"+pg.Name+pageQuery(r), http.StatusSeeOther)
}

// pageProcEnv готовит окружение вызова обработчика страницы: построитель блоков,
// Параметры из query string и dslVars со сбором Сообщить в msgs. Общий код для
// GET (ПриФормировании) и POST (КнопкаДействие).
func (s *Server) pageProcEnv(r *http.Request, msgs *[]string) (*interpreter.DSLPageBuilder, *interpreter.Map, map[string]any, *interpreter.TxState) {
	params := map[string]string{}
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			params[k] = vs[0]
		}
	}
	paramsObj := interpreter.NewStringMap(params)
	builder := interpreter.NewPageBuilder()
	mc := runtime.NewMovementsCollector("page", uuid.Nil)
	// Кладём язык запроса в контекст, чтобы НСтр(текст) в обработчике страницы
	// (и в процедуре-действии) переводил на язык пользователя (план 66, п.3).
	ctx := withLang(r.Context(), s.resolveLang(r))
	dslVars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, msgs)
	dslVars["Страница"] = builder
	dslVars["Page"] = builder
	dslVars["Параметры"] = paramsObj
	dslVars["Parameters"] = paramsObj
	return builder, paramsObj, dslVars, txState
}

// pageQuery возвращает query string запроса с ведущим «?» (или пустую строку) —
// чтобы сохранять Параметры в action-URL кнопок и при PRG-редиректе действия.
func pageQuery(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return ""
	}
	return "?" + r.URL.RawQuery
}

// decodePathParam декодирует значение chi.URLParam. Go выставляет RawPath, когда
// percent-encoding не каноничен (например, нижний регистр hex в ссылках меню), и
// тогда chi возвращает сегмент пути сырым — его нужно раскодировать перед поиском
// по имени. Уже декодированное значение (без «%») возвращается без изменений; при
// битом encoding отдаём как есть. Тот же приём инлайном — в admin_*.go.
func decodePathParam(v string) string {
	if dec, err := url.PathUnescape(v); err == nil {
		return dec
	}
	return v
}
