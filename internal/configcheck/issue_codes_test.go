package configcheck

import "testing"

func TestNewResultAddsCodeAndSuggestedFix(t *testing.T) {
	res := NewResult([]Issue{{
		File:    "src/bad.os",
		Kind:    "DSL модуль",
		Message: `неизвестная функция "НетТакойФункции"`,
		Line:    2,
		Column:  3,
	}})

	if len(res.Issues) != 1 {
		t.Fatalf("ожидалась 1 ошибка, получено %d", len(res.Issues))
	}
	got := res.Issues[0]
	if got.Code != CodeDSLUnknownFunction {
		t.Fatalf("неверный code: %q", got.Code)
	}
	if got.SuggestedFix == "" {
		t.Fatalf("ожидалась suggestedFix: %+v", got)
	}
}

func TestNewResultPreservesExplicitCode(t *testing.T) {
	res := NewResult([]Issue{{
		Code:         "CUSTOM_CODE",
		Message:      "custom",
		SuggestedFix: "custom fix",
	}})

	got := res.Issues[0]
	if got.Code != "CUSTOM_CODE" || got.SuggestedFix != "custom fix" {
		t.Fatalf("явные поля не должны перезаписываться: %+v", got)
	}
}
