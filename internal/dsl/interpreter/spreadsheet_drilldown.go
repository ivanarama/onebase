package interpreter

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/shopspring/decimal"
)

const (
	TabularPayloadMaxDepth         = 32
	TabularPayloadMaxNodes         = 65536
	TabularPayloadMaxCellBytes     = 1 << 20
	TabularPayloadMaxDocumentBytes = 8 << 20
)

type tabularPayloadBudget struct{ nodes, bytes, cellEnd int }

func (b *tabularPayloadBudget) charge(n int) error {
	b.bytes += n
	if b.bytes > TabularPayloadMaxDocumentBytes || b.bytes > b.cellEnd {
		return fmt.Errorf("расшифровка превышает лимит")
	}
	return nil
}
func (b *tabularPayloadBudget) text(s string) error {
	if len(s) > TabularPayloadMaxCellBytes {
		return fmt.Errorf("расшифровка превышает лимит")
	}
	data, _ := json.Marshal(s)
	return b.charge(len(data))
}

// FreezeTabularDrilldowns is called on a detached document at publication.
// It never invokes an arbitrary Get, String, MarshalJSON or manager method.
func FreezeTabularDrilldowns(doc *sheet.Document) (int, error) {
	budget := &tabularPayloadBudget{}
	total := 0
	freezeCell := func(c *sheet.Cell) error {
		if c == nil {
			return nil
		}
		if c.DrilldownSource != nil {
			budget.cellEnd = budget.bytes + TabularPayloadMaxCellBytes
			p, err := budget.freeze(c.DrilldownSource, 0)
			if err != nil {
				return err
			}
			data, err := json.Marshal(p)
			if err != nil {
				return fmt.Errorf("недопустимая расшифровка")
			}
			if len(data) > TabularPayloadMaxCellBytes {
				return fmt.Errorf("расшифровка ячейки превышает лимит")
			}
			total += len(data)
			if total > TabularPayloadMaxDocumentBytes {
				return fmt.Errorf("расшифровки документа превышают лимит")
			}
			c.DrilldownPayload = p
		}
		c.DrilldownSource = nil
		return nil
	}
	for _, c := range doc.Cells {
		if err := freezeCell(c); err != nil {
			return 0, err
		}
	}
	if doc.HeaderArea != nil {
		for _, c := range doc.HeaderArea.Cells {
			if err := freezeCell(c); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func (b *tabularPayloadBudget) freeze(value any, depth int) (*sheet.DrilldownValue, error) {
	b.nodes++
	if depth > TabularPayloadMaxDepth || b.nodes > TabularPayloadMaxNodes {
		return nil, fmt.Errorf("цикл или превышение пределов расшифровки")
	}
	if err := b.charge(128); err != nil {
		return nil, err
	}
	out := &sheet.DrilldownValue{}
	switch v := value.(type) {
	case nil:
		out.Kind = "null"
	case string:
		out.Kind = "string"
		out.Text = v
	case bool:
		out.Kind = "bool"
		out.Bool = v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("недопустимое число расшифровки")
		}
		out.Kind = "float64"
		out.Text = strconv.FormatFloat(v, 'g', -1, 64)
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("недопустимое число расшифровки")
		}
		out.Kind = "float32"
		out.Text = strconv.FormatFloat(float64(v), 'g', -1, 32)
	case int:
		out.Kind = "int"
		out.Text = strconv.FormatInt(int64(v), 10)
	case int64:
		out.Kind = "int64"
		out.Text = strconv.FormatInt(v, 10)
	case int32:
		out.Kind = "int32"
		out.Text = strconv.FormatInt(int64(v), 10)
	case uint:
		out.Kind = "uint"
		out.Text = strconv.FormatUint(uint64(v), 10)
	case uint64:
		out.Kind = "uint64"
		out.Text = strconv.FormatUint(v, 10)
	case uint32:
		out.Kind = "uint32"
		out.Text = strconv.FormatUint(uint64(v), 10)
	case decimal.Decimal:
		if v.Exponent() > 4096 || v.Exponent() < -4096 {
			return nil, fmt.Errorf("число расшифровки превышает лимит")
		}
		out.Kind = "decimal"
		out.Text = v.String()
	case time.Time:
		if v.Year() < 1 || v.Year() > 9999 {
			return nil, fmt.Errorf("недопустимая дата расшифровки")
		}
		out.Kind = "date"
		out.Text = v.Format(time.RFC3339Nano)
	case *Ref:
		if v == nil {
			out.Kind = "null"
			break
		}
		if v.Type == "" {
			return nil, fmt.Errorf("расшифровке требуется типизированная ссылка")
		}
		if err := b.text(v.Type + v.UUID + string(v.Kind)); err != nil {
			return nil, err
		}
		out.Kind = "reference"
		out.Ref = &sheet.Reference{Entity: v.Type, ID: v.UUID}
		out.RefKind = string(v.Kind)
	case *Array:
		if v == nil {
			out.Kind = "null"
			break
		}
		out.Kind = "array"
		for _, item := range v.items {
			child, err := b.freeze(item, depth+1)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, child)
		}
	case []any:
		out.Kind = "array"
		for _, item := range v {
			child, err := b.freeze(item, depth+1)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, child)
		}
	case *Struct:
		if v == nil {
			out.Kind = "null"
			break
		}
		out.Kind = "struct"
		for _, key := range v.keys {
			if err := b.text(key); err != nil {
				return nil, err
			}
			child, err := b.freeze(v.vals[key], depth+1)
			if err != nil {
				return nil, err
			}
			out.Fields = append(out.Fields, sheet.DrilldownField{Name: key, Value: child})
		}
	case map[string]any:
		if len(v) > TabularPayloadMaxNodes {
			return nil, fmt.Errorf("расшифровка превышает лимит")
		}
		out.Kind = "struct"
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := b.text(key); err != nil {
				return nil, err
			}
			child, err := b.freeze(v[key], depth+1)
			if err != nil {
				return nil, err
			}
			out.Fields = append(out.Fields, sheet.DrilldownField{Name: key, Value: child})
		}
	default:
		return nil, fmt.Errorf("тип расшифровки не поддерживается")
	}
	if err := b.text(out.Text); err != nil {
		return nil, err
	}
	return out, nil
}

// RestoreTabularDrilldown creates new DSL collections for each invocation.
// The resolver validates every nonempty typed reference under the live actor.
func RestoreTabularDrilldown(value *sheet.DrilldownValue, resolve func(*sheet.Reference, string) (any, error)) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch value.Kind {
	case "null":
		return nil, nil
	case "string":
		return value.Text, nil
	case "bool":
		return value.Bool, nil
	case "float64":
		return strconv.ParseFloat(value.Text, 64)
	case "float32":
		v, e := strconv.ParseFloat(value.Text, 32)
		return float32(v), e
	case "int":
		v, e := strconv.ParseInt(value.Text, 10, 64)
		return int(v), e
	case "int64":
		return strconv.ParseInt(value.Text, 10, 64)
	case "int32":
		v, e := strconv.ParseInt(value.Text, 10, 32)
		return int32(v), e
	case "uint":
		v, e := strconv.ParseUint(value.Text, 10, 64)
		return uint(v), e
	case "uint64":
		return strconv.ParseUint(value.Text, 10, 64)
	case "uint32":
		v, e := strconv.ParseUint(value.Text, 10, 32)
		return uint32(v), e
	case "decimal":
		return decimal.NewFromString(value.Text)
	case "date":
		return time.Parse(time.RFC3339Nano, value.Text)
	case "reference":
		if resolve == nil || value.Ref == nil {
			return nil, fmt.Errorf("ссылка расшифровки недоступна")
		}
		return resolve(value.Ref, value.RefKind)
	case "array":
		a := &Array{}
		for _, item := range value.Items {
			v, err := RestoreTabularDrilldown(item, resolve)
			if err != nil {
				return nil, err
			}
			a.items = append(a.items, v)
		}
		return a, nil
	case "struct":
		s := NewStructFromMap(map[string]any{})
		for _, f := range value.Fields {
			v, err := RestoreTabularDrilldown(f.Value, resolve)
			if err != nil {
				return nil, err
			}
			s.Set(f.Name, v)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("недопустимая расшифровка")
	}
}
