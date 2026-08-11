package launcher

import (
	"strings"
	"testing"
)

// TestRoleMatrixIncludesAccountRegisters: регистры бухгалтерии попадают в матрицу
// прав под секцией «register» — чтобы админ мог выдать на них read (план 54, фикс).
func TestRoleMatrixIncludesAccountRegisters(t *testing.T) {
	data := &configuratorData{
		AccountRegisters: []cfgAccountRegister{{Name: "Хозрасчётный"}},
	}
	html := roleMatrixHTML(data, nil)
	if !strings.Contains(html, "Хозрасчётный") {
		t.Fatalf("регбух не попал в матрицу прав: %s", html)
	}
}
