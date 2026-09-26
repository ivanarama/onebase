package ui

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Форма отдаёт локальное настенное время, база — UTC. Без приведения к одному
// моменту любая заполненная дата делала форму «изменённой», и сразу после
// успешной записи выскакивал диалог «Данные были изменены. Сохранить?».
func TestTpCellNormDateMatchesLocalAndUTC(t *testing.T) {
	field := metadata.Field{Name: "ДатаВызова", Type: metadata.FieldTypeDate}
	moment := time.Date(2026, 9, 26, 0, 0, 0, 0, time.Local)

	fromForm := tpCellNorm(field, moment.Format("2006-01-02T15:04"))
	fromStore := tpCellNorm(field, moment.UTC().Format("2006-01-02T15:04:05Z"))
	if fromForm == "" || fromForm != fromStore {
		t.Errorf("локальное и UTC-представление одного момента не совпали: %q vs %q", fromForm, fromStore)
	}

	other := tpCellNorm(field, moment.AddDate(0, 0, 1).Format("2006-01-02T15:04"))
	if other == fromForm {
		t.Errorf("разные даты дали одинаковую нормализацию: %q", other)
	}
	if tpCellNorm(field, "") != "" {
		t.Errorf("пустая дата должна нормализоваться в пустую строку, получено %q", tpCellNorm(field, ""))
	}
}
