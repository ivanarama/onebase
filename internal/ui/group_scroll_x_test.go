package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// scroll_x у горизонтальной группы: ряд кнопок не переносится на вторую
// строку, а прокручивается. Перенос рвёт панель действий пополам, и половина
// кнопок выглядит отдельным блоком в своей рамке.
func TestManagedGroup_ScrollXRendered(t *testing.T) {
	body := renderManagedFormBody(t, &metadata.FormElement{
		Kind:        metadata.FormElementGroupBox,
		Name:        "ГруппаДействия",
		Orientation: "horizontal",
		ScrollX:     true,
	})
	if !strings.Contains(body, "managed-group-scrollx") {
		t.Fatalf("класс прокрутки не попал в разметку группы:\n%s", body)
	}
	if !strings.Contains(body, ".managed-group-scrollx>.managed-group-body{flex-wrap:nowrap;overflow-x:auto") {
		t.Fatal("в стилях формы нет правила прокрутки")
	}
}

// Без ключа поведение прежнее — перенос на следующую строку.
func TestManagedGroup_WithoutScrollXWraps(t *testing.T) {
	body := renderManagedFormBody(t, &metadata.FormElement{
		Kind:        metadata.FormElementGroupBox,
		Name:        "ГруппаДействия",
		Orientation: "horizontal",
	})
	if strings.Contains(body, `data-ob-el="ГруппаДействия" class`) && strings.Contains(body, "managed-group-scrollx") {
		t.Fatal("группа без ключа получила прокрутку")
	}
}
