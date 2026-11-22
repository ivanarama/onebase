package resultfilter

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/shopspring/decimal"
)

const MaxConditions = 64
const MaxValueBytes = 4096

type EnumLookup func(name string) []string
type condition struct {
	field Field
	op    string
	value any
}
type Plan struct{ conditions []condition }

// New validates every condition before SQL or any row is read. It does not
// silently discard an invalid/withdrawn field from a saved preset.
func New(fields []Field, filters []reportpkg.Filter, enums EnumLookup) (*Plan, error) {
	if len(filters) > MaxConditions {
		return nil, fmt.Errorf("слишком много условий отбора")
	}
	byName := map[string]Field{}
	for _, f := range fields {
		byName[strings.ToLower(f.Name)] = f
	}
	p := &Plan{}
	for _, raw := range filters {
		f, ok := byName[strings.ToLower(raw.Field)]
		if !ok {
			return nil, fmt.Errorf("поле отбора недоступно или его происхождение не доказано")
		}
		valid := false
		for _, op := range f.Operators {
			if raw.Op == op {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("недопустимый оператор отбора")
		}
		if len(raw.Value) > MaxValueBytes {
			return nil, fmt.Errorf("значение отбора слишком велико")
		}
		c := condition{field: f, op: raw.Op}
		if raw.Op == "is_null" || raw.Op == "not_null" {
			if raw.Value != "" {
				return nil, fmt.Errorf("условие NULL не принимает значение")
			}
		} else {
			v, err := parseValue(f, raw.Value, enums)
			if err != nil {
				return nil, fmt.Errorf("некорректное значение отбора для %s", f.Name)
			}
			c.value = v
		}
		p.conditions = append(p.conditions, c)
	}
	return p, nil
}
func (p *Plan) Empty() bool { return p == nil || len(p.conditions) == 0 }
func parseValue(f Field, raw string, enums EnumLookup) (any, error) {
	if f.RefEntity != "" {
		return uuid.Parse(raw)
	}
	if f.EnumName != "" {
		if enums != nil {
			for _, value := range enums(f.EnumName) {
				if raw == value {
					return raw, nil
				}
			}
		}
		return nil, fmt.Errorf("enum")
	}
	switch f.Type {
	case "string":
		return raw, nil
	case "number":
		// Bound both coefficient and exponent before decimal comparisons allocate.
		if len(raw) > 128 {
			return nil, fmt.Errorf("number")
		}
		d, err := decimal.NewFromString(raw)
		if err != nil || d.Exponent() < -1000 || d.Exponent() > 1000 {
			return nil, fmt.Errorf("number")
		}
		return d, nil
	case "bool":
		if raw == "true" {
			return true, nil
		}
		if raw == "false" {
			return false, nil
		}
		return nil, fmt.Errorf("bool")
	case "date", "datetime":
		if d, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return d, nil
		}
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02", "02.01.2006"} {
			if d, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				return d, nil
			}
		}
	}
	return nil, fmt.Errorf("type")
}
func (p *Plan) Match(row map[string]any) (bool, error) {
	if p.Empty() {
		return true, nil
	}
	for _, c := range p.conditions {
		value, ok := row[c.field.Name]
		if !ok {
			return false, fmt.Errorf("схема результата отчёта изменилась")
		}
		if c.op == "is_null" {
			if value != nil {
				return false, nil
			}
			continue
		}
		if c.op == "not_null" {
			if value == nil {
				return false, nil
			}
			continue
		}
		// SQL-style NULL comparisons never match, including !=. Empty strings,
		// false, zero and zero UUID remain actual typed values.
		if value == nil {
			return false, nil
		}
		cmp := 0
		switch target := c.value.(type) {
		case decimal.Decimal:
			var d decimal.Decimal
			switch v := value.(type) {
			case decimal.Decimal:
				d = v
			case string:
				var err error
				d, err = decimal.NewFromString(v)
				if err != nil {
					return false, fmt.Errorf("неверный тип числового результата")
				}
			case int64:
				d = decimal.NewFromInt(v)
			case int:
				d = decimal.NewFromInt(int64(v))
			default:
				return false, fmt.Errorf("неверный тип числового результата")
			}
			if d.Exponent() < -1000 || d.Exponent() > 1000 {
				return false, fmt.Errorf("число вне диапазона отбора")
			}
			cmp = d.Cmp(target)
		case time.Time:
			d, ok := query.ToDateValue(value)
			if !ok {
				return false, fmt.Errorf("неверный тип даты результата")
			}
			if d.Before(target) {
				cmp = -1
			} else if d.After(target) {
				cmp = 1
			}
		case bool:
			v, ok := query.ToBoolValue(value)
			if !ok {
				return false, fmt.Errorf("неверный булев тип результата")
			}
			if v != target {
				cmp = 1
			}
		case uuid.UUID:
			var id uuid.UUID
			var err error
			switch v := value.(type) {
			case uuid.UUID:
				id = v
			case string:
				id, err = uuid.Parse(v)
			default:
				err = fmt.Errorf("uuid")
			}
			if err != nil {
				return false, fmt.Errorf("неверный тип ссылки результата")
			}
			if id != target {
				cmp = 1
			}
		case string:
			s, ok := value.(string)
			if !ok {
				return false, fmt.Errorf("неверный строковый тип результата")
			}
			if c.op == "contains" {
				if !strings.Contains(strings.ToLower(s), strings.ToLower(target)) {
					return false, nil
				}
				continue
			}
			cmp = strings.Compare(s, target)
		default:
			return false, fmt.Errorf("неподдержанный тип отбора %s", strconv.Quote(c.field.Type))
		}
		matched := false
		switch c.op {
		case "eq":
			matched = cmp == 0
		case "ne":
			matched = cmp != 0
		case "gt":
			matched = cmp > 0
		case "ge":
			matched = cmp >= 0
		case "lt":
			matched = cmp < 0
		case "le":
			matched = cmp <= 0
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}
