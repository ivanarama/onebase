package report

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Разбор значения параметра отчёта жил в трёх копиях — экран, выгрузки
// Excel/PDF и `/api/v2`, — и копии расходились НАМЕРЕННО: первые две тихо
// оставляют негодную дату строкой, третья отвечает 400. Расхождение нигде не
// было записано и держалось случайно, а добавление нового типа завело бы
// четвёртую копию (план 160). Здесь одна функция с двумя явно названными
// профилями вызывающего.

// ParamParseMode — профиль вызывающего. Это часть контракта совместимости, а не
// способ разнести парсер обратно по вызывающим.
type ParamParseMode uint8

const (
	// ParamParseForm — экран отчёта и выгрузки Excel/PDF. Имя типа сравнивается
	// строго: только точные `date` и `bool` получают особое поведение.
	ParamParseForm ParamParseMode = iota
	// ParamParseAPI — `/api/v2`. Имя типа непустого значения нормализуется
	// (регистр и пробелы), принимается alias `boolean`, а негодное значение
	// становится ошибкой вместо молчаливого отката к строке.
	ParamParseAPI
)

// ParseParamValue разбирает значение параметра отчёта по его типу.
// Ошибку возвращает только для значений, которые тип обязан отвергнуть;
// вызывающий решает, отвечать 400 или откатиться к исходной строке.
func ParseParamValue(raw string, p Param, mode ParamParseMode) (any, error) {
	// Пустое значение решается ДО нормализации имени типа: и экран, и API
	// сегодня сравнивают Type точно, и правило «пустой точный bool — это Ложь»
	// держится именно на этом. Для `boolean`, `BOOL` и типа с пробелами пустое
	// значение даёт nil — краевой случай сохранён намеренно.
	if raw == "" {
		if p.Type == "bool" {
			return false, nil
		}
		return nil, nil
	}
	typ := p.Type
	if mode == ParamParseAPI {
		typ = strings.ToLower(strings.TrimSpace(typ))
	}
	switch typ {
	case "datetime":
		// Три формата — те же, что у параметров обработок: браузер отдаёт
		// datetime-local без секунд, а ссылка со старым значением может нести
		// одну дату, и ломать её из-за нового типа незачем.
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				return t, nil
			}
		}
		return nil, errors.New("expected datetime YYYY-MM-DDTHH:MM[:SS]")
	case "date":
		t, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			return nil, errors.New("expected date YYYY-MM-DD")
		}
		return t, nil
	case "bool":
		if mode == ParamParseAPI {
			return parseBoolWide(raw), nil
		}
		return parseBoolForm(raw), nil
	case "boolean":
		if mode == ParamParseAPI {
			return parseBoolWide(raw), nil
		}
		return raw, nil
	case "number":
		if mode == ParamParseAPI {
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, errors.New("expected number")
			}
			return n, nil
		}
		return raw, nil
	default:
		return raw, nil
	}
}

// parseBoolForm — набор значений флажка формы: браузер присылает поле только
// когда флажок установлен.
func parseBoolForm(raw string) bool {
	return raw == "true" || raw == "on" || raw == "1" || strings.EqualFold(raw, "да")
}

// parseBoolWide — набор API: клиент пишет значение руками, поэтому принимается
// больше написаний.
func parseBoolWide(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "да", "истина":
		return true
	}
	return false
}
