package interpreter_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func callMonthThroughDSL(t *testing.T, alias string, input time.Time, months float64) (time.Time, error) {
	t.Helper()
	source := fmt.Sprintf("Процедура Test()\n out(%s(inputdate, months))\nКонецПроцедуры", alias)
	program, err := parser.New(lexer.New(source, "month.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	var value any
	sink := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) { value = args[0]; return nil, nil })
	interp := interpreter.New()
	err = interp.Run(program.Procedures[0], nil, map[string]any{"inputdate": input, "months": months, "out": sink})
	if err != nil {
		return time.Time{}, err
	}
	result, ok := value.(time.Time)
	if !ok {
		return time.Time{}, fmt.Errorf("DSL returned %T, expected a date", value)
	}
	return result, nil
}

func TestAddMonthClampsDayThroughDSL(t *testing.T) {
	// All date builtins use the public toTime conversion to time.Local.
	loc := time.Local
	cases := []struct {
		name      string
		year      int
		month     time.Month
		day       int
		shift     float64
		wantYear  int
		wantMonth time.Month
		wantDay   int
	}{
		{"negative-eom", 2026, 12, 31, -1, 2026, 11, 30},
		{"leap-back-year", 2024, 2, 29, -12, 2023, 2, 28},
		{"january-common", 2026, 1, 31, 1, 2026, 2, 28},
		{"january-leap", 2024, 1, 31, 1, 2024, 2, 29},
		{"march-back-leap", 2024, 3, 31, -1, 2024, 2, 29},
		{"direct-two-months", 2026, 1, 31, 2, 2026, 3, 31},
		{"forward-year", 2026, 12, 31, 1, 2027, 1, 31},
		{"back-year", 2026, 1, 31, -1, 2025, 12, 31},
		{"ordinary-day", 2026, 4, 15, -2, 2026, 2, 15},
		{"zero", 2026, 12, 31, 0, 2026, 12, 31},
		{"fraction-truncates", 2026, 12, 31, -1.9, 2026, 11, 30},
	}
	for _, alias := range []string{"ДобавитьМесяц", "AddMonth"} {
		t.Run(alias, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					input := time.Date(tc.year, tc.month, tc.day, 13, 14, 15, 123456789, loc)
					want := time.Date(tc.wantYear, tc.wantMonth, tc.wantDay, 13, 14, 15, 123456789, loc)
					got, err := callMonthThroughDSL(t, alias, input, tc.shift)
					if err != nil {
						t.Fatal(err)
					}
					if !got.Equal(want) || got.Location() != loc {
						t.Fatalf("%s(%s,%v)=%s, want %s with original location", alias, input, tc.shift, got, want)
					}
				})
			}
		})
	}
}

func TestAddMonthPreservesLocalNormalizationThroughDSL(t *testing.T) {
	input := time.Date(2026, 12, 31, 13, 14, 15, 123456789, time.FixedZone("SourceUTC+14", 14*60*60))
	local := input.In(time.Local)
	want := time.Date(2026, 11, 30, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.Local)
	got, err := callMonthThroughDSL(t, "ДобавитьМесяц", input, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) || got.Location() != time.Local {
		t.Fatalf("public local date normalization changed: got %s, want %s", got, want)
	}
}

func TestAddMonthKeepsDateSafetyThroughDSL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input time.Time
		shift float64
	}{
		{"year-overflow", time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), 1},
		{"shift-overflow", time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := callMonthThroughDSL(t, "ДобавитьМесяц", tc.input, tc.shift); err == nil {
				t.Fatal("invalid calendar shift escaped the existing date safety guard")
			}
		})
	}
}
