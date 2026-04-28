package printform

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ApplyFormat formats value according to spec string (e.g. "date", "number:2").
func ApplyFormat(value any, spec string) string {
	if spec == "" {
		return anyToString(value)
	}
	parts := strings.SplitN(spec, ":", 2)
	name := strings.TrimSpace(parts[0])
	arg := ""
	if len(parts) == 2 {
		arg = strings.TrimSpace(parts[1])
	}

	switch name {
	case "date":
		return formatDate(value)
	case "datetime":
		return formatDateTime(value)
	case "number":
		n := 0
		if arg != "" {
			n, _ = strconv.Atoi(arg)
		}
		return formatNumber(value, n)
	case "currency":
		return formatNumber(value, 2)
	case "upper":
		return strings.ToUpper(anyToString(value))
	case "lower":
		return strings.ToLower(anyToString(value))
	case "default":
		s := anyToString(value)
		if s == "" || s == "<nil>" {
			return arg
		}
		return s
	}
	return anyToString(value)
}

func formatDate(v any) string {
	if t, ok := toTime(v); ok {
		return t.Format("02.01.2006")
	}
	return anyToString(v)
}

func formatDateTime(v any) string {
	if t, ok := toTime(v); ok {
		return t.Format("02.01.2006 15:04")
	}
	return anyToString(v)
}

func formatNumber(v any, decimals int) string {
	f, ok := toFloat(v)
	if !ok {
		return anyToString(v)
	}
	if decimals == 0 {
		return fmt.Sprintf("%d", int64(math.Round(f)))
	}
	return fmt.Sprintf("%.*f", decimals, f)
}

func toTime(v any) (time.Time, bool) {
	if t, ok := v.(time.Time); ok {
		return t, true
	}
	if s, ok := v.(string); ok {
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case int32:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.ReplaceAll(t, ",", "."), 64)
		return f, err == nil
	}
	return 0, false
}

func anyToString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
