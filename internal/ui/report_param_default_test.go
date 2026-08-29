package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Значение параметра отчёта по умолчанию. Раньше линтер ключ `default` принимал,
// а модель отчёта его не знала: отчёт с необязательной датой молча приходил
// пустым — «Срок < NULL» не выбирает ничего, и пользователь видел пустую таблицу
// вместо просроченных задач.
//
// Правило одно на все точки сбора значений: умолчание подставляется, только
// когда параметра в запросе НЕТ. Пустое значение — это выбор пользователя,
// умолчание его не перебивает.

func TestУмолчаниеПараметраОтчёта_ПодставляетсяКогдаЗначенияНет(t *testing.T) {
	rep := &reportpkg.Report{Name: "ПросроченныеЗадачи", Params: []reportpkg.Param{
		{Name: "НаДату", Type: "date", Default: "{{today}}"},
		{Name: "Исполнитель", Type: "string"},
	}}
	r := httptest.NewRequest("GET", "/ui/report/ПросроченныеЗадачи", nil)

	values := reportParamValuesFromRequest(r, rep)

	сегодня := time.Now().Format("2006-01-02")
	if got := values["НаДату"]; got != сегодня {
		t.Errorf("НаДату = %#v, ожидалось %q (умолчание {{today}})", got, сегодня)
	}
	// Параметр без умолчания остаётся пустым — прежнее поведение.
	if got := values["Исполнитель"]; got != nil {
		t.Errorf("Исполнитель = %#v, ожидалось пусто", got)
	}
}

func TestУмолчаниеПараметраОтчёта_ЗначениеПользователяВажнее(t *testing.T) {
	rep := &reportpkg.Report{Name: "R", Params: []reportpkg.Param{
		{Name: "НаДату", Type: "date", Default: "{{today}}"},
	}}
	r := httptest.NewRequest("GET", "/ui/report/R?%D0%9D%D0%B0%D0%94%D0%B0%D1%82%D1%83=2026-01-31", nil)

	if got := reportParamValuesFromRequest(r, rep)["НаДату"]; got != "2026-01-31" {
		t.Errorf("НаДату = %#v: умолчание затёрло выбор пользователя", got)
	}
}

// Очищенное поле — тоже выбор: пользователь снял отбор и ждёт отчёт без него.
// Умолчание здесь не подставляется, иначе поле нельзя очистить вовсе.
func TestУмолчаниеПараметраОтчёта_ОчищенноеПолеНеВозвращается(t *testing.T) {
	rep := &reportpkg.Report{Name: "R", Params: []reportpkg.Param{
		{Name: "НаДату", Type: "date", Default: "{{today}}"},
	}}
	form := url.Values{"НаДату": {""}}
	r := reqWithChi("POST", "/ui/report/R", form, map[string]string{"name": "R"})

	if got := reportParamValuesFromRequest(r, rep)["НаДату"]; got != nil {
		t.Errorf("НаДату = %#v: умолчание вернулось в очищенное поле", got)
	}
}

// Снятый флажок браузер не отправляет вовсе, поэтому по одному отсутствию ключа
// «снял галку» неотличимо от «не задавал» — и умолчание true возвращало галку на
// место сразу после того, как её сняли. Форма шлёт рядом с флажком скрытый
// маркер __has.<имя>; по нему снятая галка остаётся снятой.
func TestУмолчаниеПараметраОтчёта_СнятыйФлажокНеВозвращается(t *testing.T) {
	rep := &reportpkg.Report{Name: "R", Params: []reportpkg.Param{
		{Name: "ТолькоМои", Type: "bool", Default: "true"},
	}}
	form := url.Values{"__has.ТолькоМои": {"1"}}
	r := reqWithChi("POST", "/ui/report/R", form, map[string]string{"name": "R"})

	if got := reportParamValuesFromRequest(r, rep)["ТолькоМои"]; got != nil {
		t.Errorf("ТолькоМои = %#v: умолчание вернуло снятую галку", got)
	}

	// Поставленная галка приходит как обычно.
	form2 := url.Values{"__has.ТолькоМои": {"1"}, "ТолькоМои": {"true"}}
	r2 := reqWithChi("POST", "/ui/report/R", form2, map[string]string{"name": "R"})
	if got := reportParamValuesFromRequest(r2, rep)["ТолькоМои"]; got != "true" {
		t.Errorf("ТолькоМои = %#v, ожидалось \"true\"", got)
	}
}

// Четвёртая точка запуска — сохранение настроек отчёта. Форма настроек несёт все
// параметры скрытыми полями, а редирект после сохранения ведёт на `?__run=1`,
// откуда отчёт строится заново. Пока ссылка редиректа выбрасывала пустые
// значения, ключа в ней не оказывалось — и правило «нет ключа → умолчание»
// возвращало очищенное поле и снятую галку сразу после «Сохранить».
func TestУмолчаниеПараметраОтчёта_НастройкиНеВозвращаютОчищенное(t *testing.T) {
	rep := &reportpkg.Report{
		Name: "ПросроченныеЗадачи",
		Params: []reportpkg.Param{
			{Name: "НаДату", Type: "date", Default: "{{today}}"},
			{Name: "ТолькоМои", Type: "bool", Default: "true"},
			{Name: "Исполнитель", Type: "string"},
		},
		// Настройки и пресеты есть только у отчётов с composition — ровно у тех,
		// кого задевает дефект.
		Composition: &reportpkg.Composition{Groupings: []string{"Товар"}},
	}
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "report-default-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	// Пользователь очистил дату, снял галку и оставил заполненным «Исполнитель».
	// Форма настроек шлёт все три скрытыми полями.
	form := url.Values{
		"__settings":  {`{"variant":""}`},
		"НаДату":      {""},
		"ТолькоМои":   {""},
		"Исполнитель": {"Петров"},
	}
	// Обе точки сохранения зовут одну сборку ссылки: обычное сохранение настроек
	// и сохранение именованного варианта.
	for _, tc := range []struct {
		имя    string
		extra  url.Values
		assert func(t *testing.T, loc string)
	}{
		{имя: "настройки"},
		{
			имя: "вариант",
			extra: url.Values{
				"__preset_action": {"save_as"},
				"__preset_name":   {"Мой вариант"},
			},
			assert: func(t *testing.T, loc string) {
				if !strings.Contains(loc, "__preset=") {
					t.Errorf("в ссылке редиректа потерян выбранный вариант: %s", loc)
				}
			},
		},
	} {
		t.Run(tc.имя, func(t *testing.T) {
			f := url.Values{}
			for k, v := range form {
				f[k] = v
			}
			for k, v := range tc.extra {
				f[k] = v
			}
			r := reqWithChi("POST", "/ui/report/ПросроченныеЗадачи/settings/save", f,
				map[string]string{"name": "ПросроченныеЗадачи"})
			w := httptest.NewRecorder()
			s.reportSettingsSave(w, r)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("сохранение настроек: код %d, тело %s", w.Code, w.Body.String())
			}
			loc := w.Header().Get("Location")
			if tc.assert != nil {
				tc.assert(t, loc)
			}

			// Дальше браузер идёт по редиректу, и reportForm при __run=1 собирает
			// значения тем же путём.
			values := reportParamValuesFromRequest(httptest.NewRequest("GET", loc, nil), rep)
			if got := values["НаДату"]; got != nil {
				t.Errorf("НаДату = %#v: умолчание вернулось в очищенное поле после сохранения (%s)", got, loc)
			}
			if got := values["ТолькоМои"]; got != nil {
				t.Errorf("ТолькоМои = %#v: умолчание вернуло снятую галку после сохранения (%s)", got, loc)
			}
			// Заданное значение переживает сохранение как прежде.
			if got := values["Исполнитель"]; got != "Петров" {
				t.Errorf("Исполнитель = %#v, ожидалось «Петров»: сохранение настроек потеряло значение", got)
			}
		})
	}
}

func TestУмолчаниеПараметраОтчёта_ОбычнаяСтрокаНеРазворачивается(t *testing.T) {
	// Умолчание без подстановки — просто значение.
	p := reportpkg.Param{Name: "Состояние", Type: "string", Default: "ВРаботе"}
	if got := reportParamDefault(p); got != "ВРаботе" {
		t.Errorf("= %q, ожидалось «ВРаботе»", got)
	}
	if got := reportParamDefault(reportpkg.Param{Name: "X"}); got != "" {
		t.Errorf("= %q, у параметра без умолчания ожидалось пусто", got)
	}
}

// Умолчание видно ДО первого построения: страница параметров рендерится с ним, и
// пользователь понимает, с чем поедет отчёт, и может это изменить.
func TestУмолчаниеПараметраОтчёта_ВидноПриПервомОткрытииФормы(t *testing.T) {
	rep := &reportpkg.Report{Name: "ПросроченныеЗадачи", Params: []reportpkg.Param{
		{Name: "НаДату", Type: "date", Default: "{{today}}"},
		{Name: "ТолькоМои", Type: "bool", Default: "true"},
		{Name: "Снятый", Type: "bool", Default: "false"},
	}}
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "report-default.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}

	r := reqWithChi("GET", "/ui/report/ПросроченныеЗадачи", nil,
		map[string]string{"name": "ПросроченныеЗадачи"})
	w := httptest.NewRecorder()
	s.reportForm(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("форма отчёта: код %d, тело %s", w.Code, w.Body.String())
	}
	out := w.Body.String()
	сегодня := time.Now().Format("2006-01-02")
	if !strings.Contains(out, `name="НаДату" value="`+сегодня+`"`) {
		t.Errorf("поле «НаДату» открылось пустым, умолчание %q в форму не попало", сегодня)
	}
	if !strings.Contains(out, `name="ТолькоМои" value="true" checked`) {
		t.Errorf("флажок с умолчанием true не отмечен при открытии формы")
	}
	// "false" — непустая строка, и без приведения к bool шаблон отметил бы галку.
	if strings.Contains(out, `name="Снятый" value="true" checked`) {
		t.Errorf("флажок с умолчанием false отмечен")
	}
	// Маркер-спутник обязателен у каждого флажка, иначе снятую галку не отличить
	// от «параметр не задавали».
	for _, want := range []string{`name="__has.ТолькоМои"`, `name="__has.Снятый"`} {
		if !strings.Contains(out, want) {
			t.Errorf("в форме нет маркера %s", want)
		}
	}
}

// Ссылка выгрузки — снимок формы: в неё попадают все объявленные параметры,
// включая пустые. Иначе выгрузка подставит умолчание туда, где на экране пусто,
// и Excel разойдётся с таблицей.
func TestУмолчаниеПараметраОтчёта_СсылкаВыгрузкиНесётПустыеПараметры(t *testing.T) {
	rep := &reportpkg.Report{Name: "ПросроченныеЗадачи", Params: []reportpkg.Param{
		{Name: "НаДату", Type: "date", Default: "{{today}}"},
		{Name: "ТолькоМои", Type: "bool", Default: "true"},
	}}
	data := map[string]any{
		"Report": rep,
		// Пользователь очистил дату и снял галку.
		"ParamValues":  map[string]any{"НаДату": nil, "ТолькоМои": nil},
		"ReportParams": []reportParamUI{},
		"Cfg":          Config{},
		"Lang":         "ru",
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "report-export-buttons", data); err != nil {
		t.Fatalf("execute report-export-buttons: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		url.QueryEscape("НаДату") + "=&",
		url.QueryEscape("ТолькоМои") + "=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в ссылке выгрузки нет пустого параметра %q: %s", want, out)
		}
	}
}
