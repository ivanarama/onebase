package ui

import (
	"context"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// hint_from через публичный GET формы: подсказка кнопки и надписи берётся из
// значения реквизита, пустое значение — статический hint; разметка несёт
// data-ob-hint-from, по которому клиент обновляет подсказку после события.
// Заодно — у кнопки теперь есть и статический hint (раньше title был только
// у горячей клавиши, и hint кнопки не показывался вовсе).
func TestManagedForm_HintFromAttribute(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementButton, Name: "КнПримечание", Hint: "Примечание направления", HintFrom: "Наименование"},
		{Kind: metadata.FormElementLabel, Name: "НдПримечание", HintFrom: "Наименование"},
		{Kind: metadata.FormElementButton, Name: "КнПросто", Hint: "Статическая подсказка"},
	})
	ctx := context.Background()
	render := func(name string) string {
		t.Helper()
		id := uuid.New()
		if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
			t.Fatal(err)
		}
		return renderObjectFormGET(t, srv, ent, id)
	}
	titleOf := func(body, el string) string {
		t.Helper()
		m := regexp.MustCompile(`data-ob-el="` + el + `"[^>]*?title="([^"]*)"|title="([^"]*)"[^>]*?data-ob-el="` + el + `"`).FindStringSubmatch(body)
		if m == nil {
			return ""
		}
		return m[1] + m[2]
	}
	body := render("Только после 18:00")
	if got := titleOf(body, "КнПримечание"); got != "Только после 18:00" {
		t.Errorf("кнопка: title %q, ждали значение реквизита", got)
	}
	if got := titleOf(body, "НдПримечание"); got != "Только после 18:00" {
		t.Errorf("надпись: title %q, ждали значение реквизита", got)
	}
	if got := titleOf(body, "КнПросто"); got != "Статическая подсказка" {
		t.Errorf("статический hint кнопки не показан: %q", got)
	}
	if !regexp.MustCompile(`data-ob-el="КнПримечание"[^>]*data-ob-hint-from="Наименование"`).MatchString(body) {
		t.Error("нет data-ob-hint-from — клиент не обновит подсказку после события")
	}
	if got := titleOf(render(""), "КнПримечание"); got != "Примечание направления" {
		t.Errorf("пустой реквизит: title %q, ждали статический hint", got)
	}
}

// hint_from у полей ввода всех видов (замечание ревью #2367): многострочное и
// числовое поле получают подсказку из реквизита и data-ob-hint-from; у числового
// поля без значения реквизита остаётся штатная подсказка о формате числа.
func TestManagedForm_HintFromOnInputKinds(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, nil)
	ctx := context.Background()
	ent.Fields = append(ent.Fields,
		metadata.Field{Name: "Цена", Type: metadata.FieldTypeNumber},
		metadata.Field{Name: "Примечание", Type: metadata.FieldTypeString},
	)
	if err := srv.store.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	ent.Forms[0].Elements = []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "ПлНаименование", DataPath: "Объект.Наименование", Multiline: true, HintFrom: "Примечание"},
		{Kind: metadata.FormElementField, Name: "ПлЦена", DataPath: "Объект.Цена", HintFrom: "Примечание"},
	}
	render := func(note string) string {
		t.Helper()
		id := uuid.New()
		if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Товар", "Цена": "10", "Примечание": note}, ent); err != nil {
			t.Fatal(err)
		}
		return renderObjectFormGET(t, srv, ent, id)
	}
	tagOf := func(body, tag, name string) string {
		t.Helper()
		m := regexp.MustCompile(`<` + tag + `\b[^>]*name="` + name + `"[^>]*>`).FindString(body)
		if m == "" {
			t.Fatalf("нет <%s name=%q> в форме", tag, name)
		}
		return m
	}
	titleIn := func(tag string) string {
		m := regexp.MustCompile(`title="([^"]*)"`).FindStringSubmatch(tag)
		if m == nil {
			return ""
		}
		return m[1]
	}
	body := render("Цена с НДС")
	area, price := tagOf(body, "textarea", "Наименование"), tagOf(body, "input", "Цена")
	if titleIn(area) != "Цена с НДС" || !regexp.MustCompile(`data-ob-hint-from="Примечание"`).MatchString(area) {
		t.Errorf("многострочное поле без hint_from: %s", area)
	}
	if titleIn(price) != "Цена с НДС" || !regexp.MustCompile(`data-ob-hint-from="Примечание"`).MatchString(price) {
		t.Errorf("числовое поле без hint_from: %s", price)
	}
	price = tagOf(render(""), "input", "Цена")
	const numberHint = "Введите число; десятичный разделитель — запятая или точка"
	if titleIn(price) != numberHint || !regexp.MustCompile(`data-ob-hint="`+numberHint+`"`).MatchString(price) {
		t.Errorf("пустой реквизит: у числового поля пропала штатная подсказка: %s", price)
	}
}
