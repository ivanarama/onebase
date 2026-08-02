package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/widget"
)

// widgetPreviewResponse — render-ready результат прогона виджета для панели
// предпросмотра в конфигураторе. Намеренно плоский: клиент рисует таблицу и
// (для графиков) лёгкую CSS-визуализацию без внешних библиотек.
type widgetPreviewResponse struct {
	OK      bool                  `json:"ok"`
	Error   string                `json:"error,omitempty"`
	Type    string                `json:"type"`
	Title   string                `json:"title"`
	Mapping string                `json:"mapping"`          // «Ось X: Месяц · Серии: Приход, Расход»
	Columns []string              `json:"columns"`          // подписи колонок таблицы
	Rows    [][]string            `json:"rows"`             // отформатированные ячейки
	Chart   *widgetPreviewChart   `json:"chart,omitempty"`  // только для type=chart
	// EChartsOption — та же опция ECharts, что строит рабочий стол (widget.EChartsOption).
	// Конфигуратор рисует её тем же ECharts, поэтому предпросмотр совпадает с тем,
	// что увидит пользователь (сглаживание линий, круговая и т.д.).
	EChartsOption json.RawMessage `json:"echarts_option,omitempty"`
	KPI           string          `json:"kpi,omitempty"` // только для type=kpi
}

type widgetPreviewChart struct {
	Kind   string                `json:"kind"`
	XAxis  []string              `json:"xaxis"`
	Series []widgetPreviewSeries `json:"series"`
}

type widgetPreviewSeries struct {
	Name string    `json:"name"`
	Data []float64 `json:"data"`
}

// configuratorWidgetPreview парсит YAML виджета из тела запроса, исполняет его
// против данных базы тем же widget.Runner, что и рабочий стол, и возвращает
// данные + раскладку «колонка → ось/серия». Это делает очевидным, ОТКУДА
// виджет берёт цифры и КАК они ложатся на график. Кроме идемпотентной
// миграции схемы регистров (см. ниже) на диск ничего не пишет — файлы виджетов
// не трогает.
func (h *handler) configuratorWidgetPreview(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeFormsJSON(w, widgetPreviewResponse{Error: err.Error()})
		return
	}
	body := r.FormValue("yaml")

	wdg, err := parseWidgetYAML(body)
	if err != nil {
		writeFormsJSON(w, widgetPreviewResponse{Error: err.Error()})
		return
	}

	proj, err := h.loadProjectFor(r.Context(), b)
	if err != nil {
		writeFormsJSON(w, widgetPreviewResponse{Error: "конфигурация: " + err.Error()})
		return
	}
	defer proj.Close()

	db, err := OpenDB(r.Context(), b)
	if err != nil {
		writeFormsJSON(w, widgetPreviewResponse{Error: "база данных: " + err.Error()})
		return
	}
	defer db.Close()

	// Предпросмотр исполняет запрос против реальной БД тем же путём, что и
	// рабочий стол. Если схему БД ещё не догнали под текущий YAML (в регистр
	// добавили реквизит/измерение, а базу не перезапускали), виртуальные
	// таблицы .Обороты/.Остатки безусловно выбирают все реквизиты регистра и
	// сошлются на отсутствующую колонку — запрос упадёт «no such column …»,
	// даже если сам виджет этот реквизит не использует. Поэтому best-effort
	// дотягиваем схему регистров — ровно как делает запуск базы (onebase run)
	// на старте. Это чистый идемпотентный ADD COLUMN / CREATE IF NOT EXISTS
	// (без сидинга предопределённых данных, т.е. без db.Migrate по сущностям).
	// Ошибки миграции не должны прятать сам предпросмотр — игнорируем их и
	// просто пробуем выполнить запрос.
	_ = db.MigrateRegisters(r.Context(), proj.Registers)
	_ = db.MigrateInfoRegisters(r.Context(), proj.InfoRegisters)
	_ = db.MigrateAccountRegisters(r.Context(), proj.AccountRegisters)

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities:  proj.Entities,
		Registers: proj.Registers,
		InfoRegs:  proj.InfoRegisters,
		Enums:     proj.Enums,
		Constants: proj.Constants,
	})
	reg.LoadAccountRegisters(proj.AccountRegisters, proj.ChartsOfAccounts)

	runner := widget.New(reg, db)
	res := runner.Run(r.Context(), wdg)

	writeFormsJSON(w, buildWidgetPreview(wdg, res))
}

// parseWidgetYAML переиспользует валидацию metadata.LoadWidgetFile (включая
// дефолты chart_kind/limit/scope) через временный файл — тот же путь, что и при
// сохранении, чтобы предпросмотр и сохранение трактовали YAML одинаково.
func parseWidgetYAML(body string) (*metadata.Widget, error) {
	tmpPath, cleanup, err := writeTempFile("widget-preview-*.yaml", body)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return metadata.LoadWidgetFile(tmpPath)
}

// buildWidgetPreview приводит widget.Result к плоскому render-ready виду.
func buildWidgetPreview(wdg *metadata.Widget, res widget.Result) widgetPreviewResponse {
	out := widgetPreviewResponse{
		OK:    res.Error == "",
		Error: hintSchemaError(res.Error),
		Type:  res.Type,
		Title: res.Title,
	}
	if res.Error != "" {
		return out
	}

	switch metadata.WidgetType(res.Type) {
	case metadata.WidgetTypeChart:
		if res.Chart != nil {
			ch := &widgetPreviewChart{Kind: res.Chart.Kind, XAxis: res.Chart.XAxis}
			for _, s := range res.Chart.Series {
				ch.Series = append(ch.Series, widgetPreviewSeries{Name: s.Name, Data: s.Data})
			}
			out.Chart = ch

			// Та же опция ECharts, что и у рабочего стола — единый источник.
			if opt, err := json.Marshal(widget.EChartsOption(res.Chart)); err == nil {
				out.EChartsOption = opt
			}

			// Таблица для графика = ось X + по колонке на серию: прямо показывает,
			// какая колонка стала осью, а какие — сериями.
			xLabel := wdg.XField
			if xLabel == "" {
				xLabel = "X"
			}
			out.Columns = append(out.Columns, xLabel)
			for _, s := range res.Chart.Series {
				out.Columns = append(out.Columns, s.Name)
			}
			for i, x := range res.Chart.XAxis {
				row := []string{x}
				for _, s := range res.Chart.Series {
					if i < len(s.Data) {
						row = append(row, formatNum(s.Data[i]))
					} else {
						row = append(row, "")
					}
				}
				out.Rows = append(out.Rows, row)
			}
			out.Mapping = chartMapping(wdg, res.Chart)
		}
	case metadata.WidgetTypeKPI:
		if res.KPI != nil {
			out.KPI = res.KPI.Display
			out.Columns = []string{"Значение"}
			out.Rows = [][]string{{res.KPI.Display}}
			out.Mapping = "KPI · одно число из первого столбца запроса"
		}
	default: // list / recent / actions
		for _, c := range res.Columns {
			label := c.Label
			if label == "" {
				label = c.Field
			}
			out.Columns = append(out.Columns, label)
		}
		for _, rrow := range res.Rows {
			row := make([]string, 0, len(res.Columns))
			for _, c := range res.Columns {
				row = append(row, fmt.Sprintf("%v", valueOrEmpty(rrow[c.Field])))
			}
			out.Rows = append(out.Rows, row)
		}
		out.Mapping = fmt.Sprintf("%s · %d строк, %d колонок", res.Type, len(res.Rows), len(res.Columns))
	}
	return out
}

// hintSchemaError дополняет ошибку «no such column …» подсказкой про миграцию.
// Предпросмотр уже дотягивает схему регистров перед прогоном, но если это не
// помогло (например, файл БД заблокирован запущенным процессом базы, или
// колонки не хватает в самой таблице сущности), показываем пользователю, что
// делать, вместо голой SQL-ошибки.
func hintSchemaError(msg string) string {
	if msg == "" {
		return ""
	}
	if strings.Contains(msg, "no such column") {
		return msg + " · похоже, схема БД отстала от конфигурации — запустите базу или выполните «onebase migrate», чтобы догнать схему"
	}
	return msg
}

func chartMapping(wdg *metadata.Widget, ch *widget.ChartData) string {
	x := wdg.XField
	if x == "" {
		x = "(первый столбец)"
	}
	series := make([]string, 0, len(ch.Series))
	for _, s := range ch.Series {
		series = append(series, s.Name)
	}
	y := strings.Join(series, ", ")
	if y == "" {
		y = "(остальные столбцы)"
	}
	return fmt.Sprintf("график %s · Ось X: %s · Серии: %s", ch.Kind, x, y)
}

func valueOrEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

// formatNum печатает число без хвоста .0 для целых — таблица предпросмотра
// должна быть читаемой, не «522000.000000».
func formatNum(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.2f", f)
}
