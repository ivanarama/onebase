package storage

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// PostgreSQL NUMERIC without a typmod accepts at most 131072 digits before
// the decimal point and 16383 after it. Apply the same bounds before asking
// shopspring/decimal for a fixed-point string: Decimal.String/Round rescale by
// exponent and an input such as 1e2147483647 would otherwise try to allocate a
// multi-gigabyte big.Int/string on SQLite.
const (
	maxNumericIntegerDigits  = 131072
	maxNumericFractionDigits = 16383
	maxNumericInputBytes     = maxNumericIntegerDigits + maxNumericFractionDigits + 32
)

// canonicalNumberArg is the single storage-boundary conversion for number
// fields. It returns text deliberately: SQLite NUMBER columns are TEXT, while
// PostgreSQL accepts the same text for NUMERIC. This makes the physical value
// depend on field metadata instead of the API which supplied it.
//
// number(L,S) is rounded and rendered with exactly S fractional digits, just
// like PostgreSQL NUMERIC(L,S). A plain number has no declared scale, so only
// insignificant trailing zeroes/exponent spelling are removed.
func canonicalNumberArg(f metadata.Field, v any) (any, error) {
	if f.Type != metadata.FieldTypeNumber || v == nil {
		return v, nil
	}

	dec, empty, err := decimalFromStorageArg(v)
	if err != nil {
		return nil, i18nerr.Wrapf(err, "поле %q: некорректное число", f.Name)
	}
	if empty {
		return nil, nil
	}

	if f.Length == 0 && f.Scale == 0 {
		if err := checkUnboundedDecimalSize(dec); err != nil {
			return nil, i18nerr.Wrapf(err, "поле %q", f.Name)
		}
		return dec.String(), nil
	}
	if f.Length <= 0 || f.Scale < 0 || f.Scale > f.Length {
		return nil, i18nerr.Errorf("поле %q: некорректная разрядность (%d,%d)", f.Name, f.Length, f.Scale)
	}
	if f.Length > 1000 { // PostgreSQL's maximum declared NUMERIC precision.
		return nil, i18nerr.Errorf("поле %q: разрядность (%d,%d) превышает предел PostgreSQL (1000)", f.Name, f.Length, f.Scale)
	}

	text, err := fixedScaleDecimal(dec, f.Length, f.Scale)
	if err != nil {
		return nil, i18nerr.Wrapf(err, "поле %q", f.Name)
	}
	return text, nil
}

// normalizeRegField applies reference and numeric normalization for register
// columns. Register writers can report errors, so precision/format errors must
// not be silently replaced with the original, non-canonical value.
func normalizeRegField(d Dialect, f metadata.Field, v any) (any, error) {
	return canonicalNumberArg(f, normalizeRegArg(d, v, f.RefEntity != ""))
}

func decimalFromStorageArg(v any) (decimal.Decimal, bool, error) {
	switch value := v.(type) {
	case nil:
		return decimal.Decimal{}, true, nil
	case decimal.Decimal:
		return value, false, nil
	case *decimal.Decimal:
		if value == nil {
			return decimal.Decimal{}, true, nil
		}
		return *value, false, nil
	case pgtype.Numeric:
		if !value.Valid {
			return decimal.Decimal{}, true, nil
		}
		if value.NaN {
			return decimal.Decimal{}, false, fmt.Errorf("NaN не является конечным числом")
		}
		if value.InfinityModifier != pgtype.Finite {
			return decimal.Decimal{}, false, fmt.Errorf("%s не является конечным числом", value.InfinityModifier)
		}
		if value.Int == nil {
			return decimal.New(0, value.Exp), false, nil
		}
		return decimal.NewFromBigInt(value.Int, value.Exp), false, nil
	case string:
		return decimalFromBoundedString(value)
	case []byte:
		return decimalFromBoundedString(string(value))
	case json.Number:
		return decimalFromBoundedString(string(value))
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return decimal.Decimal{}, false, fmt.Errorf("%v не является конечным числом", value)
		}
		return decimal.NewFromFloat(value), false, nil
	case float32:
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return decimal.Decimal{}, false, fmt.Errorf("%v не является конечным числом", value)
		}
		return decimal.NewFromFloat32(value), false, nil
	case int:
		return decimal.NewFromInt(int64(value)), false, nil
	case int8:
		return decimal.NewFromInt(int64(value)), false, nil
	case int16:
		return decimal.NewFromInt(int64(value)), false, nil
	case int32:
		return decimal.NewFromInt(int64(value)), false, nil
	case int64:
		return decimal.NewFromInt(value), false, nil
	case uint:
		return decimalFromBoundedString(strconv.FormatUint(uint64(value), 10))
	case uint8:
		return decimal.NewFromInt(int64(value)), false, nil
	case uint16:
		return decimal.NewFromInt(int64(value)), false, nil
	case uint32:
		return decimal.NewFromInt(int64(value)), false, nil
	case uint64:
		return decimalFromBoundedString(strconv.FormatUint(value, 10))
	default:
		return decimal.Decimal{}, false, fmt.Errorf("тип %T не поддерживается", v)
	}
}

func decimalFromBoundedString(raw string) (decimal.Decimal, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return decimal.Decimal{}, true, nil
	}
	if len(value) > maxNumericInputBytes {
		return decimal.Decimal{}, false, fmt.Errorf("запись числа слишком длинная")
	}
	// HTML/DSL number inputs accept a decimal comma. Normalize it at the same
	// persistence boundary so this path cannot reintroduce API-dependent text.
	if strings.Count(value, ",") == 1 && !strings.Contains(value, ".") {
		value = strings.Replace(value, ",", ".", 1)
	}
	dec, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Decimal{}, false, err
	}
	return dec, false, nil
}

func fixedScaleDecimal(dec decimal.Decimal, length, scale int) (string, error) {
	if dec.IsZero() {
		return decimal.Zero.StringFixed(int32(scale)), nil //nolint:gosec // length <= 1000 above
	}

	// adjustedDigits is the position of the most-significant decimal digit:
	// 1e3 => 4, 0.01 => -1. It is computable without rescaling the coefficient.
	adjustedDigits := int64(dec.NumDigits()) + int64(dec.Exponent())
	integerLimit := int64(length - scale)
	if adjustedDigits > integerLimit {
		return "", fmt.Errorf("число превышает разрядность (%d,%d)", length, scale)
	}
	// The value is smaller than one unit beyond the retained fractional part,
	// hence certainly rounds to zero. Avoid Decimal.Round trying to build
	// 10^2147483644 for inputs such as 1e-2147483647.
	if adjustedDigits < -int64(scale) {
		return decimal.Zero.StringFixed(int32(scale)), nil //nolint:gosec // length <= 1000 above
	}

	rounded := dec.Round(int32(scale)) //nolint:gosec // length <= 1000 above
	if !rounded.IsZero() {
		roundedIntegerDigits := int64(rounded.NumDigits()) + int64(rounded.Exponent())
		if roundedIntegerDigits > integerLimit {
			return "", fmt.Errorf("число превышает разрядность (%d,%d)", length, scale)
		}
	}
	return rounded.StringFixed(int32(scale)), nil //nolint:gosec // length <= 1000 above
}

func checkUnboundedDecimalSize(dec decimal.Decimal) error {
	if dec.IsZero() {
		return nil
	}
	coefficient := strings.TrimPrefix(dec.Coefficient().String(), "-")
	significant := strings.TrimRight(coefficient, "0")
	trailingZeroes := len(coefficient) - len(significant)
	digits := int64(len(significant))
	exp := int64(dec.Exponent()) + int64(trailingZeroes)
	integerDigits := digits + exp
	if integerDigits > maxNumericIntegerDigits {
		return fmt.Errorf("число содержит больше %d целых разрядов", maxNumericIntegerDigits)
	}
	if exp < -maxNumericFractionDigits {
		return fmt.Errorf("число содержит больше %d дробных разрядов", maxNumericFractionDigits)
	}
	return nil
}
