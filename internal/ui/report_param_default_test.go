package ui

import (
	"net/http/httptest"
	"testing"
	"time"

	reportpkg "github.com/ivantit66/onebase/internal/report"
)

// Значение параметра отчёта по умолчанию. Раньше линтер ключ `default` принимал,
// а модель отчёта его не знала: отчёт с необязательной датой молча приходил
// пустым — «Срок < NULL» не выбирает ничего, и пользователь видел пустую таблицу
// вместо просроченных задач.

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
