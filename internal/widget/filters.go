package widget

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

// План 182D: разбор значений интерактивных фильтров list-виджета. Браузер
// присылает только имя фильтра и сырую строку; тип, границы и сущность сервер
// берёт из метаданных. Значение никогда не попадает в текст запроса — только в
// CompileOpts.Params.

// MaxFilterStringLength — верхняя граница длины строкового фильтра.
const MaxFilterStringLength = 512

// KnownFilterTypes — типы фильтров первого среза (reference:<Сущность>
// проверяется отдельно через WidgetFilter.ReferenceEntity).
func KnownFilterTypes() []string {
	return []string{"string", "number", "date", "bool", "select"}
}

// IsKnownFilterType сообщает, относится ли тип к известным (включая
// reference:<Сущность>).
func IsKnownFilterType(t string) bool {
	for _, k := range KnownFilterTypes() {
		if k == t {
			return true
		}
	}
	return IsKnownFilterTypeMap(t)
}

// IsKnownFilterTypeMap — то же для типов вида reference:<Сущность>.
func IsKnownFilterTypeMap(t string) bool {
	const prefix = "reference:"
	if !strings.HasPrefix(t, prefix) {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(t, prefix)) != ""
}

// ParseFilterValue разбирает сырую строку из URL в типизированное значение
// параметра запроса. Пустая строка означает «отбор не задан» — типизированный
// nil. Неизвестное select-значение, плохой UUID/decimal/date и слишком длинная
// строка дают ошибку (HTTP 400 на границе).
func ParseFilterValue(f metadata.WidgetFilter, raw string) (any, error) {
	if raw == "" {
		return nil, nil
	}
	switch {
	case f.ReferenceEntity() != "":
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("значение фильтра %q не является UUID ссылки", f.Name)
		}
		return id.String(), nil
	}
	switch f.Type {
	case "string":
		if len([]rune(raw)) > MaxFilterStringLength {
			return nil, fmt.Errorf("значение фильтра %q длиннее %d символов", f.Name, MaxFilterStringLength)
		}
		return raw, nil
	case "number":
		d, err := decimal.NewFromString(raw)
		if err != nil {
			return nil, fmt.Errorf("значение фильтра %q не является числом", f.Name)
		}
		return d, nil
	case "date":
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return nil, fmt.Errorf("значение фильтра %q не является датой (ГГГГ-ММ-ДД)", f.Name)
		}
		return t, nil
	case "bool":
		switch raw {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("значение фильтра %q не является true/false", f.Name)
	case "select":
		for _, v := range f.Values {
			if v.Value == raw {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("значение фильтра %q не входит в объявленный список", f.Name)
	}
	return nil, fmt.Errorf("неизвестный тип фильтра %q", f.Type)
}

// ReferenceAllowed подтверждает, что конкретная ссылка read-доступна текущему
// пользователю (права + RLS-предикат). Недоступная и несуществующая ссылки
// неотличимы: оба случая дают false — fail-closed без раскрытия причины.
func (r *Runner) ReferenceAllowed(ctx context.Context, entityName, id string) bool {
	entity := r.Reg.GetEntity(entityName)
	if entity == nil {
		return false
	}
	uid, err := uuid.Parse(id)
	if err != nil || uid == uuid.Nil {
		return false
	}
	return r.rowAllowedID(ctx, entity, "read", uid)
}

// ValidateFilterSample проверяет, что строковое представление (значение из URL
// или умолчание из YAML) допустимо для фильтра. Используется и рантаймом, и
// onebase check.
func ValidateFilterSample(f metadata.WidgetFilter, raw string) error {
	_, err := ParseFilterValue(f, raw)
	return err
}
