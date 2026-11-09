package ui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// multiline: true у поля, привязанного к реквизиту формы (data_path: Форма.X
// или голое имя), рисует textarea, как у реквизита объекта. Раньше ветка
// реквизита формы рисовала однострочный input: описание задачи с сайта
// показывалось одной строкой. Через публичный GET формы объекта.
func TestManagedForm_MultilineFormAttributeIsTextarea(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "ПолеОписание", DataPath: "Форма.Описание", Multiline: true},
		{Kind: metadata.FormElementField, Name: "ПолеОписаниеТолькоЧтение", DataPath: "ОписаниеЧтение", Multiline: true, ReadOnly: true},
		{Kind: metadata.FormElementField, Name: "ПолеКоротко", DataPath: "Форма.Коротко"},
	})
	ent.Forms[0].Attributes = []*metadata.FormAttribute{
		{Name: "Описание", TypeRef: "string"},
		{Name: "ОписаниеЧтение", TypeRef: "string"},
		{Name: "Коротко", TypeRef: "string"},
	}
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "x"}, ent); err != nil {
		t.Fatal(err)
	}
	body := renderObjectFormGET(t, srv, ent, id)
	area := regexp.MustCompile(`<textarea name="Описание"[^>]*>`).FindString(body)
	if area == "" || !strings.Contains(area, `rows="5"`) {
		t.Fatalf("реквизит формы с multiline: нет textarea с rows: %q", area)
	}
	if regexp.MustCompile(`<input[^>]*name="Описание"`).MatchString(body) {
		t.Fatal("реквизит формы с multiline по-прежнему рисуется однострочным input")
	}
	ro := regexp.MustCompile(`<textarea name="ОписаниеЧтение"[^>]*>`).FindString(body)
	if ro == "" || !strings.Contains(ro, " readonly") {
		t.Fatalf("readonly-реквизит формы с multiline: %q", ro)
	}
	if !regexp.MustCompile(`<input type="text"[^>]*name="Коротко"`).MatchString(body) {
		t.Fatal("реквизит формы без multiline должен остаться однострочным")
	}
}
